package git

import (
	"path/filepath"
	"strconv"

	gogit "github.com/go-git/go-git/v5"
)

// Store resolves repository handles to bare repositories under a single root
// directory. Reads go through go-git; the ref-write and object-inspection
// operations (repo_write.go) shell out to the git binary, matching the locked
// design decision for the write path. It is safe for concurrent use: it holds
// only immutable configuration and opens a fresh handle per call.
type Store struct {
	root   string
	gitBin string // git binary for the write path; empty means "git" on PATH

	// maxBlobBytes caps the size of a blob a read may materialize into memory.
	// Zero leaves the built-in default; a negative value disables the cap.
	maxBlobBytes int64

	// pool holds long-lived cat-file --batch-check processes for ObjectExists
	// and ObjectType lookups, eliminating per-call spawn overhead on hot repos.
	pool  *catFilePool
	cache *objCache

// diffs caches parsed diffs by (pk, base, head) for ChangedFiles. The key
	// is content-addressed (two full object ids), so an entry never goes stale.
	diffs *diffCache

	// repos keeps warm go-git handles between requests so a point read does not
	// pay a cold pack-index parse. Handles are checked out exclusively (go-git
	// handles are not safe for concurrent use) and returned via Repo.Release;
	// InvalidateRepo drops a repository's handles after a push.
	repos *repoCache

	// overrides maps pk to an explicit filesystem path, bypassing the normal
	// root/{shard}/{pk}.git layout. Used by browse mode to point at an
	// arbitrary local repository without a managed tree.
	overrides map[int64]string
}

// defaultMaxBlobBytes is the blob size ceiling a fresh Store applies until the
// server overrides it. It matches GitHub's 100 MiB blob API limit, keeping a
// single oversized object from being read whole into server memory.
const defaultMaxBlobBytes = 100 << 20

// NewStore builds a Store rooted at dir (typically config.RepoRoot()).
func NewStore(dir string) *Store {
	s := &Store{root: dir, maxBlobBytes: defaultMaxBlobBytes}
	s.pool = newCatFilePool("git", 64)
	s.cache = newObjCache(objCacheMaxEntries)
	s.diffs = newDiffCache(diffCacheMaxBytes)
	s.repos = newRepoCache(repoCacheMaxRepos, repoCacheHandlesPerRepo)
	return s
}

// SetMaxBlobBytes overrides the blob size ceiling reads enforce. A positive
// value caps materialization at that many bytes; a negative value disables the
// cap; zero restores the built-in default. The server sets this from
// configuration.
func (s *Store) SetMaxBlobBytes(n int64) {
	if n == 0 {
		n = defaultMaxBlobBytes
	}
	s.maxBlobBytes = n
}

// SetGitBin overrides the git binary the write path execs. An empty value (the
// default) resolves "git" on PATH. The server sets this from configuration.
func (s *Store) SetGitBin(bin string) {
	s.gitBin = bin
	s.pool = newCatFilePool(s.bin(), 64)
}

// RegisterPath registers an explicit filesystem path for pk, overriding the
// normal root/{shard}/{pk}.git layout. Used by browse mode to serve an
// arbitrary local repository without a managed data tree. Set before the server
// starts; not safe to call concurrently with Open or Dir.
func (s *Store) RegisterPath(pk int64, path string) {
	if s.overrides == nil {
		s.overrides = make(map[int64]string)
	}
	s.overrides[pk] = path
}

// Dir returns the on-disk path of the bare repository for pk. Repositories are
// sharded by pk%256 to keep any single directory from holding the whole fleet:
// root/{pk%256}/{pk}.git.
func (s *Store) Dir(pk int64) string {
	if p, ok := s.overrides[pk]; ok {
		return p
	}
	shard := strconv.FormatInt(pk%256, 10)
	return filepath.Join(s.root, shard, strconv.FormatInt(pk, 10)+".git")
}

// Open opens the bare repository for pk for reading. It returns ErrRepoNotFound
// when no repository exists at the resolved path.
//
// The returned Repo may carry a warm handle from the store's repo cache; the
// caller should call Release when done with it so the handle (and its parsed
// pack index) is reused by the next request. A Repo that is never released
// still works; it just forfeits the reuse.
func (s *Store) Open(pk int64) (*Repo, error) {
	dir := s.Dir(pk)
	if _, overridden := s.overrides[pk]; overridden {
		// User-supplied path may be a working tree that changes outside our
		// control; detect the .git subdirectory and never cache the handle.
		r, err := gogit.PlainOpenWithOptions(dir, &gogit.PlainOpenOptions{DetectDotGit: true})
		if err != nil {
			return nil, ErrRepoNotFound
		}
		return &Repo{repo: r, maxBlobBytes: s.maxBlobBytes, store: s, pk: pk}, nil
	}
	h, gen, ok := s.repos.acquire(dir)
	if !ok {
		r, err := gogit.PlainOpen(dir)
		if err != nil {
			return nil, ErrRepoNotFound
		}
		h = r
	}
	return &Repo{repo: h, maxBlobBytes: s.maxBlobBytes, store: s, pk: pk, cacheDir: dir, cacheGen: gen}, nil
}

// InvalidateRepo drops the cached go-git handles for pk. The push path calls it
// after receive-pack runs: a push can write a new packfile, and a warm handle's
// lazily-built pack index would never see objects in it.
func (s *Store) InvalidateRepo(pk int64) {
	s.repos.invalidate(s.Dir(pk))
}

// blobSizes resolves the byte size of each sha, serving cache hits directly and
// resolving the rest in one pipelined cat-file --batch-check pass. It is the
// batch counterpart of catFileLookup, used by the recursive tree walk to avoid a
// per-entry object decode. Resolved sizes are memoized in the SHA cache (a git
// object id is a permanent content address). A sha that does not resolve is
// simply absent from the returned map; the caller leaves that entry's size zero.
func (s *Store) blobSizes(pk int64, shas []string) (map[string]int64, error) {
	out := make(map[string]int64, len(shas))
	var miss []string
	seen := make(map[string]struct{}, len(shas))
	for _, sha := range shas {
		if _, ok := seen[sha]; ok {
			continue
		}
		seen[sha] = struct{}{}
		if info, ok := s.cache.get(sha); ok {
			if !info.missing {
				out[sha] = info.size
			}
			continue
		}
		miss = append(miss, sha)
	}
	if len(miss) == 0 {
		return out, nil
	}
	infos, err := s.pool.lookupBatch(s.Dir(pk), pk, miss)
	if err != nil {
		return nil, err
	}
	for i, sha := range miss {
		s.cache.put(sha, infos[i])
		if !infos[i].missing {
			out[sha] = infos[i].size
		}
	}
	return out, nil
}

// Init creates an empty bare repository for pk and returns it. It is used by
// tests and, from M3, by repository creation. An existing repository is opened
// rather than reinitialized.
func (s *Store) Init(pk int64) (*Repo, error) {
	dir := s.Dir(pk)
	r, err := gogit.PlainInit(dir, true)
	if err != nil {
		if r, err = gogit.PlainOpen(dir); err != nil {
			return nil, err
		}
	}
	return &Repo{repo: r, maxBlobBytes: s.maxBlobBytes, store: s, pk: pk}, nil
}

// Close shuts down the long-lived cat-file processes the pool holds. The server
// calls it on shutdown so the helper processes do not outlive the store.
func (s *Store) Close() {
	if s.pool != nil {
		s.pool.close()
	}
}

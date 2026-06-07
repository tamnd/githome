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

// Dir returns the on-disk path of the bare repository for pk. Repositories are
// sharded by pk%256 to keep any single directory from holding the whole fleet:
// root/{pk%256}/{pk}.git.
func (s *Store) Dir(pk int64) string {
	shard := strconv.FormatInt(pk%256, 10)
	return filepath.Join(s.root, shard, strconv.FormatInt(pk, 10)+".git")
}

// Open opens the bare repository for pk for reading. It returns ErrRepoNotFound
// when no repository exists at the resolved path.
func (s *Store) Open(pk int64) (*Repo, error) {
	r, err := gogit.PlainOpen(s.Dir(pk))
	if err != nil {
		return nil, ErrRepoNotFound
	}
	return &Repo{repo: r, maxBlobBytes: s.maxBlobBytes}, nil
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
	return &Repo{repo: r, maxBlobBytes: s.maxBlobBytes}, nil
}

// Close shuts down the long-lived cat-file processes the pool holds. The server
// calls it on shutdown so the helper processes do not outlive the store.
func (s *Store) Close() {
	if s.pool != nil {
		s.pool.close()
	}
}

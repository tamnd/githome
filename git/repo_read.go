package git

import (
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
)

// maxTreeEntries caps a recursive tree walk. GitHub truncates very large trees;
// we stop at the same ceiling and report Truncated so clients can fall back to
// per-directory listings.
const maxTreeEntries = 100000

// Repo is a single bare repository opened for reading.
type Repo struct {
	repo *gogit.Repository

	// maxBlobBytes caps the size of a blob blobByHash will read into memory.
	// A positive value rejects larger blobs with ErrBlobTooLarge before the
	// read; zero or negative disables the guard.
	maxBlobBytes int64

	// store and pk let a recursive tree walk resolve blob sizes in one pipelined
	// cat-file --batch-check pass through the store's pooled process (which reads
	// the pack indexes and commit-graph the ingest maintenance builds) instead of
	// decoding every blob object through go-git one at a time. A Repo built
	// without a store (some tests) falls back to the per-object go-git lookup.
	store *Store
	pk    int64

	// cacheDir and cacheGen tie this handle back to the store's repo cache so
	// Release can return it for reuse. A Repo opened outside the cache (override
	// paths, Init, tests) has an empty cacheDir and Release is a no-op.
	cacheDir string
	cacheGen uint64
}

// Release returns the underlying go-git handle to the store's warm-handle cache
// for the next request to reuse, keeping the parsed pack index warm. The Repo
// must not be used after Release: another goroutine may check the handle out
// immediately. Releasing is optional; an unreleased handle is just collected.
func (r *Repo) Release() {
	if r == nil || r.store == nil || r.cacheDir == "" || r.repo == nil {
		return
	}
	h := r.repo
	r.repo = nil
	r.store.repos.release(r.cacheDir, r.cacheGen, h)
}

// HEAD resolves the repository's default branch to its short name and head
// commit. It returns ErrEmptyRepository when the repository has no commits.
func (r *Repo) HEAD() (Branch, error) {
	ref, err := r.repo.Head()
	if err != nil {
		return Branch{}, ErrEmptyRepository
	}
	return Branch{Name: ref.Name().Short(), Commit: ref.Hash().String()}, nil
}

// Branches lists the repository's branches in name order. An empty repository
// yields an empty slice, not an error.
func (r *Repo) Branches() ([]Branch, error) {
	if out, ok := r.branchesBatch(); ok {
		return out, nil
	}
	iter, err := r.repo.Branches()
	if err != nil {
		return nil, err
	}
	var out []Branch
	err = iter.ForEach(func(ref *plumbing.Reference) error {
		out = append(out, Branch{Name: ref.Name().Short(), Commit: ref.Hash().String()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Tags lists the repository's tags in name order, peeling annotated tags to the
// commit they point at and carrying the tag object metadata alongside.
func (r *Repo) Tags() ([]Tag, error) {
	if out, ok := r.tagsBatch(); ok {
		return out, nil
	}
	iter, err := r.repo.Tags()
	if err != nil {
		return nil, err
	}
	var out []Tag
	err = iter.ForEach(func(ref *plumbing.Reference) error {
		t := Tag{Name: ref.Name().Short()}
		if tagObj, err := r.repo.TagObject(ref.Hash()); err == nil {
			commit, cerr := tagObj.Commit()
			if cerr == nil {
				t.Commit = commit.Hash.String()
			}
			t.Annotated = &AnnotatedTag{
				SHA:        ref.Hash().String(),
				Tagger:     sigFrom(tagObj.Tagger),
				Message:    tagObj.Message,
				Target:     tagObj.Target.String(),
				TargetType: objType(tagObj.TargetType),
			}
		} else {
			t.Commit = ref.Hash().String()
		}
		out = append(out, t)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Refs lists every branch and tag ref fully qualified, in name order. The
// target is the object the ref names directly: a commit for branches and
// lightweight tags, the tag object for annotated tags.
func (r *Repo) Refs() ([]Ref, error) {
	if out, ok := r.refsBatch(); ok {
		return out, nil
	}
	iter, err := r.repo.References()
	if err != nil {
		return nil, err
	}
	var out []Ref
	err = iter.ForEach(func(ref *plumbing.Reference) error {
		if ref.Type() != plumbing.HashReference {
			return nil
		}
		name := ref.Name().String()
		if !strings.HasPrefix(name, "refs/heads/") && !strings.HasPrefix(name, "refs/tags/") {
			return nil
		}
		typ := ObjectCommit
		if _, err := r.repo.TagObject(ref.Hash()); err == nil {
			typ = ObjectTag
		}
		out = append(out, Ref{Name: name, Target: ref.Hash().String(), Type: typ})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// RefByName resolves a single reference. The name may be fully qualified
// (refs/heads/main) or carry just the suffix the REST API uses (heads/main,
// tags/v1.0).
func (r *Repo) RefByName(name string) (Ref, error) {
	full := name
	if !strings.HasPrefix(full, "refs/") {
		full = "refs/" + name
	}
	ref, err := r.repo.Reference(plumbing.ReferenceName(full), false)
	if err != nil || ref.Type() != plumbing.HashReference {
		return Ref{}, ErrObjectNotFound
	}
	typ := ObjectCommit
	if _, err := r.repo.TagObject(ref.Hash()); err == nil {
		typ = ObjectTag
	}
	return Ref{Name: full, Target: ref.Hash().String(), Type: typ}, nil
}

// ResolveCommit resolves any revision (a sha, HEAD, a branch or tag name, or an
// expression like HEAD~2) to its commit sha.
func (r *Repo) ResolveCommit(rev string) (SHA, error) {
	c, err := r.commitFromRev(rev)
	if err != nil {
		return "", err
	}
	return c.Hash.String(), nil
}

// Commit loads a single commit by any revision.
func (r *Repo) Commit(rev string) (Commit, error) {
	c, err := r.commitFromRev(rev)
	if err != nil {
		return Commit{}, err
	}
	return commitValue(c), nil
}

// Tree loads a tree by any revision (a tree sha, a commit, or a ref). When the
// revision names a commit it resolves to that commit's root tree. With
// recursive set it walks the whole subtree, stopping and reporting Truncated at
// the entry ceiling.
func (r *Repo) Tree(rev string, recursive bool) (Tree, error) {
	t, err := r.treeFromRev(rev)
	if err != nil {
		return Tree{}, err
	}
	out := Tree{SHA: t.Hash.String()}
	if !recursive {
		for _, e := range t.Entries {
			out.Entries = append(out.Entries, treeEntryValue(e.Name, e))
		}
		r.fillTreeSizes(out.Entries)
		return out, nil
	}
	walker := object.NewTreeWalker(t, true, nil)
	defer walker.Close()
	for {
		name, entry, err := walker.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Tree{}, err
		}
		if len(out.Entries) >= maxTreeEntries {
			out.Truncated = true
			break
		}
		out.Entries = append(out.Entries, treeEntryValue(name, entry))
	}
	r.fillTreeSizes(out.Entries)
	return out, nil
}

// Blob loads a blob by any revision pointing at one (typically a blob sha).
func (r *Repo) Blob(rev string) (Blob, error) {
	h, err := r.resolveHash(rev)
	if err != nil {
		return Blob{}, ErrObjectNotFound
	}
	return r.blobByHash(h)
}

// PathAt resolves a path within the tree of rev. A blob yields a file result
// with content; a tree yields a directory listing. An empty path lists the root
// tree.
func (r *Repo) PathAt(rev, path string) (PathResult, error) {
	t, err := r.treeFromRev(rev)
	if err != nil {
		return PathResult{}, err
	}
	path = strings.Trim(path, "/")
	if path == "" {
		return PathResult{IsDir: true, Dir: r.listTree(t, "")}, nil
	}
	entry, err := t.FindEntry(path)
	if err != nil {
		return PathResult{}, ErrPathNotFound
	}
	if entry.Mode == filemode.Dir {
		sub, err := t.Tree(path)
		if err != nil {
			return PathResult{}, ErrPathNotFound
		}
		return PathResult{IsDir: true, Dir: r.listTree(sub, path)}, nil
	}
	blob, err := r.blobByHash(entry.Hash)
	if err != nil {
		return PathResult{}, err
	}
	return PathResult{
		IsDir: false,
		Entry: PathEntry{
			Name: entry.Name,
			Path: path,
			Type: entryType(entry.Mode),
			Mode: modeString(entry.Mode),
			SHA:  entry.Hash.String(),
			Size: blob.Size,
		},
		File: &blob,
	}, nil
}

// Log walks commit history from opts.From, optionally filtered to a path.
func (r *Repo) Log(opts LogOpts) ([]Commit, error) {
	limit := opts.Max
	if limit <= 0 {
		limit = 30
	}
	if out, ok := r.logBatch(opts, limit); ok {
		return out, nil
	}
	h, err := r.repo.ResolveRevision(plumbing.Revision(opts.From))
	if err != nil {
		return nil, ErrObjectNotFound
	}
	lo := &gogit.LogOptions{From: *h, Since: opts.Since, Until: opts.Until}
	if opts.Path != "" {
		p := opts.Path
		lo.PathFilter = func(s string) bool { return s == p || strings.HasPrefix(s, p+"/") }
	}
	iter, err := r.repo.Log(lo)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	skip := opts.Skip
	var out []Commit
	err = iter.ForEach(func(c *object.Commit) error {
		if !matchIdent(c.Author, opts.Author) || !matchIdent(c.Committer, opts.Committer) {
			return nil
		}
		if skip > 0 {
			skip--
			return nil
		}
		if len(out) >= limit {
			return storer.ErrStop
		}
		out = append(out, commitValue(c))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// matchIdent reports whether the signature's name or email contains the
// filter, case-insensitively. An empty filter matches everything, so the
// unfiltered walk pays nothing. It mirrors the -i --author/--committer match
// the subprocess path runs.
func matchIdent(sig object.Signature, filter string) bool {
	if filter == "" {
		return true
	}
	f := strings.ToLower(filter)
	return strings.Contains(strings.ToLower(sig.Name), f) ||
		strings.Contains(strings.ToLower(sig.Email), f)
}

// resolveHash resolves a revision to an object hash. A full 40-hex id is taken
// literally (go-git's ResolveRevision only resolves commit-ish revisions, so a
// raw blob or tree sha would not resolve); anything else, including HEAD,
// branch and tag names, and expressions like HEAD~2, goes through go-git.
func (r *Repo) resolveHash(rev string) (plumbing.Hash, error) {
	if h, ok := asHash(rev); ok {
		return h, nil
	}
	hp, err := r.repo.ResolveRevision(plumbing.Revision(rev))
	if err != nil {
		return plumbing.ZeroHash, err
	}
	return *hp, nil
}

// commitFromRev resolves a revision to its commit object, peeling an annotated
// tag if the revision names one.
func (r *Repo) commitFromRev(rev string) (*object.Commit, error) {
	h, err := r.resolveHash(rev)
	if err != nil {
		if rev == "HEAD" {
			return nil, ErrEmptyRepository
		}
		return nil, ErrObjectNotFound
	}
	if c, err := r.repo.CommitObject(h); err == nil {
		return c, nil
	}
	if tagObj, err := r.repo.TagObject(h); err == nil {
		return tagObj.Commit()
	}
	return nil, ErrObjectNotFound
}

// treeFromRev resolves a revision to a tree: a tree sha directly, or the root
// tree of the commit it names.
func (r *Repo) treeFromRev(rev string) (*object.Tree, error) {
	h, err := r.resolveHash(rev)
	if err != nil {
		if rev == "HEAD" {
			return nil, ErrEmptyRepository
		}
		return nil, ErrObjectNotFound
	}
	if t, err := r.repo.TreeObject(h); err == nil {
		return t, nil
	}
	c, err := r.commitFromRev(rev)
	if err != nil {
		return nil, err
	}
	return c.Tree()
}

// asHash reports whether s is a full 40-character lowercase hex object id and,
// if so, returns it as a plumbing.Hash.
func asHash(s string) (plumbing.Hash, bool) {
	if len(s) != 40 {
		return plumbing.ZeroHash, false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return plumbing.ZeroHash, false
		}
	}
	return plumbing.NewHash(s), true
}

func (r *Repo) blobByHash(h plumbing.Hash) (Blob, error) {
	b, err := r.repo.BlobObject(h)
	if err != nil {
		return Blob{}, ErrObjectNotFound
	}
	// The blob header carries the true size, so reject an oversized object before
	// reading a single byte rather than after buffering it.
	if r.maxBlobBytes > 0 && b.Size > r.maxBlobBytes {
		return Blob{}, ErrBlobTooLarge
	}
	reader, err := b.Reader()
	if err != nil {
		return Blob{}, err
	}
	defer func() { _ = reader.Close() }()
	// Bound the read as a backstop in case the header size understates the stream:
	// LimitReader at the cap plus one byte lets a truthful blob through whole while
	// a lying one trips the ceiling check below instead of exhausting memory.
	rd := io.Reader(reader)
	if r.maxBlobBytes > 0 {
		rd = io.LimitReader(reader, r.maxBlobBytes+1)
	}
	content, err := io.ReadAll(rd)
	if err != nil {
		return Blob{}, err
	}
	if r.maxBlobBytes > 0 && int64(len(content)) > r.maxBlobBytes {
		return Blob{}, ErrBlobTooLarge
	}
	return Blob{SHA: h.String(), Size: b.Size, Content: content}, nil
}

// listTree turns a tree's immediate entries into PathEntry values rooted at
// prefix (the tree's own path, empty for the root). Blob sizes are filled in one
// batch (see blobSizeMap) rather than a per-entry object decode.
func (r *Repo) listTree(t *object.Tree, prefix string) []PathEntry {
	out := make([]PathEntry, 0, len(t.Entries))
	var blobShas []string
	for _, e := range t.Entries {
		full := e.Name
		if prefix != "" {
			full = prefix + "/" + e.Name
		}
		pe := PathEntry{
			Name: e.Name,
			Path: full,
			Type: entryType(e.Mode),
			Mode: modeString(e.Mode),
			SHA:  e.Hash.String(),
		}
		if pe.Type == ObjectBlob {
			blobShas = append(blobShas, pe.SHA)
		}
		out = append(out, pe)
	}
	sizes := r.blobSizeMap(blobShas)
	for i := range out {
		if out[i].Type == ObjectBlob {
			out[i].Size = sizes[out[i].SHA]
		}
	}
	return out
}

// treeEntryValue builds a TreeEntry without its blob size; Tree fills sizes in
// one batch pass afterward via fillTreeSizes.
func treeEntryValue(path string, e object.TreeEntry) TreeEntry {
	return TreeEntry{
		Path: path,
		Mode: modeString(e.Mode),
		Type: entryType(e.Mode),
		SHA:  e.Hash.String(),
	}
}

// fillTreeSizes populates the Size of every blob entry in place, resolving all
// blob sizes in one batch (see blobSizeMap).
func (r *Repo) fillTreeSizes(entries []TreeEntry) {
	var blobShas []string
	for i := range entries {
		if entries[i].Type == ObjectBlob {
			blobShas = append(blobShas, entries[i].SHA)
		}
	}
	sizes := r.blobSizeMap(blobShas)
	for i := range entries {
		if entries[i].Type == ObjectBlob {
			entries[i].Size = sizes[entries[i].SHA]
		}
	}
}

// blobSizeMap resolves the byte size of each blob sha. With a store it uses one
// pipelined cat-file --batch-check pass through the pooled process (which reads
// the pack indexes and commit-graph the ingest maintenance builds, and caches by
// content address); without one, or if that pass errors, it falls back to a
// per-blob go-git object decode. Shas that do not resolve are absent from the map.
func (r *Repo) blobSizeMap(shas []string) map[string]int64 {
	if len(shas) == 0 {
		return nil
	}
	if r.store != nil {
		if sizes, err := r.store.blobSizes(r.pk, shas); err == nil {
			return sizes
		}
	}
	sizes := make(map[string]int64, len(shas))
	for _, sha := range shas {
		if b, err := r.repo.BlobObject(plumbing.NewHash(sha)); err == nil {
			sizes[sha] = b.Size
		}
	}
	return sizes
}

func commitValue(c *object.Commit) Commit {
	out := Commit{
		SHA:       c.Hash.String(),
		Tree:      c.TreeHash.String(),
		Author:    sigFrom(c.Author),
		Committer: sigFrom(c.Committer),
		Message:   c.Message,
	}
	for _, p := range c.ParentHashes {
		out.Parents = append(out.Parents, p.String())
	}
	return out
}

func sigFrom(s object.Signature) Signature {
	return Signature{Name: s.Name, Email: s.Email, When: s.When.UTC()}
}

func entryType(m filemode.FileMode) ObjectType {
	switch m {
	case filemode.Dir:
		return ObjectTree
	case filemode.Submodule:
		return ObjectCommit
	default:
		return ObjectBlob
	}
}

// modeString renders the six-digit octal mode git stores in a tree, matching the
// strings GitHub emits.
func modeString(m filemode.FileMode) string {
	switch m {
	case filemode.Dir:
		return "040000"
	case filemode.Submodule:
		return "160000"
	case filemode.Symlink:
		return "120000"
	case filemode.Executable:
		return "100755"
	default:
		return "100644"
	}
}

func objType(t plumbing.ObjectType) ObjectType {
	switch t {
	case plumbing.CommitObject:
		return ObjectCommit
	case plumbing.TreeObject:
		return ObjectTree
	case plumbing.TagObject:
		return ObjectTag
	default:
		return ObjectBlob
	}
}

// BlameLine is one annotated source line from a blame operation: the commit
// that last changed the line, the author, the timestamp, the raw text, and the
// 1-based line number.
type BlameLine struct {
	SHA         string
	AuthorName  string
	AuthorEmail string
	When        time.Time
	Text        string
	LineNum     int
}

// Blame annotates every line of path at ref with the commit that last changed
// it. It returns ErrObjectNotFound when the path does not exist in the tree at
// ref, and ErrRepoNotFound for other resolution failures.
func (r *Repo) Blame(ref, path string) ([]BlameLine, error) {
	if lines, err, handled := r.blameBatch(ref, path); handled {
		return lines, err
	}
	c, err := r.commitFromRev(ref)
	if err != nil {
		return nil, err
	}
	result, err := gogit.Blame(c, path)
	if err != nil {
		if errors.Is(err, object.ErrFileNotFound) {
			return nil, ErrObjectNotFound
		}
		return nil, ErrRepoNotFound
	}
	lines := make([]BlameLine, len(result.Lines))
	for i, l := range result.Lines {
		lines[i] = BlameLine{
			SHA:         l.Hash.String(),
			AuthorName:  l.AuthorName,
			AuthorEmail: l.Author,
			When:        l.Date,
			Text:        l.Text,
			LineNum:     i + 1,
		}
	}
	return lines, nil
}

// CommitPatch returns the unified diff patch of sha against its first parent.
// For the initial commit (no parents) it returns an empty string. The patch is
// in standard unified-diff format; the handler renders it through the markup
// pipeline.
func (r *Repo) CommitPatch(sha string) (string, error) {
	if patch, handled := r.commitPatchBatch(sha); handled {
		return patch, nil
	}
	c, err := r.commitFromRev(sha)
	if err != nil {
		return "", err
	}
	if c.NumParents() == 0 {
		return "", nil
	}
	parent, err := c.Parent(0)
	if err != nil {
		return "", ErrObjectNotFound
	}
	patch, err := parent.Patch(c)
	if err != nil {
		return "", err
	}
	return patch.String(), nil
}

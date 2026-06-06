package git

import (
	"io"
	"sort"
	"strings"

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
			out.Entries = append(out.Entries, r.entryValue(e.Name, e))
		}
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
		out.Entries = append(out.Entries, r.entryValue(name, entry))
	}
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
	h, err := r.repo.ResolveRevision(plumbing.Revision(opts.From))
	if err != nil {
		return nil, ErrObjectNotFound
	}
	lo := &gogit.LogOptions{From: *h}
	if opts.Path != "" {
		p := opts.Path
		lo.PathFilter = func(s string) bool { return s == p || strings.HasPrefix(s, p+"/") }
	}
	iter, err := r.repo.Log(lo)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	limit := opts.Max
	if limit <= 0 {
		limit = 30
	}
	var out []Commit
	err = iter.ForEach(func(c *object.Commit) error {
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
// prefix (the tree's own path, empty for the root).
func (r *Repo) listTree(t *object.Tree, prefix string) []PathEntry {
	out := make([]PathEntry, 0, len(t.Entries))
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
			if b, err := r.repo.BlobObject(e.Hash); err == nil {
				pe.Size = b.Size
			}
		}
		out = append(out, pe)
	}
	return out
}

// entryValue builds a TreeEntry, filling the blob size with an object lookup.
func (r *Repo) entryValue(path string, e object.TreeEntry) TreeEntry {
	te := TreeEntry{
		Path: path,
		Mode: modeString(e.Mode),
		Type: entryType(e.Mode),
		SHA:  e.Hash.String(),
	}
	if te.Type == ObjectBlob {
		if b, err := r.repo.BlobObject(e.Hash); err == nil {
			te.Size = b.Size
		}
	}
	return te
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

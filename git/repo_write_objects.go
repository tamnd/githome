package git

import (
	"fmt"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// CreateBlobInput holds content for POST /git/blobs.
type CreateBlobInput struct {
	Content  []byte // already decoded (caller converts from utf-8 or base64)
}

// CreateBlobResult holds the outcome of CreateBlob.
type CreateBlobResult struct {
	SHA  string
	Size int64
}

// CreateBlob stores a blob object and returns its SHA.
func (r *Repo) CreateBlob(in CreateBlobInput) (*CreateBlobResult, error) {
	st := r.repo.Storer
	obj := st.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	w, err := obj.Writer()
	if err != nil {
		return nil, fmt.Errorf("git: blob writer: %w", err)
	}
	if _, err = w.Write(in.Content); err != nil {
		return nil, fmt.Errorf("git: write blob: %w", err)
	}
	h, err := st.SetEncodedObject(obj)
	if err != nil {
		return nil, fmt.Errorf("git: store blob: %w", err)
	}
	return &CreateBlobResult{SHA: h.String(), Size: int64(len(in.Content))}, nil
}

// CreateTreeEntry is one entry passed to CreateTree.
type CreateTreeEntry struct {
	Path    string
	Mode    string // "100644", "100755", "040000", "160000", "120000"
	Type    ObjectType // "blob", "tree", "commit"
	SHA     string // object SHA; empty string is allowed for inline blobs
	Content []byte // non-nil: create an inline blob first
}

// CreateTreeResult holds the outcome of CreateTree.
type CreateTreeResult struct {
	SHA     string
	Entries []TreeEntry
}

// CreateTree builds a new tree object from baseTreeSHA (may be empty) by
// overlaying entries. An empty SHA with Content creates an inline blob. A
// SHA of "" (with nil Content) removes the path from the base tree.
func (r *Repo) CreateTree(baseTreeSHA string, entries []CreateTreeEntry) (*CreateTreeResult, error) {
	st := r.repo.Storer

	// Load base tree if provided.
	var base *object.Tree
	if baseTreeSHA != "" {
		t, err := object.GetTree(st, plumbing.NewHash(baseTreeSHA))
		if err != nil {
			return nil, fmt.Errorf("git: load base tree %s: %w", baseTreeSHA, err)
		}
		base = t
	}

	// Build a flat entry map from the base tree.
	entryMap := make(map[string]object.TreeEntry)
	if base != nil {
		for _, e := range base.Entries {
			entryMap[e.Name] = e
		}
	}

	// Apply the overlay entries.
	for _, e := range entries {
		sha := e.SHA
		if len(e.Content) > 0 {
			// Inline blob: create it first.
			res, err := r.CreateBlob(CreateBlobInput{Content: e.Content})
			if err != nil {
				return nil, err
			}
			sha = res.SHA
		}
		if sha == "" {
			// Deletion: remove from map.
			delete(entryMap, e.Path)
			continue
		}
		mode := objectMode(e.Mode, e.Type)
		entryMap[e.Path] = object.TreeEntry{
			Name: e.Path,
			Mode: mode,
			Hash: plumbing.NewHash(sha),
		}
	}

	// Rebuild the tree entries slice.
	treeObj := &object.Tree{}
	for _, e := range entryMap {
		treeObj.Entries = append(treeObj.Entries, e)
	}

	encoded := st.NewEncodedObject()
	if err := treeObj.Encode(encoded); err != nil {
		return nil, fmt.Errorf("git: encode tree: %w", err)
	}
	h, err := st.SetEncodedObject(encoded)
	if err != nil {
		return nil, fmt.Errorf("git: store tree: %w", err)
	}

	// Build result entries.
	out := make([]TreeEntry, 0, len(treeObj.Entries))
	for _, e := range treeObj.Entries {
		out = append(out, TreeEntry{
			Path: e.Name,
			Mode: e.Mode.String(),
			Type: modeObjectType(e.Mode),
			SHA:  e.Hash.String(),
		})
	}
	return &CreateTreeResult{SHA: h.String(), Entries: out}, nil
}

// CreateCommitInput holds parameters for POST /git/commits.
type CreateCommitInput struct {
	Message    string
	Tree       string
	Parents    []string
	Author     Signature
	Committer  Signature
}

// CreateCommitResult holds the outcome of CreateCommit.
type CreateCommitResult struct {
	SHA string
}

// CreateCommit builds a new commit object from the given tree and parents.
func (r *Repo) CreateCommit(in CreateCommitInput) (*CreateCommitResult, error) {
	st := r.repo.Storer
	when := time.Now()
	author := object.Signature{
		Name:  in.Author.Name,
		Email: in.Author.Email,
		When:  in.Author.When,
	}
	if author.When.IsZero() {
		author.When = when
	}
	committer := object.Signature{
		Name:  in.Committer.Name,
		Email: in.Committer.Email,
		When:  in.Committer.When,
	}
	if committer.Name == "" {
		committer = author
	}
	if committer.When.IsZero() {
		committer.When = when
	}

	parentHashes := make([]plumbing.Hash, 0, len(in.Parents))
	for _, p := range in.Parents {
		parentHashes = append(parentHashes, plumbing.NewHash(p))
	}

	c := &object.Commit{
		Author:       author,
		Committer:    committer,
		Message:      in.Message,
		TreeHash:     plumbing.NewHash(in.Tree),
		ParentHashes: parentHashes,
	}

	obj := st.NewEncodedObject()
	if err := c.Encode(obj); err != nil {
		return nil, fmt.Errorf("git: encode commit: %w", err)
	}
	h, err := st.SetEncodedObject(obj)
	if err != nil {
		return nil, fmt.Errorf("git: store commit: %w", err)
	}
	return &CreateCommitResult{SHA: h.String()}, nil
}

// CreateTagInput holds parameters for POST /git/tags.
type CreateTagInput struct {
	Tag        string
	Message    string
	ObjectSHA  string
	ObjectType string // "commit", "tree", "blob"
	Tagger     Signature
}

// CreateTagResult holds the outcome of CreateTag.
type CreateTagResult struct {
	SHA     string
	Tag     string
	Message string
	Object  string // SHA of the tagged object
	Type    string
	Tagger  Signature
}

// CreateTag creates an annotated tag object.
func (r *Repo) CreateTag(in CreateTagInput) (*CreateTagResult, error) {
	st := r.repo.Storer
	when := time.Now()
	tagger := object.Signature{
		Name:  in.Tagger.Name,
		Email: in.Tagger.Email,
		When:  in.Tagger.When,
	}
	if tagger.When.IsZero() {
		tagger.When = when
	}

	tt := plumbing.CommitObject
	switch in.ObjectType {
	case "tree":
		tt = plumbing.TreeObject
	case "blob":
		tt = plumbing.BlobObject
	case "tag":
		tt = plumbing.TagObject
	}

	t := &object.Tag{
		Name:       in.Tag,
		Message:    in.Message,
		Tagger:     tagger,
		Target:     plumbing.NewHash(in.ObjectSHA),
		TargetType: tt,
	}
	obj := st.NewEncodedObject()
	if err := t.Encode(obj); err != nil {
		return nil, fmt.Errorf("git: encode tag: %w", err)
	}
	h, err := st.SetEncodedObject(obj)
	if err != nil {
		return nil, fmt.Errorf("git: store tag: %w", err)
	}
	return &CreateTagResult{
		SHA:     h.String(),
		Tag:     in.Tag,
		Message: in.Message,
		Object:  in.ObjectSHA,
		Type:    in.ObjectType,
		Tagger:  Signature{Name: tagger.Name, Email: tagger.Email, When: tagger.When},
	}, nil
}

// GetTag reads an annotated tag object by SHA.
type GetTagResult struct {
	SHA     string
	Tag     string
	Message string
	Object  string
	Type    string
	Tagger  Signature
}

func (r *Repo) GetTag(sha string) (*GetTagResult, error) {
	st := r.repo.Storer
	t, err := object.GetTag(st, plumbing.NewHash(sha))
	if err != nil {
		return nil, ErrObjectNotFound
	}
	objType := "commit"
	switch t.TargetType {
	case plumbing.TreeObject:
		objType = "tree"
	case plumbing.BlobObject:
		objType = "blob"
	case plumbing.TagObject:
		objType = "tag"
	}
	return &GetTagResult{
		SHA:     sha,
		Tag:     t.Name,
		Message: t.Message,
		Object:  t.Target.String(),
		Type:    objType,
		Tagger:  Signature{Name: t.Tagger.Name, Email: t.Tagger.Email, When: t.Tagger.When},
	}, nil
}

// objectMode maps a mode string to a git filemode. Defaults to regular file.
func objectMode(modeStr string, typ ObjectType) filemode.FileMode {
	switch modeStr {
	case "100755":
		return filemode.Executable
	case "040000":
		return filemode.Dir
	case "120000":
		return filemode.Symlink
	case "160000":
		return filemode.Submodule
	default:
		if typ == ObjectTree {
			return filemode.Dir
		}
		return filemode.Regular
	}
}

// modeObjectType maps a filemode to an ObjectType string.
func modeObjectType(m filemode.FileMode) ObjectType {
	switch m {
	case filemode.Dir:
		return ObjectTree
	case filemode.Submodule:
		return ObjectCommit
	default:
		return ObjectBlob
	}
}

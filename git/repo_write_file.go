package git

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	gogitstorage "github.com/go-git/go-git/v5/storage"
)

// FileWriteInput holds the parameters for creating or updating a file.
type FileWriteInput struct {
	Path          string
	Content       []byte   // nil means delete
	Message       string
	AuthorName    string
	AuthorEmail   string
	CommitterName  string
	CommitterEmail string
	When          time.Time
	Branch        string // refs/heads/main by default
	ParentSHA     string // current HEAD; required to detect races (empty skips check)
}

// FileWriteResult holds the outcome of a WriteFile or DeleteFile call.
type FileWriteResult struct {
	CommitSHA string
	BlobSHA   string // empty for delete
	TreeSHA   string
}

// WriteFile creates or updates a file in the repository by building a new tree
// and commit on top of the specified branch. If ParentSHA is set, the branch
// must currently point to exactly that SHA or the call returns ErrNotFastForward.
func (r *Repo) WriteFile(in FileWriteInput) (*FileWriteResult, error) {
	return r.applyFileChange(in, false)
}

// DeleteFile removes a file from the repository by building a new tree and
// commit on top of the specified branch.
func (r *Repo) DeleteFile(in FileWriteInput) (*FileWriteResult, error) {
	return r.applyFileChange(in, true)
}

func (r *Repo) applyFileChange(in FileWriteInput, del bool) (*FileWriteResult, error) {
	st := r.repo.Storer

	// Resolve branch ref.
	branch := in.Branch
	if branch == "" {
		branch = "refs/heads/main"
	}
	if !strings.HasPrefix(branch, "refs/") {
		branch = "refs/heads/" + branch
	}

	refName := plumbing.ReferenceName(branch)
	ref, refErr := storer.ResolveReference(st, refName)

	var parentHashes []plumbing.Hash
	var rootTree *object.Tree

	switch {
	case refErr == nil:
		// Branch exists: get its tree.
		if in.ParentSHA != "" && ref.Hash().String() != in.ParentSHA {
			return nil, ErrNotFastForward
		}
		parentHashes = []plumbing.Hash{ref.Hash()}
		parentCommit, err := object.GetCommit(st, ref.Hash())
		if err != nil {
			return nil, fmt.Errorf("git: load parent commit: %w", err)
		}
		rootTree, err = parentCommit.Tree()
		if err != nil {
			return nil, fmt.Errorf("git: load root tree: %w", err)
		}

	case errors.Is(refErr, plumbing.ErrReferenceNotFound):
		// New branch / empty repo — start from an empty tree.
		if del {
			return nil, ErrRefNotFound
		}
		rootTree = &object.Tree{}

	default:
		return nil, refErr
	}

	cleanPath := path.Clean(strings.TrimPrefix(in.Path, "/"))
	parts := strings.Split(cleanPath, "/")

	var blobHash plumbing.Hash
	if !del {
		// Write the blob.
		blobObj := st.NewEncodedObject()
		blobObj.SetType(plumbing.BlobObject)
		w, err := blobObj.Writer()
		if err != nil {
			return nil, fmt.Errorf("git: blob writer: %w", err)
		}
		if _, err = w.Write(in.Content); err != nil {
			return nil, fmt.Errorf("git: write blob: %w", err)
		}
		blobHash, err = st.SetEncodedObject(blobObj)
		if err != nil {
			return nil, fmt.Errorf("git: store blob: %w", err)
		}
	}

	// Rebuild tree bottom-up.
	newRootHash, err := r.rebuildTree(st, rootTree, parts, blobHash, del)
	if err != nil {
		return nil, err
	}

	// Build commit.
	when := in.When
	if when.IsZero() {
		when = time.Now()
	}
	authorName := in.AuthorName
	if authorName == "" {
		authorName = "Githome"
	}
	authorEmail := in.AuthorEmail
	if authorEmail == "" {
		authorEmail = "noreply@githome"
	}
	committerName := in.CommitterName
	if committerName == "" {
		committerName = authorName
	}
	committerEmail := in.CommitterEmail
	if committerEmail == "" {
		committerEmail = authorEmail
	}

	sig := object.Signature{Name: authorName, Email: authorEmail, When: when}
	committerSig := object.Signature{Name: committerName, Email: committerEmail, When: when}

	commitObj := st.NewEncodedObject()
	commitObj.SetType(plumbing.CommitObject)
	c := &object.Commit{
		Author:    sig,
		Committer: committerSig,
		Message:   in.Message,
		TreeHash:  newRootHash,
		ParentHashes: parentHashes,
	}
	if err := c.Encode(commitObj); err != nil {
		return nil, fmt.Errorf("git: encode commit: %w", err)
	}
	commitHash, err := st.SetEncodedObject(commitObj)
	if err != nil {
		return nil, fmt.Errorf("git: store commit: %w", err)
	}

	// Update branch reference (force, since we are the authoritative write path).
	newRef := plumbing.NewHashReference(refName, commitHash)
	if err := st.SetReference(newRef); err != nil {
		return nil, fmt.Errorf("git: update ref: %w", err)
	}

	res := &FileWriteResult{
		CommitSHA: commitHash.String(),
		TreeSHA:   newRootHash.String(),
	}
	if !del {
		res.BlobSHA = blobHash.String()
	}
	return res, nil
}

// rebuildTree recursively rebuilds the tree path from the root down to the
// target file, returning the new root tree hash.
func (r *Repo) rebuildTree(
	st gogitstorage.Storer,
	current *object.Tree,
	parts []string,
	blobHash plumbing.Hash,
	del bool,
) (plumbing.Hash, error) {

	name := parts[0]

	var newEntries []object.TreeEntry

	// Copy all existing entries except the one we are replacing.
	if current != nil {
		for _, e := range current.Entries {
			if e.Name != name {
				newEntries = append(newEntries, e)
			}
		}
	}

	if len(parts) == 1 {
		// Leaf: add the new blob (or omit it for delete).
		if !del {
			newEntries = append(newEntries, object.TreeEntry{
				Name: name,
				Mode: filemode.Regular,
				Hash: blobHash,
			})
		}
	} else {
		// Recurse into the subtree for this component.
		var subTree *object.Tree
		if current != nil {
			for _, e := range current.Entries {
				if e.Name == name && e.Mode == filemode.Dir {
					t, err := object.GetTree(st, e.Hash)
					if err != nil {
						return plumbing.ZeroHash, fmt.Errorf("git: load subtree %q: %w", name, err)
					}
					subTree = t
					break
				}
			}
		}
		if subTree == nil {
			subTree = &object.Tree{}
		}
		subHash, err := r.rebuildTree(st, subTree, parts[1:], blobHash, del)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		// Only add the subtree directory if it is non-empty after the change.
		if subHash != plumbing.ZeroHash {
			newEntries = append(newEntries, object.TreeEntry{
				Name: name,
				Mode: filemode.Dir,
				Hash: subHash,
			})
		}
	}

	// Write the new tree object.
	treeObj := st.NewEncodedObject()
	treeObj.SetType(plumbing.TreeObject)
	newTree := &object.Tree{Entries: newEntries}
	if err := newTree.Encode(treeObj); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: encode tree: %w", err)
	}
	hash, err := st.SetEncodedObject(treeObj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: store tree: %w", err)
	}
	return hash, nil
}

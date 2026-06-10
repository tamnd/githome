package domain

import (
	"context"
	"errors"
	"strings"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
)

// The git-ref write errors the REST layer maps to status: a forbidden write on a
// visible repository is a 403, an invalid ref name or a missing target object is
// a 422, an already-existing ref on create and a non-fast-forward update without
// force are 422 (GitHub reports both as unprocessable).
var (
	// ErrForbidden is returned when the actor may see the repository but not
	// write to it.
	ErrForbidden = errors.New("domain: write access denied")

	// ErrInvalidRef is returned for a ref name that is not a fully qualified,
	// well-formed reference.
	ErrInvalidRef = errors.New("domain: invalid reference name")

	// ErrRefExists is returned by CreateRef when the reference already exists.
	ErrRefExists = errors.New("domain: reference already exists")

	// ErrRefNotFound is returned by UpdateRef when the reference does not exist.
	ErrRefNotFound = errors.New("domain: reference not found")

	// ErrObjectMissing is returned when the target sha is not an object in the
	// repository.
	ErrObjectMissing = errors.New("domain: target object does not exist")

	// ErrNotFastForward is returned when a non-force update would not be a
	// fast-forward.
	ErrNotFastForward = errors.New("domain: update is not a fast-forward")
)

// CreateRef creates a fully qualified reference (refs/heads/x, refs/tags/x) at
// sha after authorizing write access. The repository is resolved through the
// visibility rule first, so a private repository the actor cannot see is
// ErrRepoNotFound (404) rather than ErrForbidden (403).
func (s *RepoService) CreateRef(ctx context.Context, actorPK int64, owner, name, ref, sha string) (git.Ref, error) {
	repo, err := s.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return git.Ref{}, err
	}
	if !validFullRef(ref) {
		return git.Ref{}, ErrInvalidRef
	}
	switch err := s.gitStore.CreateRef(ctx, repo.PK, ref, sha); {
	case errors.Is(err, git.ErrRefExists):
		return git.Ref{}, ErrRefExists
	case errors.Is(err, git.ErrObjectNotFound):
		return git.Ref{}, ErrObjectMissing
	case err != nil:
		return git.Ref{}, err
	}
	resolved, err := s.resolveRef(ctx, repo.PK, ref, sha)
	if err != nil {
		return git.Ref{}, err
	}
	cd := &CreateDeletePayload{
		Ref:          shortRef(ref),
		RefType:      refType(ref),
		MasterBranch: repo.DefaultBranch,
	}
	recordEventFull(ctx, s.store, s.enq, &store.EventRow{
		Event:   EventCreate,
		ActorPK: actorPK,
		RepoPK:  repo.PK,
		Public:  !repo.Private,
	}, nil, cd)
	return resolved, nil
}

// UpdateRef moves an existing reference to sha after authorizing write access.
// Unless force is set the move must be a fast-forward.
func (s *RepoService) UpdateRef(ctx context.Context, actorPK int64, owner, name, ref, sha string, force bool) (git.Ref, error) {
	repo, err := s.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return git.Ref{}, err
	}
	if !validFullRef(ref) {
		return git.Ref{}, ErrInvalidRef
	}
	switch err := s.gitStore.UpdateRef(ctx, repo.PK, ref, sha, force); {
	case errors.Is(err, git.ErrRefNotFound):
		return git.Ref{}, ErrRefNotFound
	case errors.Is(err, git.ErrObjectNotFound):
		return git.Ref{}, ErrObjectMissing
	case errors.Is(err, git.ErrNotFastForward):
		return git.Ref{}, ErrNotFastForward
	case err != nil:
		return git.Ref{}, err
	}
	return s.resolveRef(ctx, repo.PK, ref, sha)
}

// DeleteRef removes an existing reference after authorizing write access.
func (s *RepoService) DeleteRef(ctx context.Context, actorPK int64, owner, name, ref string) error {
	repo, err := s.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return err
	}
	if !validFullRef(ref) {
		return ErrInvalidRef
	}
	switch err := s.gitStore.DeleteRef(ctx, repo.PK, ref); {
	case errors.Is(err, git.ErrRefNotFound):
		return ErrRefNotFound
	case err != nil:
		return err
	}
	cd := &CreateDeletePayload{
		Ref:     shortRef(ref),
		RefType: refType(ref),
	}
	recordEventFull(ctx, s.store, s.enq, &store.EventRow{
		Event:   EventDelete,
		ActorPK: actorPK,
		RepoPK:  repo.PK,
		Public:  !repo.Private,
	}, nil, cd)
	return nil
}

// shortRef returns just the branch or tag name without the refs/heads/ or
// refs/tags/ prefix.
func shortRef(ref string) string {
	switch {
	case strings.HasPrefix(ref, "refs/heads/"):
		return strings.TrimPrefix(ref, "refs/heads/")
	case strings.HasPrefix(ref, "refs/tags/"):
		return strings.TrimPrefix(ref, "refs/tags/")
	}
	return ref
}

// refType returns "branch" for refs/heads/*, "tag" for refs/tags/*, and
// "repository" for everything else (e.g. the initial commit).
func refType(ref string) string {
	switch {
	case strings.HasPrefix(ref, "refs/heads/"):
		return "branch"
	case strings.HasPrefix(ref, "refs/tags/"):
		return "tag"
	}
	return "repository"
}

// AuthorizeWrite resolves the repository for the actor and checks write access.
// Visibility is enforced by GetRepo, so the not-found-vs-forbidden distinction
// matches GitHub: invisible -> 404 (ErrRepoNotFound), visible-but-no-write -> 403
// (ErrForbidden). The git transport calls it to gate receive-pack.
func (s *RepoService) AuthorizeWrite(ctx context.Context, actorPK int64, owner, name string) (*Repo, error) {
	repo, err := s.GetRepo(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	if !canWrite(repo, actorPK) {
		return nil, ErrForbidden
	}
	return repo, nil
}

// resolveRef builds the wire-ready ref value, reading the target's object type
// so the rendered object.type is "tag" for an annotated tag and "commit"
// otherwise.
func (s *RepoService) resolveRef(ctx context.Context, pk int64, ref, sha string) (git.Ref, error) {
	typ, err := s.gitStore.ObjectType(ctx, pk, sha)
	if err != nil {
		return git.Ref{}, err
	}
	return git.Ref{Name: ref, Target: sha, Type: git.ObjectType(typ)}, nil
}

// canWrite reports whether the actor may write to the repository. Only the owner
// may write for now; collaborator and organization roles arrive with their
// milestone.
func canWrite(repo *Repo, actorPK int64) bool {
	return actorPK != 0 && actorPK == repo.OwnerPK
}

// validFullRef reports whether ref is a fully qualified, well-formed reference.
// It enforces the subset git's check-ref-format guarantees that matters here:
// a refs/ prefix, at least three slash-separated non-empty components, and no
// traversal, spaces, or control characters.
func validFullRef(ref string) bool {
	if !strings.HasPrefix(ref, "refs/") || strings.HasSuffix(ref, "/") {
		return false
	}
	if strings.Contains(ref, "..") || strings.Contains(ref, "//") {
		return false
	}
	parts := strings.Split(ref, "/")
	if len(parts) < 3 {
		return false
	}
	for _, p := range parts {
		if p == "" || p == "." || strings.ContainsAny(p, " \t\n\\:?*[~^") {
			return false
		}
	}
	return true
}

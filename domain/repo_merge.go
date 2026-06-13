package domain

import (
	"context"
	"errors"

	"github.com/tamnd/githome/git"
)

var (
	// ErrMergeConflict is returned when a branch merge cannot apply cleanly. The
	// REST layer maps it to 409 Conflict.
	ErrMergeConflict = errors.New("domain: merge conflict")
	// ErrNothingToMerge is returned when the base branch already contains the
	// head, so the merge is a no-op. The REST layer maps it to 204 No Content.
	ErrNothingToMerge = errors.New("domain: nothing to merge")
	// ErrMergeMissing is returned when the base or head a branch merge names
	// cannot be resolved to a commit. The REST layer maps it to 404 Not Found.
	ErrMergeMissing = errors.New("domain: merge base or head not found")
)

// MergeBranch performs a server-side branch merge, the body of
// POST /repos/{owner}/{repo}/merges (PyGitHub repo.merge, release scripts). It
// merges head into the base branch and advances the base ref to the new merge
// commit. base must name a branch; head may be a branch name or any commit-ish
// (sha, tag). A base that already contains head is ErrNothingToMerge (204), an
// unresolvable base or head is ErrMergeMissing (404), and a merge that does not
// apply cleanly is ErrMergeConflict (409). On success it returns the repository
// and the new merge commit so the caller can render the commit object.
func (s *RepoService) MergeBranch(ctx context.Context, actorPK int64, owner, name, base, head, commitMessage string) (*Repo, git.Commit, error) {
	repo, err := s.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, git.Commit{}, err
	}

	baseTip, err := s.gitStore.RefSHA(ctx, repo.PK, "refs/heads/"+base)
	if err != nil {
		return nil, git.Commit{}, ErrMergeMissing
	}
	headCommit, err := s.GetCommit(repo, head)
	if err != nil {
		return nil, git.Commit{}, ErrMergeMissing
	}
	headSHA := headCommit.SHA

	ahead, _, err := s.gitStore.AheadBehind(ctx, repo.PK, baseTip, headSHA)
	if err != nil {
		return nil, git.Commit{}, err
	}
	if ahead == 0 {
		return nil, git.Commit{}, ErrNothingToMerge
	}

	row, err := s.store.UserByPK(ctx, actorPK)
	if err != nil {
		return nil, git.Commit{}, err
	}
	who := prSignature(userFromRow(row))
	message := commitMessage
	if message == "" {
		message = "Merge " + head + " into " + base
	}
	sha, ok, err := s.gitStore.Merge(ctx, repo.PK, git.MergeCommit, baseTip, headSHA, message, who, who)
	if err != nil {
		return nil, git.Commit{}, err
	}
	if !ok {
		return nil, git.Commit{}, ErrMergeConflict
	}
	if err := s.gitStore.UpdateRef(ctx, repo.PK, "refs/heads/"+base, sha, true); err != nil {
		return nil, git.Commit{}, err
	}

	// Fan the merge out as a push to the base branch so the activity feed and a
	// repository's webhooks observe it the same as any other update to that
	// branch. Delivery is best-effort, exactly as a real push's is.
	_ = s.OnPush(ctx, PushBatch{
		RepoPK:   repo.PK,
		PusherPK: actorPK,
		Protocol: "api",
		Updates:  []RefUpdate{{Ref: "refs/heads/" + base, OldSHA: baseTip, NewSHA: sha}},
	})

	c, err := s.GetCommit(repo, sha)
	if err != nil {
		return nil, git.Commit{}, err
	}
	return repo, c, nil
}

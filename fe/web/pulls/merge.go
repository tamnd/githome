package pulls

import (
	"errors"
	"fmt"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/git"
)

// merge.go builds the merge box and runs the merge mutation. The box is derived
// once from the cached mergeability columns the worker fills, so the page and a
// concurrent API read agree, and it re-fetches itself as a standalone fragment
// while the worker is still computing. The merge form carries the head sha as an
// optimistic-concurrency token so a merge of a head that moved out from under the
// viewer is rejected, never silently merging the wrong tip. The review-required
// and check-required gates that produce the blocked and unstable states arrive in
// F5 and F9; F4 wires the clean, behind, dirty, draft, computing, merged, and
// closed states the live worker already produces. See implementation/09 section 5.

// mergeBox derives the merge box for a pull request. It reads the cached
// mergeable_state the worker resolved rather than doing any git work here, so the
// box and a concurrent API read agree, and it sets the merge affordances only for
// a viewer who can write, since the service authorizes the merge again on submit.
func (h *Handlers) mergeBox(c *mizu.Ctx, repo *domain.Repo, pr *domain.PullRequest, vc viewerCtx) view.MergeBoxVM {
	owner := ownerLogin(repo)
	state := view.DeriveMergeBoxState(pr.Merged, pr.State, pr.MergeableState)

	box := view.MergeBoxVM{
		State:              state,
		HeadSHA:            pr.Head.SHA,
		ViewerCanMerge:     canWrite(repo, vc.pk) && state == view.MergeClean,
		HeadRefExists:      pr.Head.SHA != "",
		MergeURL:           route.PullMerge(owner, repo.Name, pr.Number),
		PollURL:            route.PullMergeBox(owner, repo.Name, pr.Number),
		DefaultCommitTitle: defaultMergeTitle(pr),
		CSRFToken:          view.CSRFFrom(c.Context()),
		Methods:            mergeMethods(),
		PrimaryMethod:      string(git.MergeCommit),
	}
	if state == view.MergeBehind {
		// Behind is mergeable: the worker reports the head trails the base but does
		// not conflict, so the green control still merges (a merge commit subsumes
		// the catch-up). The view note tells the viewer the branch is behind.
		box.ViewerCanMerge = canWrite(repo, vc.pk)
	}
	if pr.State == "closed" && !pr.Merged {
		box.CanReopen = canWrite(repo, vc.pk)
	}
	return box
}

// mergeMethods is the three git merge strategies the box offers, merge commit
// first as the default. The per-repo allow-list that hides some of them is a
// repository-settings concern (F8); F4 offers all three.
func mergeMethods() []view.MergeMethodVM {
	return []view.MergeMethodVM{
		{Method: string(git.MergeCommit), Label: "Create a merge commit", IsDefault: true},
		{Method: string(git.MergeSquash), Label: "Squash and merge"},
		{Method: string(git.MergeRebase), Label: "Rebase and merge"},
	}
}

// defaultMergeTitle is the merge commit subject the box pre-fills, matching the
// "Merge pull request #N from head" line git forges write by default.
func defaultMergeTitle(pr *domain.PullRequest) string {
	head := pr.Head.Ref
	if pr.Head.Label != "" {
		head = pr.Head.Label
	}
	return fmt.Sprintf("Merge pull request #%d from %s", pr.Number, head)
}

// MergeBox renders the merge box on its own, the fragment the Computing state
// re-fetches until the worker resolves the mergeability. It is the same model the
// Conversation tab embeds, rendered through the standalone partial so the poll
// swaps just the box. A missing PR is the soft 404.
func (h *Handlers) MergeBox(c *mizu.Ctx) error {
	repo, ok := repoFromContext(c.Context())
	if !ok {
		return h.notFound(c)
	}
	pr, ok := h.loadPR(c, repo)
	if !ok {
		return nil
	}
	box := h.mergeBox(c, repo, pr, h.viewer(c))
	return h.render.Fragment(c, "pulls/merge_box", box)
}

// Merge performs the merge and redirects back to the Conversation tab. The form
// carries the strategy and the expected head sha; the service re-authorizes the
// write, re-checks mergeability, and rejects a stale head, so this handler maps
// the outcome rather than deciding it. A not-mergeable or head-moved result
// re-renders the Conversation with the reason inline (the no-JS error path), since
// those are expected races, not server faults. See implementation/09 section 5.3.
func (h *Handlers) Merge(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	owner := ownerLogin(repo)
	number, ok := numberParam(c.Param("number"))
	if !ok {
		return h.notFound(c)
	}
	vc := h.viewer(c)

	in := domain.MergeInput{
		Method:        git.MergeMethod(formString(c, "method")),
		CommitTitle:   formString(c, "commit_title"),
		CommitMessage: formRaw(c, "commit_message"),
		ExpectedHead:  formString(c, "head_sha"),
	}
	_, err := h.pulls.Merge(ctx, vc.pk, owner, repo.Name, number, in)
	switch {
	case err == nil:
		return redirect(c, route.Pull(owner, repo.Name, number))
	case errors.Is(err, domain.ErrHeadMismatch):
		return h.conversationError(c, repo, number, vc, "The head moved since this page loaded. Reload and try again.")
	case errors.Is(err, domain.ErrNotMergeable):
		return h.conversationError(c, repo, number, vc, "This pull request can no longer be merged.")
	case errors.Is(err, domain.ErrInvalidMergeMethod):
		return h.conversationError(c, repo, number, vc, "That merge method is not available.")
	default:
		return h.writeError(c, err)
	}
}

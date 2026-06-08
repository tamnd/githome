package pulls

import (
	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
)

// composer.go holds the Conversation-tab mutations a PR shares with an issue: a
// new timeline comment and the close/reopen toggle. A PR is an issue under the
// hood, so both go through the issue service on the PR's number, and both land
// back on the Conversation tab with a 303 so the no-JS flow re-fetches a clean
// GET. The merge mutation lives in merge.go; these are the lifecycle writes that
// are not a merge. See implementation/09 section 3.

// CreateComment posts a comment to the PR's shared issue thread and redirects to
// the new comment's permalink. An empty body re-renders the Conversation with the
// message inline, the no-JS validation path.
func (h *Handlers) CreateComment(c *mizu.Ctx) error {
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

	body := formRaw(c, "body")
	if isBlank(body) {
		return h.conversationError(c, repo, number, vc, "A comment needs a body.")
	}
	cm, err := h.issues.CreateComment(ctx, vc.pk, owner, repo.Name, number, body)
	if isValidation(err) {
		return h.conversationError(c, repo, number, vc, "That comment could not be posted.")
	}
	if err != nil {
		return h.writeError(c, err)
	}
	return redirect(c, route.PullComment(owner, repo.Name, number, cm.ID))
}

// ToggleState closes or reopens the pull request, optionally posting a comment in
// the same submit (the "Close with comment" path). The target state is derived
// from the PR's current state, so the one button does the right thing either way.
// Closing a PR is closing its issue; it does not merge. A merged PR is already
// closed, so the composer hides this control once merged.
func (h *Handlers) ToggleState(c *mizu.Ctx) error {
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

	iss, err := h.issues.GetIssue(ctx, vc.pk, owner, repo.Name, number)
	if isNotFound(err) {
		return h.notFound(c)
	}
	if err != nil {
		return h.writeError(c, err)
	}

	// A comment typed into the composer rides along: post it first so it lands
	// before the state-change event in the timeline order, matching the issues
	// surface.
	if body := formRaw(c, "body"); !isBlank(body) {
		if _, err := h.issues.CreateComment(ctx, vc.pk, owner, repo.Name, number, body); err != nil && !isValidation(err) {
			return h.writeError(c, err)
		}
	}

	patch := domain.IssuePatch{}
	if iss.State == "open" {
		closed := "closed"
		reason := "not_planned"
		patch.State = &closed
		patch.StateReason = &reason
	} else {
		open := "open"
		reason := "reopened"
		patch.State = &open
		patch.StateReason = &reason
	}
	if _, err := h.issues.EditIssue(ctx, vc.pk, owner, repo.Name, number, patch); err != nil {
		return h.writeError(c, err)
	}
	return redirect(c, route.Pull(owner, repo.Name, number))
}

// conversationError re-renders the Conversation tab with a validation message
// echoed into the composer, re-reading the PR so the rest of the page reflects its
// current state. A read failure on the re-render falls back to the soft 404.
func (h *Handlers) conversationError(c *mizu.Ctx, repo *domain.Repo, number int64, vc viewerCtx, msg string) error {
	ctx := c.Context()
	owner := ownerLogin(repo)
	pr, err := h.pulls.GetPR(ctx, vc.pk, owner, repo.Name, number)
	if isNotFound(err) {
		return h.notFound(c)
	}
	if err != nil {
		return h.writeError(c, err)
	}
	vm, err := h.conversation(ctx, c, repo, pr, vc, msg)
	if err != nil {
		return h.render.ServerError(c, err)
	}
	return h.render.Page(c, "pulls/conversation", vm)
}

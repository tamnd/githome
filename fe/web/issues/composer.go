package issues

import (
	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// composer.go holds the timeline mutations: adding a comment, toggling the issue
// state (with an optional comment in the same submit), editing the title, and
// editing or deleting a comment. Each has a plain HTML form path and redirects to
// a clean GET on success, so the flow works with no JavaScript. See
// implementation/08 sections 6 and 8.

// CreateComment appends a comment and redirects to its permalink. An empty body
// re-renders the issue with the message inline rather than posting a blank
// comment.
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
		return h.showWithError(c, repo, number, vc, "A comment needs a body.")
	}
	cm, err := h.issues.CreateComment(ctx, vc.pk, owner, repo.Name, number, body)
	if isValidation(err) {
		return h.showWithError(c, repo, number, vc, "That comment could not be posted.")
	}
	if err != nil {
		return h.writeError(c, err)
	}
	return redirect(c, route.IssueComment(owner, repo.Name, number, cm.ID))
}

// ToggleState closes or reopens the issue, optionally posting a comment in the
// same submit (the "Close with comment" path). The target state is derived from
// the issue's current state, so the one button does the right thing either way.
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
	// before the state-change event in the timeline order, matching github.com.
	if body := formRaw(c, "body"); !isBlank(body) {
		if _, err := h.issues.CreateComment(ctx, vc.pk, owner, repo.Name, number, body); err != nil && !isValidation(err) {
			return h.writeError(c, err)
		}
	}

	patch := domain.IssuePatch{}
	if iss.State == "open" {
		closed := "closed"
		reason := "completed"
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
	return redirect(c, route.Issue(owner, repo.Name, number))
}

// EditTitle renames the issue. An empty title re-renders the issue with the
// message inline.
func (h *Handlers) EditTitle(c *mizu.Ctx) error {
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

	title := formString(c, "title")
	if title == "" {
		return h.showWithError(c, repo, number, vc, "An issue needs a title.")
	}
	if _, err := h.issues.EditIssue(ctx, vc.pk, owner, repo.Name, number, domain.IssuePatch{Title: &title}); err != nil {
		if isValidation(err) {
			return h.showWithError(c, repo, number, vc, "That title could not be saved.")
		}
		return h.writeError(c, err)
	}
	return redirect(c, route.Issue(owner, repo.Name, number))
}

// EditComment updates a comment body and redirects to its permalink.
func (h *Handlers) EditComment(c *mizu.Ctx) error {
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
	commentID, ok := numberParam(c.Param("comment"))
	if !ok {
		return h.notFound(c)
	}
	vc := h.viewer(c)

	body := formRaw(c, "body")
	if isBlank(body) {
		return h.showWithError(c, repo, number, vc, "A comment needs a body.")
	}
	if _, err := h.issues.EditComment(ctx, vc.pk, owner, repo.Name, commentID, body); err != nil {
		if isValidation(err) {
			return h.showWithError(c, repo, number, vc, "That comment could not be saved.")
		}
		return h.writeError(c, err)
	}
	return redirect(c, route.IssueComment(owner, repo.Name, number, commentID))
}

// DeleteComment removes a comment and redirects to the issue.
func (h *Handlers) DeleteComment(c *mizu.Ctx) error {
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
	commentID, ok := numberParam(c.Param("comment"))
	if !ok {
		return h.notFound(c)
	}
	vc := h.viewer(c)

	if err := h.issues.DeleteComment(ctx, vc.pk, owner, repo.Name, commentID); err != nil {
		return h.writeError(c, err)
	}
	return redirect(c, route.Issue(owner, repo.Name, number))
}

// showWithError reloads the issue and its comments and re-renders the detail page
// with a form-error message, the no-JS error path for a failed mutation. A reload
// failure (the issue vanished under the error) falls back to the soft 404.
func (h *Handlers) showWithError(c *mizu.Ctx, repo *domain.Repo, number int64, vc viewerCtx, msg string) error {
	ctx := c.Context()
	owner := ownerLogin(repo)
	iss, err := h.issues.GetIssue(ctx, vc.pk, owner, repo.Name, number)
	if err != nil {
		return h.writeError(c, err)
	}
	comments, err := h.issues.ListComments(ctx, vc.pk, owner, repo.Name, number, 1, showPerPage)
	if err != nil {
		return err
	}
	// The error re-render lands on the first page of the thread, so it carries an
	// empty pager (a failed comment is rare and the composer is what matters here).
	vm := h.detail(ctx, c, repo, iss, comments, vc, msg, view.Pager{Page: 1})
	return h.render.Page(c, "issues/show", vm)
}

// isBlank reports whether s is empty or only whitespace.
func isBlank(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}

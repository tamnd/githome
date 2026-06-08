package issues

import (
	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// Create opens an issue from the new-issue form and redirects to its detail page.
// A missing title re-renders the form with the values preserved and the message
// inline (the no-JS validation path), so a typo never loses the draft. The
// service authorizes write access; a viewer without it gets the themed 403. See
// implementation/08 section 10.
func (h *Handlers) Create(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	owner := ownerLogin(repo)
	vc := h.viewer(c)

	title := formString(c, "title")
	body := formRaw(c, "body")

	if title == "" {
		return h.renderNewError(c, repo, vc, title, body, "An issue needs a title.")
	}

	in := domain.IssueInput{Title: title}
	if body != "" {
		in.Body = &body
	}
	iss, err := h.issues.CreateIssue(ctx, vc.pk, owner, repo.Name, in)
	if isValidation(err) {
		return h.renderNewError(c, repo, vc, title, body, "That issue could not be created. Check the title and try again.")
	}
	if err != nil {
		return h.writeError(c, err)
	}
	return redirect(c, route.Issue(owner, repo.Name, iss.Number))
}

// renderNewError re-renders the new-issue form with the submitted values and a
// message, returning a 422 so the failure is visible to a client that checks the
// status while the page still reads cleanly to a human.
func (h *Handlers) renderNewError(c *mizu.Ctx, repo *domain.Repo, vc viewerCtx, title, body, msg string) error {
	owner := ownerLogin(repo)
	vm := view.NewIssueVM{
		Chrome:    h.chrome(c, "New issue"),
		Header:    h.header(repo),
		Nav:       h.nav(repo),
		Repo:      repoRef(repo),
		Action:    route.Issues(owner, repo.Name, ""),
		Title:     title,
		Body:      body,
		CanSubmit: canComment(vc.pk) && canWrite(repo, vc.pk),
		FormError: msg,
	}
	return h.render.Page(c, "issues/new", vm)
}

package issues

import (
	"errors"
	"strconv"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// newIssueForm is the submitted new-issue form: the title and body the viewer
// typed plus the metadata the prefill carried through hidden fields. It rides
// into the error re-render so a validation miss never loses any of it.
type newIssueForm struct {
	title     string
	body      string
	labels    []string
	assignees []string
	milestone int64 // 0 = none
}

// readNewIssueForm parses the create POST. The metadata fields repeat (one
// hidden input per item), so they read as multi-values.
func readNewIssueForm(c *mizu.Ctx) newIssueForm {
	f := newIssueForm{
		title: formString(c, "title"),
		body:  formRaw(c, "body"),
	}
	form, err := c.Form()
	if err != nil {
		return f
	}
	f.labels = cleanFormList(form["labels"])
	f.assignees = cleanFormList(form["assignees"])
	if n, err := strconv.ParseInt(strings.TrimSpace(form.Get("milestone")), 10, 64); err == nil && n > 0 {
		f.milestone = n
	}
	return f
}

// cleanFormList trims a repeated form value and drops empties.
func cleanFormList(vs []string) []string {
	var out []string
	for _, v := range vs {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Create opens an issue from the new-issue form and redirects to its detail page.
// A missing title re-renders the form with the values preserved and the message
// inline (the no-JS validation path), so a typo never loses the draft. The labels,
// assignees, and milestone the prefill carried apply through the same service
// input a REST create uses. The service authorizes write access; a viewer without
// it gets the themed 403. See implementation/08 section 10.
func (h *Handlers) Create(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	owner := ownerLogin(repo)
	vc := h.viewer(c)

	f := readNewIssueForm(c)
	if f.title == "" {
		return h.renderNewError(c, repo, vc, f, "An issue needs a title.")
	}

	in := domain.IssueInput{
		Title:          f.title,
		Labels:         f.labels,
		AssigneeLogins: f.assignees,
	}
	if f.body != "" {
		in.Body = &f.body
	}
	if f.milestone > 0 {
		in.MilestoneNumber = &f.milestone
	}
	iss, err := h.issues.CreateIssue(ctx, vc.pk, owner, repo.Name, in)
	if errors.Is(err, domain.ErrMilestoneNotFound) {
		// A stale prefill can point at a deleted milestone; drop it and let the
		// viewer resubmit rather than failing the whole create.
		f.milestone = 0
		return h.renderNewError(c, repo, vc, f, "That milestone does not exist; it was removed from the form.")
	}
	if isValidation(err) {
		return h.renderNewError(c, repo, vc, f, "That issue could not be created. Check the title and try again.")
	}
	if err != nil {
		return h.writeError(c, err)
	}
	return redirect(c, route.Issue(owner, repo.Name, iss.Number))
}

// renderNewError re-renders the new-issue form with the submitted values and a
// message, returning a 422 so the failure is visible to a client that checks the
// status while the page still reads cleanly to a human.
func (h *Handlers) renderNewError(c *mizu.Ctx, repo *domain.Repo, vc viewerCtx, f newIssueForm, msg string) error {
	owner := ownerLogin(repo)
	vm := view.NewIssueVM{
		Chrome:    h.chrome(c, "New issue"),
		Header:    h.header(repo),
		Nav:       h.nav(repo),
		Repo:      repoRef(repo),
		Action:    route.Issues(owner, repo.Name, ""),
		Title:     f.title,
		Body:      f.body,
		Labels:    f.labels,
		Assignees: f.assignees,
		CanSubmit: canComment(vc.pk) && canWrite(repo, vc.pk),
		FormError: msg,
	}
	if f.milestone > 0 {
		vm.Milestone = strconv.FormatInt(f.milestone, 10)
	}
	return h.render.Page(c, "issues/new", vm)
}

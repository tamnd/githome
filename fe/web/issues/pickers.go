package issues

import (
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
)

// pickers.go holds the sidebar metadata edits. The real service exposes labels
// and assignees through one EditIssue patch, so the sidebar form posts the fields
// it changes and the handler folds them into a single patch. A field absent from
// the form is left unchanged; an empty labels field clears the labels (the form
// always submits the field, so its emptiness is meaningful). See implementation/08
// section 9.

// EditSidebar applies the label and assignee edits from the sidebar form. The
// service authorizes write access and resolves unknown labels or assignees the
// same way it does on create, so the handler only parses the comma lists and
// redirects back to the issue.
func (h *Handlers) EditSidebar(c *mizu.Ctx) error {
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

	form, err := c.Form()
	if err != nil {
		return h.showWithError(c, repo, number, vc, "That edit could not be read.")
	}

	patch := domain.IssuePatch{}
	// A field is only patched when the form carries it, so the same handler serves
	// the labels-only form and a future assignees-only form without clobbering the
	// field the other form owns.
	if form.Has("labels") {
		labels := splitCommaList(form.Get("labels"))
		patch.Labels = &labels
	}
	if form.Has("assignees") {
		assignees := splitCommaList(form.Get("assignees"))
		patch.AssigneeLogins = &assignees
	}

	if _, err := h.issues.EditIssue(ctx, vc.pk, owner, repo.Name, number, patch); err != nil {
		if isValidation(err) {
			return h.showWithError(c, repo, number, vc, "That edit could not be saved.")
		}
		return h.writeError(c, err)
	}
	return redirect(c, route.Issue(owner, repo.Name, number))
}

// splitCommaList parses a comma-separated input into trimmed, non-empty entries,
// preserving order and dropping duplicates. An empty input yields an empty (not
// nil) slice, so the caller can pass it to a patch that clears the field.
func splitCommaList(raw string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

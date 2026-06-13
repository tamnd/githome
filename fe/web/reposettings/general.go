package reposettings

// general.go holds the repository General settings section, the page
// /{owner}/{repo}/settings opens on. The main form renames the repository,
// edits its description, and changes its default branch; the danger zone flips
// it between public and private and deletes it. Every write goes through the
// domain RepoService the REST surface uses, so a change the page makes is the
// same change the API would, and the owner-only authority the Resolve gate
// already checked is re-checked by the service on every call. Each mutation
// posts and redirects so the no-JS flow lands on a clean GET behind the CSRF
// guard. The sections Githome does not yet back (collaborators, branch
// protection, deploy keys) get no form, the same honest absence the rest of the
// settings tree took. See 2005/review/01 R01-51.

import (
	"errors"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

// General renders the repository's General settings: the rename, description,
// and default-branch form, and the danger zone. It is what the bare settings
// root now serves instead of bouncing straight to the webhooks.
func (h *Handlers) General(c *mizu.Ctx) error {
	ctx := c.Context()
	owner, name := h.owner(c), h.name(c)
	repo, err := h.repos.GetRepo(ctx, webmw.ViewerID(ctx), owner, name)
	if err != nil {
		return h.serviceError(c, err)
	}
	return h.render.Page(c, "settings/general", h.generalVM(c, repo, ""))
}

// UpdateGeneral applies the main form: a rename, a description change, and a
// default-branch change, in one post. A rejected name re-renders the filled
// form with an inline error; a clean save redirects to the settings root, which
// is the (possibly renamed) repository's General page.
func (h *Handlers) UpdateGeneral(c *mizu.Ctx) error {
	ctx := c.Context()
	owner, name := h.owner(c), h.name(c)
	viewerPK := webmw.ViewerID(ctx)

	form, err := c.Form()
	if err != nil {
		repo, gerr := h.repos.GetRepo(ctx, viewerPK, owner, name)
		if gerr != nil {
			return h.serviceError(c, gerr)
		}
		return h.render.Page(c, "settings/general", h.generalVM(c, repo, "Could not read the form. Please try again."))
	}

	newName := strings.TrimSpace(form.Get("name"))
	description := strings.TrimSpace(form.Get("description"))
	defaultBranch := strings.TrimSpace(form.Get("default_branch"))
	if newName == "" {
		repo, gerr := h.repos.GetRepo(ctx, viewerPK, owner, name)
		if gerr != nil {
			return h.serviceError(c, gerr)
		}
		return h.render.Page(c, "settings/general", h.generalVM(c, repo, "Repository name cannot be empty."))
	}

	patch := domain.RepoPatch{Name: &newName, Description: &description}
	if defaultBranch != "" {
		patch.DefaultBranch = &defaultBranch
	}
	updated, err := h.repos.UpdateRepo(ctx, viewerPK, owner, name, patch)
	if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrRepoNotFound) {
		return h.notFound(c)
	}
	if err != nil {
		repo, gerr := h.repos.GetRepo(ctx, viewerPK, owner, name)
		if gerr != nil {
			return h.serviceError(c, gerr)
		}
		h.log.Error("reposettings general: update", "err", err)
		return h.render.Page(c, "settings/general", h.generalVM(c, repo, "Could not save the changes. The name may already be taken."))
	}
	h.flash.Add(c, "success", "Repository settings saved.")
	return h.redirect(c, route.RepoSettings(repoOwnerLogin(updated), updated.Name))
}

// UpdateVisibility flips the repository between public and private from the
// danger zone. The form carries the target visibility, so a double-submit is
// idempotent rather than a toggle that races.
func (h *Handlers) UpdateVisibility(c *mizu.Ctx) error {
	ctx := c.Context()
	owner, name := h.owner(c), h.name(c)
	viewerPK := webmw.ViewerID(ctx)

	form, err := c.Form()
	if err != nil {
		return h.serviceError(c, err)
	}
	private := form.Get("visibility") == "private"
	if _, err := h.repos.UpdateRepo(ctx, viewerPK, owner, name, domain.RepoPatch{Private: &private}); err != nil {
		if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrRepoNotFound) {
			return h.notFound(c)
		}
		h.log.Error("reposettings general: visibility", "err", err)
		return err
	}
	if private {
		h.flash.Add(c, "success", "Repository is now private.")
	} else {
		h.flash.Add(c, "success", "Repository is now public.")
	}
	return h.redirect(c, route.RepoSettings(owner, name))
}

// Delete removes the repository from the danger zone. It confirms the full name
// was typed back before deleting, the same guard github.com puts in front of an
// irreversible delete; a mismatch re-renders the General page with an inline
// error rather than touching the repository. A clean delete lands on the
// owner's profile, since the repository the viewer was on is gone.
func (h *Handlers) Delete(c *mizu.Ctx) error {
	ctx := c.Context()
	owner, name := h.owner(c), h.name(c)
	viewerPK := webmw.ViewerID(ctx)

	form, err := c.Form()
	if err != nil {
		return h.serviceError(c, err)
	}
	confirm := strings.TrimSpace(form.Get("confirm"))
	if !strings.EqualFold(confirm, owner+"/"+name) && !strings.EqualFold(confirm, name) {
		repo, gerr := h.repos.GetRepo(ctx, viewerPK, owner, name)
		if gerr != nil {
			return h.serviceError(c, gerr)
		}
		return h.render.Page(c, "settings/general", h.generalVM(c, repo, "The confirmation did not match the repository name."))
	}
	if err := h.repos.DeleteRepo(ctx, viewerPK, owner, name); err != nil {
		if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrRepoNotFound) {
			return h.notFound(c)
		}
		h.log.Error("reposettings general: delete", "err", err)
		return err
	}
	h.flash.Add(c, "success", "Repository "+owner+"/"+name+" was deleted.")
	return h.redirect(c, route.Profile(owner))
}

// generalVM builds the General page model from the resolved repository. The
// default-branch select offers the repository's branches with the current head
// marked; an empty repository (no commits, so no branches) drives the
// branchless state the template renders as a disabled field.
func (h *Handlers) generalVM(c *mizu.Ctx, repo *domain.Repo, formErr string) view.RepoGeneralVM {
	owner, name := h.owner(c), h.name(c)
	description := ""
	if repo.Description != nil {
		description = *repo.Description
	}
	branches := h.branchOptions(repo)
	return view.RepoGeneralVM{
		Chrome:           h.view.Chrome(c, "Settings"),
		Nav:              h.nav(c, route.RepoSettings(owner, name)),
		RepoFullName:     owner + "/" + name,
		Action:           route.RepoSettings(owner, name),
		Name:             repo.Name,
		Description:      description,
		Branches:         branches,
		HasBranches:      len(branches) > 0,
		Private:          repo.Private,
		VisibilityAction: route.RepoSettingsVisibility(owner, name),
		DeleteAction:     route.RepoSettingsDelete(owner, name),
		FormError:        formErr,
	}
}

// branchOptions lists the repository's branches for the default-branch select,
// the current default marked selected. An empty repository has no branches, so
// the select renders disabled; the listing failing is treated the same, since
// the rest of the page must still render.
func (h *Handlers) branchOptions(repo *domain.Repo) []view.AppearanceOption {
	branches, err := h.repos.ListBranches(repo)
	if err != nil || len(branches) == 0 {
		return nil
	}
	out := make([]view.AppearanceOption, 0, len(branches))
	for _, b := range branches {
		out = append(out, view.AppearanceOption{
			Value:    b.Name,
			Label:    b.Name,
			Selected: b.Name == repo.DefaultBranch,
		})
	}
	return out
}

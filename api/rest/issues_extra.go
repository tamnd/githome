package rest

import (
	"errors"
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
)

// handleIssueLabelsList serves GET /repos/{owner}/{repo}/issues/{number}/labels.
func handleIssueLabelsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		issue, err := d.Issues.GetIssue(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), number)
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrRepoNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, labelsJSON(issue.Labels, d, c.Param("owner"), c.Param("repo")))
		return nil
	}
}

// handleIssueLabelsAdd serves POST /repos/{owner}/{repo}/issues/{number}/labels.
func handleIssueLabelsAdd(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body struct {
			Labels []string `json:"labels"`
		}
		if !decodeJSON(c, &body) {
			return nil
		}
		issue, err := d.Issues.AddLabels(ctx, actor.UserID, c.Param("owner"), c.Param("repo"), number, body.Labels)
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrRepoNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, labelsJSON(issue.Labels, d, c.Param("owner"), c.Param("repo")))
		return nil
	}
}

// handleIssueLabelsReplace serves PUT /repos/{owner}/{repo}/issues/{number}/labels.
func handleIssueLabelsReplace(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body struct {
			Labels []string `json:"labels"`
		}
		if !decodeJSON(c, &body) {
			return nil
		}
		issue, err := d.Issues.EditIssue(ctx, actor.UserID, c.Param("owner"), c.Param("repo"), number, domain.IssuePatch{
			Labels: &body.Labels,
		})
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrRepoNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, labelsJSON(issue.Labels, d, c.Param("owner"), c.Param("repo")))
		return nil
	}
}

// handleIssueLabelRemove serves DELETE /repos/{owner}/{repo}/issues/{number}/labels/{name}.
func handleIssueLabelRemove(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		name := c.Param("name")
		issue, err := d.Issues.RemoveLabels(ctx, actor.UserID, c.Param("owner"), c.Param("repo"), number, []string{name})
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrRepoNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, labelsJSON(issue.Labels, d, c.Param("owner"), c.Param("repo")))
		return nil
	}
}

// handleIssueLabelsRemoveAll serves DELETE /repos/{owner}/{repo}/issues/{number}/labels.
func handleIssueLabelsRemoveAll(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		empty := []string{}
		_, err := d.Issues.EditIssue(ctx, actor.UserID, c.Param("owner"), c.Param("repo"), number, domain.IssuePatch{
			Labels: &empty,
		})
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrRepoNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleAssigneesList serves GET /repos/{owner}/{repo}/assignees.
// Returns a list of users who can be assigned to issues in the repo
// (the owner plus collaborators).
func handleAssigneesList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		owner, repoName := c.Param("owner"), c.Param("repo")
		actor := auth.ActorFrom(c.Request().Context())
		repo, err := d.Repos.GetRepo(c.Request().Context(), actor.UserID, owner, repoName)
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrRepoNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		// Return just the owner as a minimal assignable user list.
		writeJSON(c.Writer(), http.StatusOK, []any{
			d.URLs.SimpleUser(repo.Owner, d.NodeFormat),
		})
		return nil
	}
}

// handleIssueAssigneesAdd serves POST /repos/{owner}/{repo}/issues/{number}/assignees.
func handleIssueAssigneesAdd(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body struct {
			Assignees []string `json:"assignees"`
		}
		if !decodeJSON(c, &body) {
			return nil
		}
		issue, err := d.Issues.AddAssignees(ctx, actor.UserID, c.Param("owner"), c.Param("repo"), number, body.Assignees)
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrRepoNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.Issue(c.Param("owner"), c.Param("repo"), issue, d.NodeFormat))
		return nil
	}
}

// handleIssueAssigneesRemove serves DELETE /repos/{owner}/{repo}/issues/{number}/assignees.
func handleIssueAssigneesRemove(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body struct {
			Assignees []string `json:"assignees"`
		}
		if !decodeJSON(c, &body) {
			return nil
		}
		issue, err := d.Issues.RemoveAssignees(ctx, actor.UserID, c.Param("owner"), c.Param("repo"), number, body.Assignees)
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrRepoNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.Issue(c.Param("owner"), c.Param("repo"), issue, d.NodeFormat))
		return nil
	}
}

// labelsJSON converts a slice of domain labels to the REST JSON shape.
func labelsJSON(labels []*domain.Label, d Deps, owner, repoName string) []any {
	out := make([]any, 0, len(labels))
	for _, l := range labels {
		out = append(out, d.URLs.Label(owner, repoName, l, d.NodeFormat))
	}
	return out
}

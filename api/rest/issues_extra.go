package rest

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
)

// decodeLabelsBody reads the labels add/replace request, which GitHub accepts
// in two shapes: an object {"labels": [...]} or a bare JSON array. Members of
// either array may be plain strings or {"name": ...} objects.
func decodeLabelsBody(c *mizu.Ctx) ([]string, bool) {
	var raw json.RawMessage
	if !decodeJSON(c, &raw) {
		return nil, false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, true
	}
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
		var list labelList
		if err := json.Unmarshal(raw, &list); err != nil {
			writeError(c.Writer(), &apiError{Status: http.StatusBadRequest, Message: "Problems parsing JSON", DocURL: docRoot})
			return nil, false
		}
		return list, true
	}
	var body struct {
		Labels labelList `json:"labels"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		writeError(c.Writer(), &apiError{Status: http.StatusBadRequest, Message: "Problems parsing JSON", DocURL: docRoot})
		return nil, false
	}
	return body.Labels, true
}

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
		labels, ok := decodeLabelsBody(c)
		if !ok {
			return nil
		}
		issue, err := d.Issues.AddLabels(ctx, actor.UserID, c.Param("owner"), c.Param("repo"), number, labels)
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

// validLockReason reports whether a lock reason is one of GitHub's fixed set.
func validLockReason(r string) bool {
	switch r {
	case "off-topic", "too heated", "resolved", "spam":
		return true
	default:
		return false
	}
}

// handleIssueLock serves PUT /repos/{owner}/{repo}/issues/{number}/lock. The
// body is optional; when present, lock_reason must be one of GitHub's four
// values. A successful lock is a bare 204.
func handleIssueLock(d Deps) mizu.Handler {
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
			LockReason *string `json:"lock_reason"`
		}
		if !decodeJSON(c, &body) {
			return nil
		}
		if body.LockReason != nil && !validLockReason(*body.LockReason) {
			writeError(c.Writer(), errValidation(FieldError{Resource: "Issue", Field: "lock_reason", Code: "invalid"}))
			return nil
		}
		err := d.Issues.SetLocked(ctx, actor.UserID, c.Param("owner"), c.Param("repo"), number, true, body.LockReason)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleIssueUnlock serves DELETE /repos/{owner}/{repo}/issues/{number}/lock,
// clearing the lock and its reason. A successful unlock is a bare 204.
func handleIssueUnlock(d Deps) mizu.Handler {
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
		err := d.Issues.SetLocked(ctx, actor.UserID, c.Param("owner"), c.Param("repo"), number, false, nil)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
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
		labels, ok := decodeLabelsBody(c)
		if !ok {
			return nil
		}
		issue, err := d.Issues.EditIssue(ctx, actor.UserID, c.Param("owner"), c.Param("repo"), number, domain.IssuePatch{
			Labels: &labels,
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

// handleAssigneeCheck serves GET /repos/{owner}/{repo}/assignees/{username}.
// GitHub answers 204 when the user can be assigned to issues in the repo and 404
// when they cannot, with no body either way. The assignable set mirrors
// handleAssigneesList: the repository owner. A user the repo itself is not
// visible to gets the same 404, so the endpoint never leaks a private repo.
func handleAssigneeCheck(d Deps) mizu.Handler {
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
		if repo.Owner != nil && repo.Owner.Login == c.Param("username") {
			c.Writer().WriteHeader(http.StatusNoContent)
			return nil
		}
		writeError(c.Writer(), errNotFound())
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

package rest

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
)

// mergeBody is the POST /repos/{owner}/{repo}/merges request: head (a branch
// name or commit-ish) is merged into the base branch with an optional commit
// message.
type mergeBody struct {
	Base          string `json:"base"`
	Head          string `json:"head"`
	CommitMessage string `json:"commit_message"`
}

// handleRepoMerge serves POST /repos/{owner}/{repo}/merges, the server-side
// branch merge PyGitHub's repo.merge and release scripts call. A successful
// merge returns 201 with the merge commit object; a base that already contains
// head returns 204; a missing base or head returns 404; a conflicting merge
// returns 409. A request missing base or head is a 422.
func handleRepoMerge(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		var body mergeBody
		if !decodeJSON(c, &body) {
			return nil
		}
		var fields []FieldError
		if body.Base == "" {
			fields = append(fields, FieldError{Resource: "Merge", Field: "base", Code: "missing_field"})
		}
		if body.Head == "" {
			fields = append(fields, FieldError{Resource: "Merge", Field: "head", Code: "missing_field"})
		}
		if len(fields) > 0 {
			writeError(c.Writer(), errValidation(fields...))
			return nil
		}

		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		repo, commit, err := d.Repos.MergeBranch(ctx, actor.UserID,
			c.Param("owner"), c.Param("repo"), body.Base, body.Head, body.CommitMessage)
		if apiErr := mergeError(err); apiErr != nil {
			writeError(c.Writer(), apiErr)
			return nil
		}
		if errors.Is(err, domain.ErrNothingToMerge) {
			c.Writer().WriteHeader(http.StatusNoContent)
			return nil
		}
		if err != nil {
			return err
		}
		out := d.URLs.RepoCommit(c.Param("owner"), c.Param("repo"), repo.ID, commit)
		writeJSON(c.Writer(), http.StatusCreated, out)
		return nil
	}
}

// mergeError maps a branch-merge domain error to its API response, or nil when
// err is nil, ErrNothingToMerge (handled as 204 by the caller), or unrecognized
// (the caller falls through to the central 500).
func mergeError(err error) *apiError {
	switch {
	case err == nil, errors.Is(err, domain.ErrNothingToMerge):
		return nil
	case errors.Is(err, domain.ErrRepoNotFound), errors.Is(err, domain.ErrMergeMissing):
		return errNotFound()
	case errors.Is(err, domain.ErrForbidden):
		return errForbidden("Must have write access to repository.")
	case errors.Is(err, domain.ErrMergeConflict):
		return errConflict("Merge conflict")
	default:
		return nil
	}
}

// dispatchBody is the POST /repos/{owner}/{repo}/dispatches request: a required
// caller-chosen event_type and an optional opaque client_payload.
type dispatchBody struct {
	EventType     string          `json:"event_type"`
	ClientPayload json.RawMessage `json:"client_payload"`
}

// handleRepoDispatch serves POST /repos/{owner}/{repo}/dispatches, the
// repository_dispatch trigger CI uses to start a custom workflow. It needs write
// access, requires event_type (1-100 chars), and returns 204 on success while
// the webhook fan-out delivers the repository_dispatch event.
func handleRepoDispatch(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		var body dispatchBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if body.EventType == "" || len(body.EventType) > 100 {
			writeError(c.Writer(), errValidation(FieldError{
				Resource: "Dispatch", Field: "event_type", Code: "invalid",
			}))
			return nil
		}

		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		err := d.Repos.Dispatch(ctx, actor.UserID, c.Param("owner"), c.Param("repo"),
			body.EventType, body.ClientPayload)
		switch {
		case errors.Is(err, domain.ErrRepoNotFound):
			writeError(c.Writer(), errNotFound())
			return nil
		case errors.Is(err, domain.ErrForbidden):
			writeError(c.Writer(), errForbidden("Must have write access to repository."))
			return nil
		case err != nil:
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleRepoByID serves GET /repositories/{id}, the lookup-by-id octokit and
// webhook consumers use when they have a repository's numeric id but not its
// owner/name. It renders the same full repository object GET /repos/{o}/{r}
// does, and 404s an id that does not exist or the viewer cannot see.
func handleRepoByID(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		id, ok := pathInt64(c, "id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		repo, err := d.Repos.GetRepoByID(ctx, actor.UserID, id)
		if errors.Is(err, domain.ErrRepoNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		det, err := repoDetail(d, c, repo)
		if err != nil {
			return err
		}
		perm, err := repoPermissions(ctx, d, actor, repo)
		if err != nil {
			return err
		}
		body := d.URLs.RepositoryFull(repo, d.NodeFormat, perm, det)
		writeJSON(c.Writer(), http.StatusOK, body)
		return nil
	}
}

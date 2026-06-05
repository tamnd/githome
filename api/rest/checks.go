package rest

import (
	"errors"
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/presenter/restmodel"
)

// statusCreateBody is the POST /statuses/{sha} request. State is one of error,
// failure, pending, or success; context defaults to "default" on GitHub when
// omitted, which the service applies.
type statusCreateBody struct {
	State       string `json:"state"`
	TargetURL   string `json:"target_url"`
	Description string `json:"description"`
	Context     string `json:"context"`
}

// checkRunBody is the POST /check-runs and PATCH /check-runs/{id} request. On
// create, name and head_sha are required; on update both are optional and only
// the present fields move.
type checkRunBody struct {
	Name       string          `json:"name"`
	HeadSHA    string          `json:"head_sha"`
	Status     string          `json:"status"`
	Conclusion string          `json:"conclusion"`
	DetailsURL string          `json:"details_url"`
	ExternalID string          `json:"external_id"`
	Output     *checkRunOutput `json:"output"`
}

// checkRunOutput is the output block of a check run create or update.
type checkRunOutput struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Text    string `json:"text"`
}

// mountChecks registers the commit status and check run endpoints on r. The four
// per-ref read collections hang off /commits/{ref}; reports are posted to
// /statuses/{sha} and /check-runs.
func mountChecks(r *mizu.Router, d Deps) {
	r.Get("/repos/{owner}/{repo}/commits/{ref}/statuses", handleStatusesList(d))
	r.Get("/repos/{owner}/{repo}/commits/{ref}/status", handleCombinedStatus(d))
	r.Get("/repos/{owner}/{repo}/commits/{ref}/check-runs", handleCheckRunsList(d))
	r.Get("/repos/{owner}/{repo}/commits/{ref}/check-suites", handleCheckSuitesList(d))

	r.Post("/repos/{owner}/{repo}/statuses/{sha}", handleStatusCreate(d))

	r.Post("/repos/{owner}/{repo}/check-runs", handleCheckRunCreate(d))
	r.Get("/repos/{owner}/{repo}/check-runs/{check_run_id}", handleCheckRunGet(d))
	r.Patch("/repos/{owner}/{repo}/check-runs/{check_run_id}", handleCheckRunUpdate(d))
}

// handleStatusesList serves GET /repos/{owner}/{repo}/commits/{ref}/statuses,
// newest first.
func handleStatusesList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo, ref := c.Param("owner"), c.Param("repo"), c.Param("ref")
		statuses, _, err := d.Checks.ListStatuses(c.Request().Context(), actor.UserID, owner, repo, ref)
		if checksError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		out := make([]restmodel.Status, 0, len(statuses))
		for _, s := range statuses {
			out = append(out, d.URLs.Status(owner, repo, s, d.NodeFormat))
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleCombinedStatus serves GET /repos/{owner}/{repo}/commits/{ref}/status.
func handleCombinedStatus(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo, ref := c.Param("owner"), c.Param("repo"), c.Param("ref")
		cs, err := d.Checks.CombinedStatus(c.Request().Context(), actor.UserID, owner, repo, ref)
		if checksError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.CombinedStatus(owner, repo, cs, d.NodeFormat))
		return nil
	}
}

// handleCheckRunsList serves GET
// /repos/{owner}/{repo}/commits/{ref}/check-runs.
func handleCheckRunsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo, ref := c.Param("owner"), c.Param("repo"), c.Param("ref")
		runs, _, err := d.Checks.ListCheckRuns(c.Request().Context(), actor.UserID, owner, repo, ref)
		if checksError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.CheckRunList(owner, repo, runs, d.NodeFormat))
		return nil
	}
}

// handleCheckSuitesList serves GET
// /repos/{owner}/{repo}/commits/{ref}/check-suites.
func handleCheckSuitesList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo, ref := c.Param("owner"), c.Param("repo"), c.Param("ref")
		suites, _, err := d.Checks.ListCheckSuites(c.Request().Context(), actor.UserID, owner, repo, ref)
		if checksError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.CheckSuiteList(owner, repo, suites, d.NodeFormat))
		return nil
	}
}

// handleStatusCreate serves POST /repos/{owner}/{repo}/statuses/{sha}.
func handleStatusCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		var body statusCreateBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if body.State == "" {
			writeError(c.Writer(), errValidation(FieldError{Resource: "Status", Field: "state", Code: "missing_field"}))
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo, sha := c.Param("owner"), c.Param("repo"), c.Param("sha")
		st, err := d.Checks.CreateStatus(c.Request().Context(), actor.UserID, owner, repo, sha, domain.StatusInput{
			State:       body.State,
			Context:     body.Context,
			TargetURL:   body.TargetURL,
			Description: body.Description,
		})
		if checksError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.Status(owner, repo, st, d.NodeFormat))
		return nil
	}
}

// handleCheckRunCreate serves POST /repos/{owner}/{repo}/check-runs.
func handleCheckRunCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		var body checkRunBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if body.Name == "" {
			writeError(c.Writer(), errValidation(FieldError{Resource: "CheckRun", Field: "name", Code: "missing_field"}))
			return nil
		}
		if body.HeadSHA == "" {
			writeError(c.Writer(), errValidation(FieldError{Resource: "CheckRun", Field: "head_sha", Code: "missing_field"}))
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		run, err := d.Checks.CreateCheckRun(c.Request().Context(), actor.UserID, owner, repo, checkRunInput(body))
		if checksError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.CheckRun(owner, repo, run, d.NodeFormat))
		return nil
	}
}

// handleCheckRunGet serves GET /repos/{owner}/{repo}/check-runs/{check_run_id}.
func handleCheckRunGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		id, ok := pathInt64(c, "check_run_id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		run, err := d.Checks.GetCheckRun(c.Request().Context(), actor.UserID, owner, repo, id)
		if checksError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.CheckRun(owner, repo, run, d.NodeFormat))
		return nil
	}
}

// handleCheckRunUpdate serves PATCH
// /repos/{owner}/{repo}/check-runs/{check_run_id}.
func handleCheckRunUpdate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		id, ok := pathInt64(c, "check_run_id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body checkRunBody
		if !decodeJSON(c, &body) {
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		run, err := d.Checks.UpdateCheckRun(c.Request().Context(), actor.UserID, owner, repo, id, checkRunInput(body))
		if checksError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.CheckRun(owner, repo, run, d.NodeFormat))
		return nil
	}
}

// checkRunInput maps the wire check run body to the domain input, flattening the
// optional output block.
func checkRunInput(body checkRunBody) domain.CheckRunInput {
	in := domain.CheckRunInput{
		Name:       body.Name,
		HeadSHA:    body.HeadSHA,
		Status:     body.Status,
		Conclusion: body.Conclusion,
		DetailsURL: body.DetailsURL,
		ExternalID: body.ExternalID,
	}
	if body.Output != nil {
		in.OutputTitle = body.Output.Title
		in.OutputSummary = body.Output.Summary
		in.OutputText = body.Output.Text
	}
	return in
}

// checksError maps a checks-subsystem domain error to its API response, returning
// true when it wrote one.
func checksError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, domain.ErrCheckNotFound),
		errors.Is(err, domain.ErrRepoNotFound):
		writeError(w, errNotFound())
	case errors.Is(err, domain.ErrForbidden):
		writeError(w, errForbidden("Write access to the repository is required."))
	case errors.Is(err, domain.ErrValidation):
		writeError(w, errValidation())
	default:
		return false
	}
	return true
}

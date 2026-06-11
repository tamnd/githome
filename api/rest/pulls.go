package rest

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/etag"
	"github.com/tamnd/githome/presenter/restmodel"
	"github.com/tamnd/githome/store"
)

// pullCreateBody is the POST /pulls request. head and base are branch names in
// the repository; the cross-repository "owner:branch" head form arrives with the
// fork milestone. draft opens the pull request as a draft.
type pullCreateBody struct {
	Title               string  `json:"title"`
	Head                string  `json:"head"`
	Base                string  `json:"base"`
	Body                *string `json:"body"`
	Draft               bool    `json:"draft"`
	MaintainerCanModify bool    `json:"maintainer_can_modify"`
}

// handlePullsList serves GET /repos/{owner}/{repo}/pulls. The state query selects
// open, closed, or all, defaulting to open.
func handlePullsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		page, perr := parsePage(c)
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		q := domain.PRQuery{
			State:   c.Query("state"),
			Page:    page.Page,
			PerPage: page.PerPage,
			Cursor:  c.Query("cursor"),
		}

		// Flat read path: a cursor follow-up seeks on the per-repo number
		// (newest first, the only order this list offers) and skips the COUNT
		// that page-number navigation needs for rel="last". Only rel="next" is
		// offered, so deep walks cost the page, not a count plus a deep OFFSET.
		if q.Cursor != "" {
			prs, hasMore, err := d.Pulls.ListPRsPage(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), q)
			if pullError(c.Writer(), err) {
				return nil
			}
			if err != nil {
				return err
			}
			out := make([]any, 0, len(prs))
			for _, pr := range prs {
				out = append(out, d.URLs.PullRequest(c.Param("owner"), c.Param("repo"), pr, d.NodeFormat, false))
			}
			var nextCursor string
			if hasMore && len(prs) > 0 {
				nextCursor = store.EncodePullCursor(store.PullCursor{Number: prs[len(prs)-1].Number})
			}
			writeNextCursorLink(c.Writer(), c.Request(), d.URLs, nextCursor)
			conditionalJSON(c.Writer(), c.Request(), http.StatusOK, out)
			return nil
		}

		// Seed a version ETag from one aggregate over the state-filtered
		// window and short-circuit an If-None-Match hit before fetching,
		// assembling, or marshaling the page, the same shape as the issues
		// list. The marker covers the pull row too, so head pushes and
		// mergeability recomputes invalidate the tag.
		total, marker, err := d.Pulls.ListPRsVersion(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), q.State)
		if pullError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		tag := etag.Version(c.Request().URL.RequestURI()+"|"+marker, int64(total))
		if notModified(c.Writer(), c.Request(), tag) {
			return nil
		}

		prs, err := d.Pulls.ListPRsWindow(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), q)
		if pullError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		out := make([]any, 0, len(prs))
		for _, pr := range prs {
			out = append(out, d.URLs.PullRequest(c.Param("owner"), c.Param("repo"), pr, d.NodeFormat, false))
		}
		page.Total = total

		// Hand off a cursor on the next-link so a client can switch from
		// page-number navigation to the flat keyset path after the first page.
		var nextCursor string
		if len(prs) > 0 && page.HasNextPage() {
			nextCursor = store.EncodePullCursor(store.PullCursor{Number: prs[len(prs)-1].Number})
		}
		writeLinkHeaderCursor(c.Writer(), c.Request(), d.URLs, page, nextCursor)
		conditionalVersioned(c.Writer(), c.Request(), http.StatusOK, out, tag)
		return nil
	}
}

// handlePullCreate serves POST /repos/{owner}/{repo}/pulls.
func handlePullCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		var body pullCreateBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if strings.TrimSpace(body.Title) == "" {
			writeError(c.Writer(), errValidation(FieldError{Resource: "PullRequest", Field: "title", Code: "missing_field"}))
			return nil
		}
		if strings.TrimSpace(body.Head) == "" {
			writeError(c.Writer(), errValidation(FieldError{Resource: "PullRequest", Field: "head", Code: "missing_field"}))
			return nil
		}
		if strings.TrimSpace(body.Base) == "" {
			writeError(c.Writer(), errValidation(FieldError{Resource: "PullRequest", Field: "base", Code: "missing_field"}))
			return nil
		}
		in := domain.PRInput{
			Title:               body.Title,
			Body:                body.Body,
			Base:                body.Base,
			Head:                body.Head,
			Draft:               body.Draft,
			MaintainerCanModify: body.MaintainerCanModify,
		}
		actor := auth.ActorFrom(c.Request().Context())
		pr, err := d.Pulls.CreatePR(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), in)
		if pullError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.Notifications != nil {
			text := ""
			if pr.Body != nil {
				text = *pr.Body
			}
			d.Notifications.NotifyIssueOpened(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), pr.Number, text)
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.PullRequest(c.Param("owner"), c.Param("repo"), pr, d.NodeFormat, true))
		return nil
	}
}

// handlePullGet serves GET /repos/{owner}/{repo}/pulls/{number}. The diff and
// patch media types in the Accept header switch the body to the raw unified diff
// or the mbox patch series, the path gh pr diff takes; otherwise it is the JSON
// pull request.
func handlePullGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		switch accepted := pullMedia(c.Request().Header.Get("Accept")); accepted {
		case mediaDiff:
			raw, err := d.Pulls.Diff(c.Request().Context(), actor.UserID, owner, repo, number)
			if pullError(c.Writer(), err) {
				return nil
			}
			if err != nil {
				return err
			}
			writePullText(c.Writer(), "application/vnd.github.diff; charset=utf-8", raw)
			return nil
		case mediaPatch:
			raw, err := d.Pulls.Patch(c.Request().Context(), actor.UserID, owner, repo, number)
			if pullError(c.Writer(), err) {
				return nil
			}
			if err != nil {
				return err
			}
			writePullText(c.Writer(), "application/vnd.github.patch; charset=utf-8", raw)
			return nil
		default:
			pr, err := d.Pulls.GetPR(c.Request().Context(), actor.UserID, owner, repo, number)
			if pullError(c.Writer(), err) {
				return nil
			}
			if err != nil {
				return err
			}
			conditionalJSON(c.Writer(), c.Request(), http.StatusOK, d.URLs.PullRequest(owner, repo, pr, d.NodeFormat, true))
			return nil
		}
	}
}

// pullFiles serves the per-file diff of the pull request range, the body of GET
// /repos/{owner}/{repo}/pulls/{number}/files. The number arrives from the shared
// /pulls/{seg1}/{seg2} GET dispatcher.
func pullFiles(d Deps, c *mizu.Ctx, number int64) error {
	actor := auth.ActorFrom(c.Request().Context())
	owner, repo := c.Param("owner"), c.Param("repo")
	pr, err := d.Pulls.GetPR(c.Request().Context(), actor.UserID, owner, repo, number)
	if pullError(c.Writer(), err) {
		return nil
	}
	if err != nil {
		return err
	}
	files, err := d.Pulls.Files(c.Request().Context(), actor.UserID, owner, repo, number)
	if pullError(c.Writer(), err) {
		return nil
	}
	if err != nil {
		return err
	}
	page, perr := parsePage(c)
	if perr != nil {
		writeError(c.Writer(), perr)
		return nil
	}
	files = paginateSlice(&page, files)
	out := make([]restmodel.PullRequestFile, 0, len(files))
	for _, f := range files {
		out = append(out, d.URLs.PullRequestFile(owner, repo, pr.Head.SHA, f))
	}
	writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
	writeJSON(c.Writer(), http.StatusOK, out)
	return nil
}

// pullCommits serves the pull request's own commits oldest first, the body of GET
// /repos/{owner}/{repo}/pulls/{number}/commits. The number arrives from the
// shared /pulls/{seg1}/{seg2} GET dispatcher.
func pullCommits(d Deps, c *mizu.Ctx, number int64) error {
	actor := auth.ActorFrom(c.Request().Context())
	owner, repo := c.Param("owner"), c.Param("repo")
	prq, err := d.Pulls.GetPR(c.Request().Context(), actor.UserID, owner, repo, number)
	if pullError(c.Writer(), err) {
		return nil
	}
	if err != nil {
		return err
	}
	commits, err := d.Pulls.Commits(c.Request().Context(), actor.UserID, owner, repo, number)
	if pullError(c.Writer(), err) {
		return nil
	}
	if err != nil {
		return err
	}
	page, perr := parsePage(c)
	if perr != nil {
		writeError(c.Writer(), perr)
		return nil
	}
	commits = paginateSlice(&page, commits)
	out := make([]restmodel.RepoCommit, 0, len(commits))
	for _, cm := range commits {
		out = append(out, d.URLs.RepoCommit(owner, repo, prq.Repo.ID, cm))
	}
	writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
	writeJSON(c.Writer(), http.StatusOK, out)
	return nil
}

// mediaDiff and mediaPatch name the two raw media types a pull request GET can
// negotiate; mediaJSON is the default JSON body.
const (
	mediaJSON = iota
	mediaDiff
	mediaPatch
)

// pullMedia reads an Accept header and reports which pull request media a client
// asked for. The diff and patch suffixes match GitHub's vendor media types in
// either the versioned (v3) or unversioned form.
func pullMedia(accept string) int {
	a := strings.ToLower(accept)
	switch {
	case strings.Contains(a, "diff"):
		return mediaDiff
	case strings.Contains(a, "patch"):
		return mediaPatch
	default:
		return mediaJSON
	}
}

// writePullText writes a raw diff or patch body with its media type.
func writePullText(w http.ResponseWriter, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// pullError maps a pull-request-subsystem domain error to its API response,
// returning true when it wrote one. A nil or unrecognized error returns false so
// the caller falls through to its success path or the central 500 handler.
func pullError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, domain.ErrPullNotFound),
		errors.Is(err, domain.ErrRepoNotFound),
		errors.Is(err, domain.ErrIssueNotFound):
		writeError(w, errNotFound())
	case errors.Is(err, domain.ErrForbidden):
		writeError(w, errForbidden("Write access to the repository is required."))
	case errors.Is(err, domain.ErrNotMergeable):
		writeError(w, errMethodNotAllowed("Pull Request is not mergeable"))
	case errors.Is(err, domain.ErrHeadMismatch):
		writeError(w, errConflict("Head branch was modified. Review and try the merge again."))
	case errors.Is(err, domain.ErrInvalidMergeMethod):
		writeError(w, errValidation(FieldError{Resource: "PullRequest", Field: "merge_method", Code: "invalid"}))
	case errors.Is(err, domain.ErrValidation):
		writeError(w, errValidation())
	default:
		return false
	}
	return true
}

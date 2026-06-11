package rest

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
)

// handleSearchIssues serves GET /search/issues. The q query carries the search
// string in GitHub's mini-language; sort and order pick the ordering. A missing
// q is a 422, matching GitHub's required-field error.
func handleSearchIssues(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		raw, ok := searchTerm(c)
		if !ok {
			writeError(c.Writer(), errValidation(missingQ()))
			return nil
		}
		page, perr := parsePageFor(c, "Search")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		hits, total, err := d.Search.SearchIssues(c.Request().Context(), actor.UserID, raw, c.Query("sort"), c.Query("order"), page.Page, page.PerPage)
		if err != nil {
			return err
		}
		body := d.URLs.SearchIssues(hits, total, false, d.NodeFormat)
		page.Total = total
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		conditionalJSON(c.Writer(), c.Request(), http.StatusOK, body)
		return nil
	}
}

// handleSearchRepositories serves GET /search/repositories.
func handleSearchRepositories(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		raw, ok := searchTerm(c)
		if !ok {
			writeError(c.Writer(), errValidation(missingQ()))
			return nil
		}
		page, perr := parsePageFor(c, "Search")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		repos, total, err := d.Search.SearchRepositories(c.Request().Context(), actor.UserID, raw, c.Query("sort"), c.Query("order"), page.Page, page.PerPage)
		if err != nil {
			return err
		}
		body := d.URLs.SearchRepositories(repos, total, false, d.NodeFormat)
		page.Total = total
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		conditionalJSON(c.Writer(), c.Request(), http.StatusOK, body)
		return nil
	}
}

// handleSearchCode serves GET /search/code. The query must scope the walk with
// a repo:, user:, or org: qualifier; an unscoped query is a 422, since the walk
// cannot span every repository.
func handleSearchCode(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		raw, ok := searchTerm(c)
		if !ok {
			writeError(c.Writer(), errValidation(missingQ()))
			return nil
		}
		page, perr := parsePageFor(c, "Search")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		results, total, incomplete, err := d.Search.SearchCode(c.Request().Context(), actor.UserID, raw, page.Page, page.PerPage)
		if errors.Is(err, domain.ErrSearchScopeRequired) {
			writeError(c.Writer(), errValidation(FieldError{
				Resource: "Search", Field: "q", Code: "invalid",
				Message: "Code search requires a repo, user, or org qualifier to scope the search.",
			}))
			return nil
		}
		if err != nil {
			return err
		}
		body := d.URLs.SearchCode(results, total, incomplete, d.NodeFormat)
		page.Total = total
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		conditionalJSON(c.Writer(), c.Request(), http.StatusOK, body)
		return nil
	}
}

// searchTerm reads the q query parameter, reporting false when it is missing or
// blank so the handler returns the required-field error.
func searchTerm(c *mizu.Ctx) (string, bool) {
	raw := strings.TrimSpace(c.Query("q"))
	if raw == "" {
		return "", false
	}
	return raw, true
}

// missingQ is the validation field error for a search with no q.
func missingQ() FieldError {
	return FieldError{Resource: "Search", Field: "q", Code: "missing"}
}

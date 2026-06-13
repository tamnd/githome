package rest

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/presenter/restmodel"
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
		if wantsTextMatch(c.Request()) {
			terms := queryTerms(raw)
			for i := range body.Items {
				body.Items[i].TextMatches = issueTextMatches(body.Items[i], terms)
			}
		}
		page.Total = total
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		conditionalJSON(c.Writer(), c.Request(), http.StatusOK, body)
		return nil
	}
}

// issueTextMatches builds the text_matches entries for an issue hit from its
// title and body, the two free-text properties GitHub highlights.
func issueTextMatches(item restmodel.IssueSearchItem, terms []string) []restmodel.TextMatch {
	var out []restmodel.TextMatch
	if tm, ok := textMatch(item.URL, strptr("Issue"), "title", item.Title, terms); ok {
		out = append(out, tm)
	}
	if item.Body != nil {
		if tm, ok := textMatch(item.URL, strptr("Issue"), "body", *item.Body, terms); ok {
			out = append(out, tm)
		}
	}
	return out
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
		if wantsTextMatch(c.Request()) {
			terms := queryTerms(raw)
			for i := range body.Items {
				body.Items[i].TextMatches = repoTextMatches(body.Items[i], terms)
			}
		}
		page.Total = total
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		conditionalJSON(c.Writer(), c.Request(), http.StatusOK, body)
		return nil
	}
}

// repoTextMatches builds the text_matches entries for a repository hit from its
// name and description.
func repoTextMatches(item restmodel.RepoSearchItem, terms []string) []restmodel.TextMatch {
	var out []restmodel.TextMatch
	if tm, ok := textMatch(item.URL, strptr("Repository"), "name", item.Name, terms); ok {
		out = append(out, tm)
	}
	if item.Desc != nil {
		if tm, ok := textMatch(item.URL, strptr("Repository"), "description", *item.Desc, terms); ok {
			out = append(out, tm)
		}
	}
	return out
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
				Message: "Must include at least one user, organization, or repository",
			}))
			return nil
		}
		if err != nil {
			return err
		}
		body := d.URLs.SearchCode(results, total, incomplete, d.NodeFormat)
		if wantsTextMatch(c.Request()) {
			terms := queryTerms(raw)
			for i := range body.Items {
				body.Items[i].TextMatches = codeTextMatches(body.Items[i], terms)
			}
		}
		page.Total = total
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		conditionalJSON(c.Writer(), c.Request(), http.StatusOK, body)
		return nil
	}
}

// codeTextMatches builds the text_matches entries for a code hit. Only the file
// path is matched: githome's code index stores paths and blob ids, not the
// content fragments GitHub highlights for in-file matches, so the richer
// content text matches are not synthesized.
func codeTextMatches(item restmodel.CodeSearchItem, terms []string) []restmodel.TextMatch {
	if tm, ok := textMatch(item.URL, nil, "path", item.Path, terms); ok {
		return []restmodel.TextMatch{tm}
	}
	return nil
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

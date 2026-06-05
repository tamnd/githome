package rest

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/presenter"
)

// Page is one page of a list response. Page and PerPage come from the request;
// Total is filled in by the handler from the domain count once the page is
// fetched, and finalize derives the navigation flags the Link header needs.
type Page struct {
	Page    int
	PerPage int
	Total   int
	Last    int
	HasPrev bool
	HasNext bool
}

// Offset is the row offset this page starts at, the value the store query takes.
func (p Page) Offset() int { return (p.Page - 1) * p.PerPage }

// parsePage reads the page and per_page query parameters with GitHub's bounds: a
// missing value defaults (page 1, per_page 30), a per_page above 100 is clamped
// rather than rejected, and anything non-integer or below 1 is a 422 before any
// work happens.
func parsePage(c *mizu.Ctx) (Page, *apiError) {
	page, perPage := 1, 30
	if v := c.Query("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return Page{}, errValidation(FieldError{Resource: "Search", Field: "page", Code: "invalid"})
		}
		page = n
	}
	if v := c.Query("per_page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return Page{}, errValidation(FieldError{Resource: "Search", Field: "per_page", Code: "invalid"})
		}
		if n > 100 {
			n = 100
		}
		perPage = n
	}
	return Page{Page: page, PerPage: perPage}, nil
}

// finalize derives Last, HasPrev, and HasNext from Total. The last page is at
// least 1 even when the collection is empty, so a request for page 1 of an empty
// list is the single page it is, never a page past the end.
func (p *Page) finalize() {
	p.Last = (p.Total + p.PerPage - 1) / p.PerPage
	if p.Last < 1 {
		p.Last = 1
	}
	p.HasPrev = p.Page > 1
	p.HasNext = p.Page < p.Last
}

// writeLinkHeader sets the RFC 5988 Link header for a paginated response, the
// header gh and every paginating client follows. A single-page result carries
// no header, matching GitHub. The rels appear in GitHub's order: prev, next,
// last, first. Every other query parameter rides through unchanged; only page
// is rewritten, and the URL is rebuilt on the configured API host.
func writeLinkHeader(w http.ResponseWriter, r *http.Request, ub *presenter.URLBuilder, p Page) {
	p.finalize()
	if !p.HasPrev && !p.HasNext {
		return
	}
	path := r.URL.Path
	raw := r.URL.RawQuery
	var parts []string
	add := func(rel string, page int) {
		parts = append(parts, "<"+ub.PageLink(path, raw, page)+`>; rel="`+rel+`"`)
	}
	if p.HasPrev {
		add("prev", p.Page-1)
	}
	if p.HasNext {
		add("next", p.Page+1)
		add("last", p.Last)
	}
	if p.HasPrev {
		add("first", 1)
	}
	w.Header().Set("Link", strings.Join(parts, ", "))
}

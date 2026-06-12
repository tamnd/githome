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

// HasNextPage reports whether there is at least one more page after this one,
// using the Total set by the handler. It is called before finalize so the
// handler can decide whether to build a cursor before writing the Link header.
func (p Page) HasNextPage() bool {
	if p.Total <= 0 {
		return false
	}
	return p.Page*p.PerPage < p.Total
}

// parsePage reads the page and per_page query parameters with GitHub's bounds: a
// missing value defaults (page 1, per_page 30), a per_page above 100 is clamped
// rather than rejected, and anything non-integer or below 1 is a 422 before any
// work happens. resource names the endpoint family the 422 reports, the way
// GitHub's validation errors carry the resource being listed rather than a
// fixed one.
func parsePageFor(c *mizu.Ctx, resource string) (Page, *apiError) {
	page, perPage := 1, 30
	if v := c.Query("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return Page{}, errValidation(FieldError{Resource: resource, Field: "page", Code: "invalid"})
		}
		page = n
	}
	if v := c.Query("per_page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return Page{}, errValidation(FieldError{Resource: resource, Field: "per_page", Code: "invalid"})
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

// paginateSlice clips a fully-loaded list to the requested page window and
// records the total on the page, so handlers whose domain calls return the
// whole collection can paginate and emit a counted Link header without each
// reimplementing the window math. A page past the end is an empty list, the
// same answer GitHub gives.
func paginateSlice[T any](p *Page, items []T) []T {
	p.Total = len(items)
	start := p.Offset()
	if start > len(items) {
		start = len(items)
	}
	end := start + p.PerPage
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

// writeLinkHeader sets the RFC 5988 Link header for a paginated response, the
// header gh and every paginating client follows. A single-page result carries
// no header, matching GitHub. The rels appear in GitHub's order: prev, next,
// last, first. Every other query parameter rides through unchanged; only page
// is rewritten, and the URL is rebuilt on the configured API host.
func writeLinkHeader(w http.ResponseWriter, r *http.Request, ub *presenter.URLBuilder, p Page) {
	writeLinkHeaderCursor(w, r, ub, p, "")
}

// writeLinkHeaderCursor is writeLinkHeader with an optional keyset cursor for
// the next-page link. When nextCursor is non-empty the rel="next" URL carries
// ?cursor=... instead of ?page=N+1, enabling index-seek pagination on the
// follow-up request. All other rels (prev, last, first) keep the page-number
// form so random access and backward navigation remain possible.
func writeLinkHeaderCursor(w http.ResponseWriter, r *http.Request, ub *presenter.URLBuilder, p Page, nextCursor string) {
	p.finalize()
	if !p.HasPrev && !p.HasNext {
		return
	}
	path := r.URL.Path
	raw := r.URL.RawQuery
	var parts []string
	addPage := func(rel string, page int) {
		parts = append(parts, "<"+ub.PageLink(path, raw, page)+`>; rel="`+rel+`"`)
	}
	if p.HasPrev {
		addPage("prev", p.Page-1)
	}
	if p.HasNext {
		if nextCursor != "" {
			parts = append(parts, "<"+ub.CursorLink(path, raw, nextCursor, p.Page+1)+`>; rel="next"`)
		} else {
			addPage("next", p.Page+1)
		}
		addPage("last", p.Last)
	}
	if p.HasPrev {
		addPage("first", 1)
	}
	w.Header().Set("Link", strings.Join(parts, ", "))
}

// writeLinkHeaderUncounted sets the Link header for a list whose total is never
// counted, such as a commit walk. Only prev, next, and first appear; there is
// no rel="last" because deriving it would need the count the endpoint exists to
// avoid, which is also why GitHub's own commits listing omits it.
func writeLinkHeaderUncounted(w http.ResponseWriter, r *http.Request, ub *presenter.URLBuilder, p Page, hasNext bool) {
	hasPrev := p.Page > 1
	if !hasPrev && !hasNext {
		return
	}
	path := r.URL.Path
	raw := r.URL.RawQuery
	var parts []string
	addPage := func(rel string, page int) {
		parts = append(parts, "<"+ub.PageLink(path, raw, page)+`>; rel="`+rel+`"`)
	}
	if hasPrev {
		addPage("prev", p.Page-1)
	}
	if hasNext {
		addPage("next", p.Page+1)
	}
	if hasPrev {
		addPage("first", 1)
	}
	w.Header().Set("Link", strings.Join(parts, ", "))
}

// writeNextCursorLink sets the Link header for the flat read path: a cursor
// walk skips the COUNT that page-number navigation needs for rel="last", so no
// total is known and the forward hop stays a keyset cursor. The cursor links
// carry the page number the hop lands on, so on a follow-up the position is
// known and rel="prev" and rel="first" appear as plain page-number links the
// counted path serves; only rel="last" is honestly absent. An empty cursor on
// page 1 means a single page and no header, matching GitHub.
func writeNextCursorLink(w http.ResponseWriter, r *http.Request, ub *presenter.URLBuilder, p Page, nextCursor string) {
	hasPrev := p.Page > 1
	if !hasPrev && nextCursor == "" {
		return
	}
	path := r.URL.Path
	raw := r.URL.RawQuery
	var parts []string
	if hasPrev {
		parts = append(parts, "<"+ub.PageLink(path, raw, p.Page-1)+`>; rel="prev"`)
	}
	if nextCursor != "" {
		parts = append(parts, "<"+ub.CursorLink(path, raw, nextCursor, p.Page+1)+`>; rel="next"`)
	}
	if hasPrev {
		parts = append(parts, "<"+ub.PageLink(path, raw, 1)+`>; rel="first"`)
	}
	w.Header().Set("Link", strings.Join(parts, ", "))
}

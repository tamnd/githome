package pulls

import (
	"context"
	"net/url"
	"strconv"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

// indexPerPage is how many pull requests one index page lists. It is a fixed
// window, not a user knob, the same bound the issues index uses so the two lists
// page alike.
const indexPerPage = 25

// Index renders the pull-request list with its open and closed tabs. The PR index
// is the issue index frame pre-filtered to pull requests: the ?state= selector is
// the only filter F4 reads, the open and closed tabs flip it, and the list pages
// with prev and next links, all server-side so it works with no JavaScript. The
// richer is:label and is:author query grammar the issues index parses arrives for
// pulls in a later pass; F4 ships the state tabs and pagination the merge flow
// needs. See implementation/09 section 2.
func (h *Handlers) Index(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	owner := ownerLogin(repo)

	state := stateParam(c.Query("state"))
	page := pageParam(c.Query("page"))
	viewerPK := webmw.ViewerID(ctx)

	rows, total, err := h.pulls.ListPRs(ctx, viewerPK, owner, repo.Name, domain.PRQuery{
		State:   state,
		Page:    page,
		PerPage: indexPerPage,
	})
	if err != nil {
		return h.listError(c, err)
	}

	openTotal, closedTotal := h.tabCounts(ctx, viewerPK, owner, repo.Name, state, total)

	vm := view.PRIndexVM{
		Chrome:    h.chrome(c, repo.Name+" pull requests"),
		Header:    h.header(repo),
		Nav:       h.nav(repo),
		Repo:      repoRef(repo),
		OpenTab:   view.FilterTab{Label: "Open", Count: openTotal, URL: tabURL(owner, repo.Name, "open"), IsActive: state == "open"},
		ClosedTab: view.FilterTab{Label: "Closed", Count: closedTotal, URL: tabURL(owner, repo.Name, "closed"), IsActive: state == "closed"},
		Rows:      h.indexRows(repo, rows),
		Pager:     h.pager(owner, repo.Name, state, page, total),
	}
	if len(vm.Rows) == 0 {
		vm.Empty = true
		vm.EmptyReason = emptyReason(state)
	}
	return h.render.Page(c, "pulls/index", vm)
}

// indexRows maps the listed pull requests into index rows.
func (h *Handlers) indexRows(repo *domain.Repo, prs []*domain.PullRequest) []view.PRRow {
	out := make([]view.PRRow, 0, len(prs))
	for _, pr := range prs {
		out = append(out, h.prRow(repo, pr))
	}
	return out
}

// tabCounts returns the open and closed totals for the state tabs. The active
// state's total comes from the list query already run; the other is a count-only
// probe in the flipped state, the same one-extra-read the issues index pays.
func (h *Handlers) tabCounts(ctx context.Context, viewerPK int64, owner, name, state string, activeTotal int) (open, closed int) {
	other := "open"
	if state == "open" {
		other = "closed"
	}
	_, otherTotal, err := h.pulls.ListPRs(ctx, viewerPK, owner, name, domain.PRQuery{
		State:   other,
		Page:    1,
		PerPage: 1,
	})
	if err != nil {
		otherTotal = 0
	}
	if state == "open" {
		return activeTotal, otherTotal
	}
	return otherTotal, activeTotal
}

// pager builds the prev and next links from the running total. A page beyond the
// last shows no next; the first page shows no prev.
func (h *Handlers) pager(owner, name, state string, page, total int) view.Pager {
	p := view.Pager{Page: page}
	if page > 1 {
		p.PrevURL = route.Pulls(owner, name, listQuery(state, page-1))
	}
	if page*indexPerPage < total {
		p.NextURL = route.Pulls(owner, name, listQuery(state, page+1))
	}
	return p
}

// listError maps a domain list error to a response: a repo that vanished between
// the resolve and the list is a 404, anything else a 500.
func (h *Handlers) listError(c *mizu.Ctx, err error) error {
	if isNotFound(err) {
		return h.notFound(c)
	}
	return h.render.ServerError(c, err)
}

// tabURL is the canonical URL for a state tab: the bare index for open (the
// default the list canonicalizes to) and ?state=closed for closed.
func tabURL(owner, name, state string) string {
	if state == "closed" {
		return route.Pulls(owner, name, "state=closed")
	}
	return route.Pulls(owner, name, "")
}

// listQuery builds the raw query string for a state plus a page number, omitting
// the defaults (open state, page 1) so the first open page stays the clean URL.
func listQuery(state string, page int) string {
	vals := url.Values{}
	if state == "closed" {
		vals.Set("state", "closed")
	}
	if page > 1 {
		vals.Set("page", strconv.Itoa(page))
	}
	return vals.Encode()
}

// stateParam reads the ?state= selector, defaulting to open and treating only the
// explicit "closed" as closed so a stray value lands on the open list rather than
// an empty one.
func stateParam(s string) string {
	if s == "closed" {
		return "closed"
	}
	return "open"
}

// pageParam parses a 1-based page number, clamping a missing or malformed value to
// the first page.
func pageParam(s string) int {
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// emptyReason is the blankslate line for a list with no results in the given
// state.
func emptyReason(state string) string {
	if state == "closed" {
		return "No closed pull requests."
	}
	return "No open pull requests."
}

// viewer assembles the viewerCtx from the request: the PK from the middleware and
// the login from the resolved chrome viewer.
func (h *Handlers) viewer(c *mizu.Ctx) viewerCtx {
	ctx := c.Context()
	vc := viewerCtx{pk: webmw.ViewerID(ctx)}
	if v := view.ViewerFrom(ctx); v != nil {
		vc.login = v.Login
	}
	return vc
}

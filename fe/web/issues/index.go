package issues

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

// indexPerPage is how many issues one index page lists. It is a fixed window, not
// a user knob, matching the code views' bounded lists.
const indexPerPage = 25

// Index renders the issues list with its search-and-filter bar. The ?q= string is
// the single source of filter truth: it is parsed once, projected to the domain
// query the API also uses, and re-composed server-side for every tab and chip so
// the page filters with no JavaScript. An is:pr query on this index is not a
// filter; it redirects to /pulls with the same query so the issues index stays
// issue-implicit. See implementation/08 sections 2 and 3.
func (h *Handlers) Index(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	owner := ownerLogin(repo)

	q := ParseQuery(c.Query("q"))
	if q.Type == "pr" {
		// An is:pr query on the issues index is not a filter here, it is a request
		// for the pulls surface. F4 ships that surface, so the index hands the same
		// query string off to /pulls rather than rendering pull requests under the
		// issues shell. The redirect keeps a bookmarked is:pr URL working.
		return c.Redirect(http.StatusSeeOther, route.Pulls(owner, repo.Name, c.Request().URL.RawQuery))
	}

	page := pageParam(c.Query("page"))
	vc := h.viewer(c)

	dq := q.Filter(view.ViewerFrom(ctx))
	dq.Page = page
	dq.PerPage = indexPerPage

	rows, total, err := h.issues.ListIssues(ctx, vc.pk, owner, repo.Name, dq)
	if err != nil {
		return h.listError(c, err)
	}

	// The inactive tab needs its own total. A COUNT in the flipped state is the
	// cheapest way to get it, fetching no rows.
	openTotal, closedTotal := h.tabCounts(ctx, vc.pk, owner, repo.Name, q, total)

	vm := view.IssueIndexVM{
		Chrome:      h.chrome(c, repo.Name+" issues"),
		Header:      h.header(c.Context(), repo),
		Nav:         h.nav(repo),
		Repo:        repoRef(repo),
		QueryValue:  q.Raw,
		NewIssueURL: route.NewIssue(owner, repo.Name),
		OpenTab:     view.FilterTab{Label: "Open", Count: openTotal, URL: "?" + qHref(q.WithState("open")), IsActive: q.state() == "open"},
		ClosedTab:   view.FilterTab{Label: "Closed", Count: closedTotal, URL: "?" + qHref(q.WithState("closed")), IsActive: q.state() == "closed"},
		Rows:        h.rows(repo, rows),
		Pager:       h.pager(owner, repo.Name, q, page, total),
	}
	for _, name := range q.ActiveLabels() {
		vm.ActiveChips = append(vm.ActiveChips, view.LabelVM{
			Name: name,
			URL:  "?" + qHref(q.RemoveLabel(name)),
		})
	}
	if len(vm.Rows) == 0 {
		vm.Empty = true
		vm.EmptyReason = emptyReason(q)
	}
	return h.render.Page(c, "issues/index", vm)
}

// rows maps the listed issues into index rows.
func (h *Handlers) rows(repo *domain.Repo, issues []*domain.Issue) []view.IssueRow {
	owner := ownerLogin(repo)
	out := make([]view.IssueRow, 0, len(issues))
	for _, iss := range issues {
		row := view.IssueRow{
			Number:       iss.Number,
			Title:        iss.Title,
			URL:          route.Issue(owner, repo.Name, iss.Number),
			State:        stateBadge(iss),
			Author:       h.userChip(iss.User),
			OpenedAt:     iss.CreatedAt.UTC().Format("Jan 2, 2006"),
			OpenedISO:    iss.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			Labels:       labelChips(owner, repo.Name, iss.Labels),
			Assignees:    h.userChips(iss.Assignees),
			Milestone:    milestoneChip(owner, repo.Name, iss.Milestone),
			CommentCount: iss.CommentsCount,
		}
		out = append(out, row)
	}
	return out
}

// tabCounts returns the open and closed totals for the state tabs. The active
// state's total comes from the list query already run; the other is a COUNT in
// the flipped state, which fetches no rows (the old one-row probe still ran the
// page query under a LIMIT 1).
func (h *Handlers) tabCounts(ctx context.Context, viewerPK int64, owner, name string, q *Query, activeTotal int) (open, closed int) {
	other := "open"
	if q.state() == "open" {
		other = "closed"
	}
	probe := q.clone()
	probe.State = other
	pq := probe.Filter(view.ViewerFrom(ctx))
	otherTotal, err := h.issues.CountIssues(ctx, viewerPK, owner, name, pq)
	if err != nil {
		otherTotal = 0
	}
	if q.state() == "open" {
		return activeTotal, otherTotal
	}
	return otherTotal, activeTotal
}

// pager builds the prev/next links from the running total. A page beyond the last
// shows no next; the first page shows no prev.
func (h *Handlers) pager(owner, name string, q *Query, page, total int) view.Pager {
	p := view.Pager{Page: page}
	base := q.Encode()
	if page > 1 {
		p.PrevURL = route.Issues(owner, name, encodePage(base, page-1))
	}
	if page*indexPerPage < total {
		p.NextURL = route.Issues(owner, name, encodePage(base, page+1))
	}
	return p
}

// listError maps a domain list error to a response. A repo that vanished between
// the resolve and the list is a 404; anything else is a 500.
func (h *Handlers) listError(c *mizu.Ctx, err error) error {
	if isNotFound(err) {
		return h.notFound(c)
	}
	return err
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

// pageParam parses a 1-based page number, clamping a missing or malformed value
// to the first page.
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

// qHref turns a composed ?q=... string (which already starts with ?) into a bare
// query string the template prefixes with ? itself, keeping the composer output
// and the tab counts on one encoding.
func qHref(composed string) string {
	if len(composed) > 0 && composed[0] == '?' {
		return composed[1:]
	}
	return composed
}

// encodePage builds the raw query string for a q value plus a page number,
// omitting page=1 so the first page stays the clean URL.
func encodePage(q string, page int) string {
	vals := url.Values{"q": {q}}
	if page > 1 {
		vals.Set("page", strconv.Itoa(page))
	}
	return vals.Encode()
}

// emptyReason is the blankslate line for a filtered list with no results.
func emptyReason(q *Query) string {
	if q.state() == "closed" {
		return "No closed issues match this filter."
	}
	return "No open issues match this filter."
}

// isNotFound reports whether err is one of the domain not-found sentinels the
// handlers turn into a soft 404.
func isNotFound(err error) bool {
	return errors.Is(err, domain.ErrRepoNotFound) || errors.Is(err, domain.ErrIssueNotFound)
}

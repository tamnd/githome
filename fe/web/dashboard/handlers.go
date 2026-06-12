// Package dashboard holds the global, viewer-scoped lists at the reserved
// top-level /issues and /pulls names: the cross-repo "your issues" and "your
// pull requests" pages (spec doc 02 section 1.1, doc 09 section 1.6). Each is
// a session-gated view over the same domain search the /search page and the
// REST search run, scoped to the viewer by an author: or assignee: qualifier,
// so the dashboard and a hand-typed search can never disagree about what the
// viewer may see. An anonymous request bounces to the sign-in form with
// return_to carrying the dashboard, the 302 github.com answers. The tabs the
// search can back are Created and Assigned; the Mentioned and Review-requests
// tabs github.com adds need mention and review-request filters the domain
// search does not index yet, so they are absent rather than dead links.
package dashboard

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/presenter"
)

// perPage is how many rows one dashboard page lists, the same fixed window the
// search page uses.
const perPage = 25

// Deps are the dashboard handlers' dependencies: the domain search service
// every list runs through, the presenter for avatar URLs, the render set, the
// view builder for the shell chrome, and a logger.
type Deps struct {
	Search *domain.SearchService
	URLs   *presenter.URLBuilder
	Render *render.Set
	View   *view.Builder
	Logger *slog.Logger
}

// Handlers is the dashboard handler set. One is built at boot and shared; it
// holds no per-request state.
type Handlers struct {
	search *domain.SearchService
	urls   *presenter.URLBuilder
	render *render.Set
	view   *view.Builder
	log    *slog.Logger
}

// New wires the handler set from its dependencies.
func New(d Deps) *Handlers {
	return &Handlers{
		search: d.Search,
		urls:   d.URLs,
		render: d.Render,
		view:   d.View,
		log:    d.Logger,
	}
}

// Issues renders /issues, the cross-repo "your issues" page.
func (h *Handlers) Issues(c *mizu.Ctx) error {
	return h.dashboard(c, false)
}

// Pulls renders /pulls, the cross-repo "your pull requests" page.
func (h *Handlers) Pulls(c *mizu.Ctx) error {
	return h.dashboard(c, true)
}

// tab keys. The default is the created scope, like github.com.
const (
	tabCreated  = "created"
	tabAssigned = "assigned"
)

// dashboard runs the viewer-scoped search and renders the page. The viewer
// scope is a qualifier prepended to the raw query, so anything the viewer
// types in the filter box (label:, repo:, is:closed) narrows within their
// slice the same way it would on /search.
func (h *Handlers) dashboard(c *mizu.Ctx, isPull bool) error {
	ctx := c.Context()
	viewer := view.ViewerFrom(ctx)
	if viewer == nil {
		return c.Redirect(http.StatusFound, route.LoginWithReturn(c.Request().URL.RequestURI()))
	}
	viewerPK := webmw.ViewerID(ctx)

	q := c.Query("q")
	tab := c.Query("tab")
	if tab != tabAssigned {
		tab = tabCreated
	}
	page := pageParam(c.Query("page"))

	hits, total, err := h.search.SearchIssues(ctx, viewerPK, rawQuery(isPull, tab, viewer.Login, q), "", "", page, perPage)
	if err != nil {
		return err
	}

	vm := view.DashboardVM{
		QueryValue: q,
		Tab:        tab,
		Rows:       h.rows(hits, isPull),
	}
	if isPull {
		vm.Chrome = h.view.Chrome(c, "Your pull requests")
		vm.Heading, vm.Icon = "Pull requests", "git-pull-request"
		vm.Action = route.DashboardPulls("")
	} else {
		vm.Chrome = h.view.Chrome(c, "Your issues")
		vm.Heading, vm.Icon = "Issues", "issue-opened"
		vm.Action = route.DashboardIssues("")
	}
	vm.Tabs = h.tabs(ctx, viewerPK, viewer.Login, isPull, tab, q, total)
	vm.Pager = h.pager(isPull, tab, q, page, total)
	if total == 0 {
		vm.Empty = true
		vm.EmptyReason = emptyReason(isPull, tab)
	}
	return h.render.Page(c, "dashboard/page", vm)
}

// rawQuery builds the search string for a tab: the type, the viewer scope, and
// whatever extra query the viewer typed.
func rawQuery(isPull bool, tab, login, q string) string {
	raw := "is:issue "
	if isPull {
		raw = "is:pr "
	}
	if tab == tabAssigned {
		raw += "assignee:" + login
	} else {
		raw += "author:" + login
	}
	if q != "" {
		raw += " " + q
	}
	return raw
}

// tabs builds the Created and Assigned tabs with their counts: the active
// tab's count is the total already in hand, the inactive one comes from a
// one-row probe, and a probe error degrades to no count rather than failing
// the page.
func (h *Handlers) tabs(ctx context.Context, viewerPK int64, login string, isPull bool, active, q string, activeTotal int) []view.FilterTab {
	out := make([]view.FilterTab, 0, 2)
	for _, t := range []struct{ key, label string }{
		{tabCreated, "Created"},
		{tabAssigned, "Assigned"},
	} {
		tab := view.FilterTab{
			Label:    t.label,
			URL:      h.pageURL(isPull, t.key, q, 1),
			IsActive: t.key == active,
		}
		if t.key == active {
			tab.Count = activeTotal
		} else if _, n, err := h.search.SearchIssues(ctx, viewerPK, rawQuery(isPull, t.key, login, q), "", "", 1, 1); err == nil {
			tab.Count = n
		}
		out = append(out, tab)
	}
	return out
}

// pager builds the prev/next links from the running total, keeping the tab and
// the query intact.
func (h *Handlers) pager(isPull bool, tab, q string, page, total int) view.Pager {
	p := view.Pager{Page: page}
	if page > 1 {
		p.PrevURL = h.pageURL(isPull, tab, q, page-1)
	}
	if page*perPage < total {
		p.NextURL = h.pageURL(isPull, tab, q, page+1)
	}
	return p
}

// pageURL composes a dashboard URL carrying the tab, the extra query, and the
// page, dropping each default so the canonical first page is the bare path.
func (h *Handlers) pageURL(isPull bool, tab, q string, page int) string {
	vals := url.Values{}
	if tab != tabCreated {
		vals.Set("tab", tab)
	}
	if q != "" {
		vals.Set("q", q)
	}
	if page > 1 {
		vals.Set("page", strconv.Itoa(page))
	}
	if isPull {
		return route.DashboardPulls(vals.Encode())
	}
	return route.DashboardIssues(vals.Encode())
}

// pageParam parses a 1-based page number, clamping a missing or malformed
// value to the first page.
func pageParam(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// rows maps the matched issues or pull requests into the cross-repo result
// rows the search page also renders.
func (h *Handlers) rows(hits []domain.IssueHit, isPull bool) []view.IssueResultVM {
	out := make([]view.IssueResultVM, 0, len(hits))
	for _, hit := range hits {
		owner := ownerLogin(hit.Repo)
		iss := hit.Issue
		row := view.IssueResultVM{
			Number:       iss.Number,
			Title:        iss.Title,
			State:        stateBadge(iss, isPull),
			Author:       h.userChip(iss.User),
			OpenedAt:     iss.CreatedAt.UTC().Format("Jan 2, 2006"),
			OpenedISO:    iss.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			CommentCount: iss.CommentsCount,
			RepoFullName: owner + "/" + hit.Repo.Name,
			RepoURL:      route.Repo(owner, hit.Repo.Name),
		}
		if isPull {
			row.URL = route.Pull(owner, hit.Repo.Name, iss.Number)
		} else {
			row.URL = route.Issue(owner, hit.Repo.Name, iss.Number)
		}
		out = append(out, row)
	}
	return out
}

// ownerLogin returns the repo owner's login, tolerating a repo assembled
// without its owner.
func ownerLogin(r *domain.Repo) string {
	if r != nil && r.Owner != nil {
		return r.Owner.Login
	}
	return ""
}

// userChip maps a domain user into the small chip the rows show. A nil user (a
// ghost author whose account is gone) yields a neutral chip with no profile
// link rather than a broken one.
func (h *Handlers) userChip(u *domain.User) view.UserChipVM {
	if u == nil {
		return view.UserChipVM{Login: "ghost"}
	}
	return view.UserChipVM{
		Login:     u.Login,
		AvatarURL: h.urls.HTML("avatars", "u", strconv.FormatInt(u.ID, 10)),
		URL:       "/" + u.Login,
	}
}

// stateBadge maps a matched issue or pull request into its state badge, the
// same mapping the search results use: a merged pull wears the merge glyph, a
// not-planned issue the skip glyph.
func stateBadge(iss *domain.Issue, isPull bool) view.IssueStateVM {
	reason := ""
	if iss.StateReason != nil {
		reason = *iss.StateReason
	}
	if isPull {
		switch {
		case reason == "merged":
			return view.IssueStateVM{State: "closed", Reason: "merged", Label: "Merged", Icon: "git-merge", Modifier: "merged"}
		case iss.State == "closed":
			return view.IssueStateVM{State: "closed", Reason: reason, Label: "Closed", Icon: "git-pull-request-closed", Modifier: "closed"}
		default:
			return view.IssueStateVM{State: "open", Reason: reason, Label: "Open", Icon: "git-pull-request", Modifier: "open"}
		}
	}
	if iss.State == "closed" {
		if reason == "not_planned" {
			return view.IssueStateVM{State: "closed", Reason: reason, Label: "Closed", Icon: "skip", Modifier: "not-planned"}
		}
		return view.IssueStateVM{State: "closed", Reason: reason, Label: "Closed", Icon: "issue-closed", Modifier: "closed"}
	}
	return view.IssueStateVM{State: "open", Reason: reason, Label: "Open", Icon: "issue-opened", Modifier: "open"}
}

// emptyReason is the blankslate line for an empty scoped list.
func emptyReason(isPull bool, tab string) string {
	kind := "issues"
	if isPull {
		kind = "pull requests"
	}
	if tab == tabAssigned {
		return "No " + kind + " assigned to you matched."
	}
	return "No " + kind + " you created matched."
}

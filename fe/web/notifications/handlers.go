// Package notifications holds the viewer-scoped notifications inbox at the
// reserved top-level /notifications name (spec doc 02 section 1.1). The inbox
// is backed by the notifications domain layer: it lists the viewer's threads,
// newest first, with an Inbox (unread) and an All filter, both served by the
// same domain query the REST /notifications endpoint runs, so the page and the
// API never disagree about what the viewer is subscribed to. An anonymous
// request bounces to the sign-in form with return_to carrying the inbox, the
// 302 github.com answers. When the notifications service is not wired the
// route still exists and renders the empty-inbox blankslate.
package notifications

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
)

// perPage is how many threads one inbox page lists.
const perPage = 25

// Deps are the inbox handler's dependencies: the notifications domain service
// every list runs through, the repo service that resolves each thread's
// repository for its link and full name, the render set, the view builder for
// the shell chrome, and a logger.
type Deps struct {
	Notifications *domain.NotificationService
	Repos         *domain.RepoService
	Render        *render.Set
	View          *view.Builder
	Logger        *slog.Logger
}

// Handlers is the inbox handler set. One is built at boot and shared; it holds
// no per-request state.
type Handlers struct {
	notifications *domain.NotificationService
	repos         *domain.RepoService
	render        *render.Set
	view          *view.Builder
	log           *slog.Logger
}

// New wires the handler set from its dependencies.
func New(d Deps) *Handlers {
	return &Handlers{
		notifications: d.Notifications,
		repos:         d.Repos,
		render:        d.Render,
		view:          d.View,
		log:           d.Logger,
	}
}

// Index renders /notifications, the viewer's inbox. An anonymous request
// bounces to the sign-in form carrying the inbox as return_to.
func (h *Handlers) Index(c *mizu.Ctx) error {
	ctx := c.Context()
	viewer := view.ViewerFrom(ctx)
	if viewer == nil {
		return c.Redirect(http.StatusFound, route.LoginWithReturn(c.Request().URL.RequestURI()))
	}
	viewerPK := webmw.ViewerID(ctx)

	all := c.Query("all") == "true"
	page := pageParam(c.Query("page"))

	threads, total, err := h.notifications.List(ctx, viewerPK, 0, all, page, perPage)
	if err != nil {
		return err
	}

	vm := view.NotificationsVM{
		Chrome:  h.view.Chrome(c, "Notifications"),
		Filters: h.filters(all),
		Threads: h.rows(ctx, threads),
		Pager:   h.pager(all, page, total),
	}
	if total == 0 {
		vm.Empty = true
		vm.EmptyAll = all
	}
	return h.render.Page(c, "notifications/index", vm)
}

// filters builds the left-rail links. Inbox lists the unread threads; All adds
// the ones already read. github.com's Saved and Participating rails need
// filters the domain layer does not index yet, so they are absent rather than
// dead links.
func (h *Handlers) filters(all bool) []view.NotificationFilterVM {
	return []view.NotificationFilterVM{
		{Label: "Inbox", URL: route.Notifications(""), Current: !all},
		{Label: "All", URL: route.Notifications("all=true"), Current: all},
	}
}

// rows maps the threads into inbox rows, resolving each thread's repository for
// its full name and link. The repos are cached across the page so a busy
// repository is resolved once. A thread whose repository no longer resolves
// keeps its subject but drops the repository chip rather than failing the page.
func (h *Handlers) rows(ctx context.Context, threads []*domain.NotificationThread) []view.NotificationRowVM {
	repos := make(map[int64]*domain.Repo)
	out := make([]view.NotificationRowVM, 0, len(threads))
	for _, t := range threads {
		repo := repos[t.RepoPK]
		if repo == nil {
			if r, err := h.repos.RepoForEvent(ctx, t.RepoPK); err == nil {
				repo = r
				repos[t.RepoPK] = r
			}
		}
		row := view.NotificationRowVM{
			Title:      t.SubjectTitle,
			Reason:     humanizeReason(t.Reason),
			Unread:     t.Unread,
			IsPull:     t.SubjectIsPull,
			UpdatedAt:  t.UpdatedAt.UTC().Format("Jan 2, 2006"),
			UpdatedISO: t.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		}
		if repo != nil {
			owner := ownerLogin(repo)
			row.RepoFullName = owner + "/" + repo.Name
			row.RepoURL = route.Repo(owner, repo.Name)
			if t.SubjectIsPull {
				row.URL = route.Pull(owner, repo.Name, t.SubjectNumber)
			} else {
				row.URL = route.Issue(owner, repo.Name, t.SubjectNumber)
			}
		}
		out = append(out, row)
	}
	return out
}

// pager builds the prev/next links from the running total, keeping the active
// filter intact.
func (h *Handlers) pager(all bool, page, total int) view.Pager {
	p := view.Pager{Page: page}
	if page > 1 {
		p.PrevURL = pageURL(all, page-1)
	}
	if page*perPage < total {
		p.NextURL = pageURL(all, page+1)
	}
	return p
}

// pageURL composes an inbox URL carrying the filter and the page, dropping each
// default so the canonical first page of the inbox is the bare path.
func pageURL(all bool, page int) string {
	vals := url.Values{}
	if all {
		vals.Set("all", "true")
	}
	if page > 1 {
		vals.Set("page", strconv.Itoa(page))
	}
	return route.Notifications(vals.Encode())
}

// pageParam parses a 1-based page number, clamping a missing or malformed value
// to the first page.
func pageParam(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// humanizeReason maps a domain subscription reason to the short label
// github.com shows on the thread, falling back to the raw reason for one the
// fan-out adds later that this map does not yet name.
func humanizeReason(reason string) string {
	switch reason {
	case "author":
		return "you authored"
	case "assign":
		return "assigned"
	case "mention":
		return "mentioned"
	case "comment":
		return "commented"
	case "review_requested":
		return "review requested"
	case "subscribed":
		return "subscribed"
	case "":
		return ""
	default:
		return reason
	}
}

// ownerLogin returns the repo owner's login, tolerating a repo assembled
// without its owner.
func ownerLogin(r *domain.Repo) string {
	if r != nil && r.Owner != nil {
		return r.Owner.Login
	}
	return ""
}

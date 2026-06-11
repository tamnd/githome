// Package home holds the Githome web front's landing surface: the root page at /
// and its named twin at /dashboard. The root switches on the viewer the session
// middleware resolved: a signed-in viewer gets the dashboard (their repositories
// and their recent activity), an anonymous one the sign-in landing. /dashboard is
// the same dashboard at a stable URL, so a bookmark survives being signed out:
// an anonymous request there bounces to the sign-in form with return_to, the
// settings rule, because the page is function-private rather than secret. Both
// routes render the same template from the same model, so the two URLs can never
// drift apart. The feed reads the viewer's own event timeline through the same
// catalog the profile timeline uses (fe/web/profile.FeedItems); Githome has no
// follow graph, so "your feed" is honestly your own recent activity, not a faked
// network feed. See Spec 2005 docs 02 and 04.
package home

import (
	"log/slog"
	"net/http"
	"sort"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	webprofile "github.com/tamnd/githome/fe/web/profile"
	"github.com/tamnd/githome/fe/webmw"
)

// homeRepoLimit caps the dashboard sidebar; the "show all" link carries the rest
// to the profile repositories tab. homeFeedLimit is the activity page size, the
// same first page the profile overview shows.
const (
	homeRepoLimit = 12
	homeFeedLimit = 30
)

// Deps are the home handlers' dependencies: the repo service for the sidebar
// list, the event service for the activity feed, the render set, the view
// builder for the chrome, and a logger. Either service may be nil; the dashboard
// then renders without that panel, the same degrade the profile takes.
type Deps struct {
	Repos  *domain.RepoService
	Events *domain.EventService
	Render *render.Set
	View   *view.Builder
	Logger *slog.Logger
}

// Handlers is the home handler set. One is built at boot and shared; it holds no
// per-request state.
type Handlers struct {
	repos  *domain.RepoService
	events *domain.EventService
	render *render.Set
	view   *view.Builder
	log    *slog.Logger
}

// New wires the handler set from its dependencies.
func New(d Deps) *Handlers {
	return &Handlers{
		repos:  d.Repos,
		events: d.Events,
		render: d.Render,
		view:   d.View,
		log:    d.Logger,
	}
}

// Index serves /: the dashboard for a signed-in viewer, the sign-in landing for
// an anonymous one.
func (h *Handlers) Index(c *mizu.Ctx) error {
	return h.page(c)
}

// Dashboard serves /dashboard, the dashboard's stable URL. An anonymous request
// bounces to the sign-in form with return_to carrying the page, so a signed-out
// bookmark lands back here after the sign-in.
func (h *Handlers) Dashboard(c *mizu.Ctx) error {
	if view.ViewerFrom(c.Context()) == nil {
		return c.Redirect(http.StatusFound, route.LoginWithReturn(c.Request().URL.RequestURI()))
	}
	return h.page(c)
}

// page renders home/index: the shell from the view builder, plus the dashboard
// panels when a viewer is signed in.
func (h *Handlers) page(c *mizu.Ctx) error {
	vm := h.view.Home(c)
	if v := view.ViewerFrom(c.Context()); v != nil {
		if err := h.fillDashboard(c, v, &vm); err != nil {
			return err
		}
	}
	return h.render.Page(c, "home/index", vm)
}

// fillDashboard loads the signed-in panels: the viewer's repositories, newest
// activity first and capped for the sidebar, and their recent activity mapped
// through the shared profile feed catalog. The viewer reads their own timeline,
// so private activity shows here the way it does on their own profile.
func (h *Handlers) fillDashboard(c *mizu.Ctx, v *view.Viewer, vm *view.HomeVM) error {
	ctx := c.Context()
	viewerPK := webmw.ViewerID(ctx)

	vm.NewRepoURL = route.NewRepo()
	vm.ReposURL = route.ProfileTab(v.Login, view.ProfileRepositories)

	if h.repos != nil {
		repos, err := h.repos.ListReposByLogin(ctx, viewerPK, v.Login)
		if err != nil {
			return err
		}
		sort.SliceStable(repos, func(i, j int) bool {
			return repos[i].UpdatedAt.After(repos[j].UpdatedAt)
		})
		if len(repos) > homeRepoLimit {
			repos = repos[:homeRepoLimit]
		}
		vm.Repos = make([]view.HomeRepoVM, 0, len(repos))
		for _, r := range repos {
			vm.Repos = append(vm.Repos, view.HomeRepoVM{
				FullName: r.FullName(),
				URL:      route.Repo(v.Login, r.Name),
				Private:  r.Private,
			})
		}
	}

	if h.events != nil {
		events, err := h.events.UserFeed(ctx, viewerPK, v.Login, homeFeedLimit)
		if err != nil {
			return err
		}
		vm.Feed = webprofile.FeedItems(events)
	}
	vm.FeedEmpty = len(vm.Feed) == 0
	return nil
}

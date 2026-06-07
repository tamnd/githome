// Package fe mounts the Githome web front: the human-facing, server-rendered HTML
// surface that sits beside the REST and GraphQL APIs in the same binary. It owns
// no domain logic. It resolves data through the domain services, builds view
// models with fe/view, and renders them with fe/render; its middleware live in
// fe/webmw and its URL rules in fe/route. The front never calls the public API
// over HTTP: it shares the process and the domain layer directly. See
// implementation/00 and implementation/02.
package fe

import (
	"log/slog"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
	webrepo "github.com/tamnd/githome/fe/web/repo"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/presenter"
)

// Deps are the web front's dependencies. F0 needs the render set, the view
// builder, and the three stateful middleware (session, CSRF, flash) plus a
// logger. F1 adds the domain repo service and the presenter URL builder its
// code-browsing handlers read; a zero service leaves its routes unmounted,
// mirroring how the REST surface mounts.
type Deps struct {
	Render   *render.Set
	View     *view.Builder
	Repos    *domain.RepoService
	URLs     *presenter.URLBuilder
	Sessions *webmw.Sessions
	CSRF     *webmw.CSRF
	Flash    *webmw.Flash
	Logger   *slog.Logger
}

// Mount registers the web front on root. It does not touch the global middleware
// or the error handler the API surface installed: it registers its routes through
// scoped subrouters, so the web middleware chain applies to web routes only and
// the API keeps its own. The page chain carries recovery, the session, the color
// mode, the CSRF guard and the flash reader; the asset chain carries only
// recovery, so a static file does not pay for a session lookup.
func Mount(root *mizu.Router, d Deps) {
	page := root.With(
		webmw.Recover(d.Render, d.Logger),
		d.Sessions.Middleware(),
		webmw.ColorMode(),
		d.CSRF.Middleware(),
		d.Flash.Middleware(),
	)
	page.Get("/{$}", handleHome(d))

	mountRepo(page, d)

	asset := root.With(webmw.Recover(d.Render, d.Logger))
	asset.Get(render.AssetURLPrefix+"{file...}", d.Render.AssetHandler())
}

// mountRepo registers the code-browsing routes under /{owner}/{repo}. Every route
// runs the Resolve middleware first, which loads the repository read-gated for the
// viewer (or a 404), so a handler only decides whether a ref, path, or object
// inside a visible repo exists. The repo service is the gate: with no service the
// routes stay unmounted, the same as the REST surface. The greedy {rest} routes
// carry a ref and an optional path the handler splits with the repo's ref set. See
// implementation/07.
func mountRepo(page *mizu.Router, d Deps) {
	if d.Repos == nil {
		return
	}
	rh := webrepo.New(webrepo.Deps{
		Repos:  d.Repos,
		URLs:   d.URLs,
		Render: d.Render,
		View:   d.View,
		Logger: d.Logger,
	})
	rg := page.With(rh.Resolve)
	rg.Get("/{owner}/{repo}", rh.Home)
	rg.Get("/{owner}/{repo}/tree/{rest...}", rh.Tree)
	rg.Get("/{owner}/{repo}/blob/{rest...}", rh.Blob)
	rg.Get("/{owner}/{repo}/raw/{rest...}", rh.Raw)
	rg.Get("/{owner}/{repo}/commits", rh.Commits)
	rg.Get("/{owner}/{repo}/commits/{rest...}", rh.Commits)
	rg.Get("/{owner}/{repo}/branches", rh.Branches)
	rg.Get("/{owner}/{repo}/tags", rh.Tags)
	rg.Get("/{owner}/{repo}/find/{rest...}", rh.FileFinder)
}

// handleHome renders the landing page. A signed-in viewer sees the dashboard
// shell, an anonymous viewer the sign-in blankslate; the difference is driven by
// the viewer the session middleware resolved, so the same handler serves both.
func handleHome(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		return d.Render.Page(c, "home/index", d.View.Home(c))
	}
}

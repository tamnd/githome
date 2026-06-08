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
	webissues "github.com/tamnd/githome/fe/web/issues"
	webpulls "github.com/tamnd/githome/fe/web/pulls"
	webrepo "github.com/tamnd/githome/fe/web/repo"
	websearch "github.com/tamnd/githome/fe/web/search"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/markup"
	"github.com/tamnd/githome/presenter"
)

// Deps are the web front's dependencies. F0 needs the render set, the view
// builder, and the three stateful middleware (session, CSRF, flash) plus a
// logger. F1 adds the domain repo service and the presenter URL builder its
// code-browsing handlers read; a zero service leaves its routes unmounted,
// mirroring how the REST surface mounts. F2 adds the shared markup renderer the
// README and Markdown blob views render through; a nil renderer falls back to
// the escaped-source view, so the front still serves with markup unconfigured.
type Deps struct {
	Render   *render.Set
	View     *view.Builder
	Repos    *domain.RepoService
	Issues   *domain.IssueService
	Pulls    *domain.PRService
	Reviews  *domain.ReviewService
	Search   *domain.SearchService
	URLs     *presenter.URLBuilder
	Markup   *markup.Renderer
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
	mountIssues(page, d)
	mountPulls(page, d)
	mountSearch(page, d)

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
		Markup: d.Markup,
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

// mountIssues registers the issues routes under /{owner}/{repo}/issues. Like the
// code-browsing routes every route runs the issues Resolve middleware first,
// which loads the repository read-gated for the viewer (or a 404), so a handler
// only decides whether an issue inside a visible repo exists. The issue service
// is the gate: with no service the routes stay unmounted. The literal /issues/new
// route is registered before the /issues/{number} route so "new" is never read as
// a number. The mutation routes (create, comment, state, title, edit, reactions)
// arrive with the write handlers. See implementation/08.
func mountIssues(page *mizu.Router, d Deps) {
	if d.Issues == nil || d.Repos == nil {
		return
	}
	ih := webissues.New(webissues.Deps{
		Issues: d.Issues,
		Repos:  d.Repos,
		URLs:   d.URLs,
		Render: d.Render,
		View:   d.View,
		Markup: d.Markup,
		Logger: d.Logger,
	})
	ig := page.With(ih.Resolve)
	ig.Get("/{owner}/{repo}/issues", ih.Index)
	ig.Get("/{owner}/{repo}/issues/new", ih.New)
	ig.Get("/{owner}/{repo}/issues/{number}", ih.Show)

	// The mutations all post and redirect, so a reload re-fetches with GET. The
	// service re-authorizes every write, so an anonymous or read-only viewer who
	// forges a POST gets the themed 403, not a silent success. The literal /new
	// create route is registered before the {number} mutation routes for the same
	// reason the GET routes are ordered: "new" is never read as a number.
	ig.Post("/{owner}/{repo}/issues", ih.Create)
	ig.Post("/{owner}/{repo}/issues/{number}/comments", ih.CreateComment)
	ig.Post("/{owner}/{repo}/issues/{number}/state", ih.ToggleState)
	ig.Post("/{owner}/{repo}/issues/{number}/title", ih.EditTitle)
	ig.Post("/{owner}/{repo}/issues/{number}/edit", ih.EditSidebar)
	ig.Post("/{owner}/{repo}/issues/{number}/reactions", ih.ToggleIssueReaction)
	ig.Post("/{owner}/{repo}/issues/{number}/comments/{comment}", ih.EditComment)
	ig.Post("/{owner}/{repo}/issues/{number}/comments/{comment}/delete", ih.DeleteComment)
	ig.Post("/{owner}/{repo}/issues/{number}/comments/{comment}/reactions", ih.ToggleCommentReaction)
}

// mountPulls registers the pull-request routes under /{owner}/{repo}/pulls (the
// plural index) and /{owner}/{repo}/pull/{number} (the singular detail, matching
// the github.com URL split). Like the issues routes every route runs the pulls
// Resolve middleware first, which loads the repository read-gated for the viewer
// (or a 404), so a handler only decides whether a pull request inside a visible
// repo exists. The PR service is the gate, and the issue service drives the shared
// Conversation timeline; with either missing the routes stay unmounted. The
// literal /partials/merge-box GET is registered under the {number} prefix; it
// cannot collide with a tab name because the tabs are distinct literals. The
// mutations all post and redirect, and the service re-authorizes every write, so a
// forged POST from a read-only viewer gets the themed 403, not a silent success.
// See implementation/09.
func mountPulls(page *mizu.Router, d Deps) {
	if d.Pulls == nil || d.Issues == nil || d.Repos == nil {
		return
	}
	ph := webpulls.New(webpulls.Deps{
		Pulls:   d.Pulls,
		Issues:  d.Issues,
		Reviews: d.Reviews,
		Repos:   d.Repos,
		URLs:    d.URLs,
		Render:  d.Render,
		View:    d.View,
		Markup:  d.Markup,
		Logger:  d.Logger,
	})
	pg := page.With(ph.Resolve)
	pg.Get("/{owner}/{repo}/pulls", ph.Index)
	pg.Get("/{owner}/{repo}/pull/{number}", ph.Conversation)
	pg.Get("/{owner}/{repo}/pull/{number}/commits", ph.Commits)
	pg.Get("/{owner}/{repo}/pull/{number}/files", ph.Files)
	pg.Get("/{owner}/{repo}/pull/{number}/partials/merge-box", ph.MergeBox)

	pg.Post("/{owner}/{repo}/pull/{number}/comments", ph.CreateComment)
	pg.Post("/{owner}/{repo}/pull/{number}/state", ph.ToggleState)
	pg.Post("/{owner}/{repo}/pull/{number}/merge", ph.Merge)

	// The code-review mutations (F5). Each posts and redirects, and the review
	// service re-authorizes every write, so a forged POST from a read-only viewer
	// gets the soft 404 or the themed 403, never a silent success. The reply and
	// resolve routes carry the thread's root comment id in the path.
	pg.Post("/{owner}/{repo}/pull/{number}/review-comments", ph.CreateReviewComment)
	pg.Post("/{owner}/{repo}/pull/{number}/review-comments/{comment}/replies", ph.ReplyReviewComment)
	pg.Post("/{owner}/{repo}/pull/{number}/review-threads/{root}/resolve", ph.ToggleReviewThread)
	pg.Post("/{owner}/{repo}/pull/{number}/reviews", ph.SubmitReview)
}

// mountSearch registers the search surface: the global /search page in the static
// band and the in-repo /{owner}/{repo}/search page behind the search Resolve
// middleware, which loads the repository read-gated for the viewer (or a 404) so
// the scoped search never confirms a private repo's existence. The search service
// is the gate, and the repo service backs the scoped resolve; with either missing
// the routes stay unmounted, the same as the other surfaces. The /search literal
// is a reserved top-level name (fe/route), so it can never be read as a /{owner}
// profile. See implementation/12 section 2.
func mountSearch(page *mizu.Router, d Deps) {
	if d.Search == nil || d.Repos == nil {
		return
	}
	sh := websearch.New(websearch.Deps{
		Search: d.Search,
		Repos:  d.Repos,
		URLs:   d.URLs,
		Render: d.Render,
		View:   d.View,
		Logger: d.Logger,
	})
	page.Get("/search", sh.Global)

	sg := page.With(sh.Resolve)
	sg.Get("/{owner}/{repo}/search", sh.Scoped)
}

// handleHome renders the landing page. A signed-in viewer sees the dashboard
// shell, an anonymous viewer the sign-in blankslate; the difference is driven by
// the viewer the session middleware resolved, so the same handler serves both.
func handleHome(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		return d.Render.Page(c, "home/index", d.View.Home(c))
	}
}

// Package fe mounts the Githome web front: the human-facing, server-rendered HTML
// surface that sits beside the REST and GraphQL APIs in the same binary. It owns
// no domain logic. It resolves data through the domain services, builds view
// models with fe/view, and renders them with fe/render; its middleware live in
// fe/webmw and its URL rules in fe/route. The front never calls the public API
// over HTTP: it shares the process and the domain layer directly. See
// implementation/00 and implementation/02.
package fe

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	webauth "github.com/tamnd/githome/fe/web/auth"
	webchecks "github.com/tamnd/githome/fe/web/checks"
	webcompare "github.com/tamnd/githome/fe/web/compare"
	webissues "github.com/tamnd/githome/fe/web/issues"
	webprofile "github.com/tamnd/githome/fe/web/profile"
	webpulls "github.com/tamnd/githome/fe/web/pulls"
	webrepo "github.com/tamnd/githome/fe/web/repo"
	webreposettings "github.com/tamnd/githome/fe/web/reposettings"
	websearch "github.com/tamnd/githome/fe/web/search"
	websettings "github.com/tamnd/githome/fe/web/settings"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/markup"
	"github.com/tamnd/githome/presenter"
)

// AuthPwStore is the narrow password-auth interface the auth handlers need.
// The concrete *store.Store satisfies it; cmd/githome passes the store directly
// since it already imports store. fe/mount never imports store directly (doc 01 §6).
type AuthPwStore interface {
	PasswordHashFor(ctx context.Context, login string) (pk int64, hash string, err error)
	InsertUserWithPassword(ctx context.Context, login, email, hash string) (int64, error)
	UserLoginExists(ctx context.Context, login string) (bool, error)
}

// Deps are the web front's dependencies. F0 needs the render set, the view
// builder, and the three stateful middleware (session, CSRF, flash) plus a
// logger. F1 adds the domain repo service and the presenter URL builder its
// code-browsing handlers read; a zero service leaves its routes unmounted,
// mirroring how the REST surface mounts. F2 adds the shared markup renderer the
// README and Markdown blob views render through; a nil renderer falls back to
// the escaped-source view, so the front still serves with markup unconfigured.
// Auth (added F1) is the password store the sign-in/join routes need; nil leaves
// those routes unmounted. HomeHandler overrides the default landing page; browse
// mode uses it to redirect straight to the repository root.
type Deps struct {
	Render      *render.Set
	View        *view.Builder
	Auth        AuthPwStore
	OAuthSvc    webauth.OAuthService
	Tokens      websettings.TokenService
	Repos       *domain.RepoService
	Hooks       *domain.HookService
	Checks      *domain.ChecksService
	Issues      *domain.IssueService
	Pulls       *domain.PRService
	Reviews     *domain.ReviewService
	Search      *domain.SearchService
	Users       *domain.UserService
	Events      *domain.EventService
	URLs        *presenter.URLBuilder
	Markup      *markup.Renderer
	Sessions    *webmw.Sessions
	CSRF        *webmw.CSRF
	Flash       *webmw.Flash
	Logger      *slog.Logger
	HomeHandler mizu.Handler // optional: overrides the default landing page
}

// Mount registers the web front's dynamic routes on root and returns the
// servable handler: the asset tree peeled off in front of root. It does not touch
// the global middleware or the error handler the API surface installed: it
// registers its routes through scoped subrouters, so the web middleware chain
// applies to web routes only and the API keeps its own. The page chain carries
// recovery, the session, the color mode, the CSRF guard and the flash reader; the
// asset chain carries only recovery, so a static file does not pay for a session
// lookup.
//
// The returned handler, not root, is what the server must serve when the web
// front is enabled. The static asset tree cannot share root's net/http mux: it is
// served under a greedy /assets/{file...} pattern, and that overlaps the front's
// own /{owner}/{repo}/... wildcards (an owner could be named "assets") in a way
// the Go 1.22 mux refuses to register. So assets live on their own router and are
// dispatched ahead of root by path prefix. See implementation/02.
func Mount(root *mizu.Router, d Deps) http.Handler {
	page := root.With(
		webmw.Recover(d.Render, d.Logger),
		webmw.SecureHeaders(),
		d.Sessions.Middleware(),
		webmw.ColorMode(),
		d.CSRF.Middleware(),
		d.Flash.Middleware(),
	)
	if d.HomeHandler != nil {
		page.Get("/{$}", d.HomeHandler)
	} else {
		page.Get("/{$}", handleHome(d))
	}

	mountAuth(page, d)
	mountRepo(page, d)
	mountChecks(page, d)
	mountCompare(page, d)
	mountIssues(page, d)
	mountPulls(page, d)
	mountSearch(page, d)
	mountNotifications(page, d)
	mountRepoSettings(page, d)
	mountSettings(page, d)
	mountProfile(page, d)

	// The catch-all owns every URL nothing above claimed. "GET /" is the least
	// specific pattern on the mux, so each mounted route still wins, and a GET
	// pattern also answers HEAD. It runs the full page chain, so the 404 it
	// renders carries the viewer's chrome and theme like any other page.
	page.Get("/", handleNotFound(d))

	assets := mizu.NewRouter()
	assets.With(webmw.Recover(d.Render, d.Logger)).
		Get(render.AssetURLPrefix+"{file...}", d.Render.AssetHandler())

	// Compression wraps the whole servable surface (pages, assets, and the API
	// routes sharing root), negotiated per request and applied per response
	// content type, so HTML/CSS/JS/SVG/JSON ship gzipped while images,
	// archives, and git packs pass through untouched.
	return webmw.Gzip(themedMethodNotAllowed(assetDispatch(assets, root), d.Render))
}

// assetDispatch serves the hashed asset tree under render.AssetURLPrefix from
// assets and sends every other request to app. It joins the two routers by a path
// prefix rather than registering both on one mux, which the overlapping
// asset/owner-space patterns would make the Go 1.22 mux reject. The prefix test is
// the same AssetURLPrefix the manifest emits, so a hashed URL the page renders
// lands on the asset router and everything else falls through to the app.
func assetDispatch(assets, app http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, render.AssetURLPrefix) {
			assets.ServeHTTP(w, r)
			return
		}
		app.ServeHTTP(w, r)
	})
}

// themedMethodNotAllowed dresses the mux's 405 in the themed error page. The
// mux knows every registered pattern, so it is the only place the Allow header
// is always right; rather than recompute that, the wrapper lets the mux answer
// and intercepts only the plain-text body it writes, then renders the HTML 405
// over the same header map, Allow included (spec §7.4). A 405 can come from no
// other writer on this surface: the front's handlers never return one, and the
// API namespace, whose clients want machine-shaped errors, is passed through
// untouched.
func themedMethodNotAllowed(app http.Handler, rs *render.Set) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			app.ServeHTTP(w, r)
			return
		}
		iw := &methodNotAllowedInterceptor{ResponseWriter: w}
		app.ServeHTTP(iw, r)
		if iw.intercepted {
			c := mizu.NewCtx(w, r, nil)
			_ = rs.MethodNotAllowed(c)
		}
	})
}

// methodNotAllowedInterceptor suppresses a response only when its first
// WriteHeader is the mux's 405, leaving every other response byte-for-byte
// alone. Unwrap keeps http.ResponseController able to reach the real writer's
// Flusher and friends through the wrapper, and Flush passes through directly
// for writers that get type-asserted instead.
type methodNotAllowedInterceptor struct {
	http.ResponseWriter
	wrote       bool
	intercepted bool
}

func (w *methodNotAllowedInterceptor) WriteHeader(code int) {
	if !w.wrote {
		w.wrote = true
		if code == http.StatusMethodNotAllowed {
			w.intercepted = true
			return
		}
	}
	if !w.intercepted {
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *methodNotAllowedInterceptor) Write(b []byte) (int, error) {
	w.wrote = true
	if w.intercepted {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

func (w *methodNotAllowedInterceptor) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *methodNotAllowedInterceptor) Flush() {
	if w.intercepted {
		return
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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
	rg.Get("/{owner}/{repo}/blame/{rest...}", rh.Blame)
	rg.Get("/{owner}/{repo}/raw/{rest...}", rh.Raw)
	rg.Get("/{owner}/{repo}/commits", rh.Commits)
	rg.Get("/{owner}/{repo}/commits/{rest...}", rh.Commits)
	rg.Get("/{owner}/{repo}/commit/{sha}", rh.Commit)
	rg.Get("/{owner}/{repo}/branches", rh.Branches)
	rg.Get("/{owner}/{repo}/tags", rh.Tags)
	rg.Get("/{owner}/{repo}/find/{rest...}", rh.FileFinder)
}

// mountCompare registers the branch-comparison routes under /{owner}/{repo}/compare.
// The compare Resolve middleware loads the repository read-gated for the viewer so
// the picker and the range view never confirm a private repo's existence. The repo
// service is the gate: with no service the routes stay unmounted. See
// implementation/09 section 8.
func mountCompare(page *mizu.Router, d Deps) {
	if d.Repos == nil {
		return
	}
	ch := webcompare.New(webcompare.Deps{
		Repos:  d.Repos,
		Render: d.Render,
		View:   d.View,
		Logger: d.Logger,
	})
	cg := page.With(ch.Resolve)
	cg.Get("/{owner}/{repo}/compare", ch.Picker)
	cg.Get("/{owner}/{repo}/compare/{basehead...}", ch.Range)
}

// mountChecks registers the commit-checks page under /{owner}/{repo}/checks/{ref}.
// Like the code-browsing routes it runs the checks Resolve middleware first, which
// loads the repository read-gated for the viewer (or a 404), so the handler only
// folds the ref's rollup for a visible repository and a private one the viewer
// cannot see 404s rather than confirming its existence. The checks service is the
// gate, and the repo service backs the header bar; with either missing the route
// stays unmounted, the same as the other surfaces. The page renders the backed
// checks signals (check runs, suites, commit statuses, and their rollup); the
// full Actions run engine doc 11 sketches has no store, so it is absent rather
// than faked. The greedy {rest} carries the whole ref so a branch with slashes
// resolves as one ref. See implementation/11.
func mountChecks(page *mizu.Router, d Deps) {
	if d.Checks == nil || d.Repos == nil {
		return
	}
	ch := webchecks.New(webchecks.Deps{
		Checks: d.Checks,
		Repos:  d.Repos,
		Render: d.Render,
		View:   d.View,
		Logger: d.Logger,
	})
	cg := page.With(ch.Resolve)
	cg.Get("/{owner}/{repo}/checks/{rest...}", ch.Index)
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
		Checks:  d.Checks,
		Repos:   d.Repos,
		URLs:    d.URLs,
		Render:  d.Render,
		View:    d.View,
		Markup:  d.Markup,
		Logger:  d.Logger,
	})
	pg := page.With(ph.Resolve)
	pg.Get("/{owner}/{repo}/pulls", ph.Index)
	pg.Post("/{owner}/{repo}/pulls", ph.Create)
	pg.Get("/{owner}/{repo}/pull/{number}", ph.Conversation)
	pg.Get("/{owner}/{repo}/pull/{number}/commits", ph.Commits)
	pg.Get("/{owner}/{repo}/pull/{number}/files", ph.Files)
	// The Checks tab mounts only when the checks service is wired, the same gate
	// the standalone checks page sits behind; the shell hides the tab then too.
	if d.Checks != nil {
		pg.Get("/{owner}/{repo}/pull/{number}/checks", ph.Checks)
	}
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

// mountRepoSettings registers the repository settings tree under
// /{owner}/{repo}/settings. Githome backs one section, the webhooks, so the
// routes are the bare-root redirect, the webhooks list, the shared create and
// edit form, the delete, and a recorded delivery's detail and replay. Every route
// runs the reposettings Resolve middleware first, which loads the repository
// read-gated for the viewer and then gates it to an administrator, so a
// repository the viewer cannot see and one they can see but not administer both
// render the same 404 (the settings surface never confirms its own existence to
// someone who cannot use it). The hook service and the repo service are the gate:
// with either missing the routes stay unmounted, the same as the other surfaces.
// The literal /hooks/new is registered before the /hooks/{hook} route so "new" is
// never read as an id, and every mutation posts and redirects behind the CSRF
// guard. See implementation/13 section 3.
func mountRepoSettings(page *mizu.Router, d Deps) {
	if d.Hooks == nil || d.Repos == nil {
		return
	}
	rh := webreposettings.New(webreposettings.Deps{
		Hooks:  d.Hooks,
		Repos:  d.Repos,
		Render: d.Render,
		View:   d.View,
		Flash:  d.Flash,
		Logger: d.Logger,
	})
	rg := page.With(rh.Resolve)
	rg.Get("/{owner}/{repo}/settings", rh.Root)
	rg.Get("/{owner}/{repo}/settings/hooks", rh.Hooks)
	rg.Get("/{owner}/{repo}/settings/hooks/new", rh.NewHook)
	rg.Post("/{owner}/{repo}/settings/hooks", rh.CreateHook)
	rg.Get("/{owner}/{repo}/settings/hooks/{hook}", rh.EditHook)
	rg.Post("/{owner}/{repo}/settings/hooks/{hook}", rh.UpdateHook)
	rg.Post("/{owner}/{repo}/settings/hooks/{hook}/delete", rh.DeleteHook)
	rg.Get("/{owner}/{repo}/settings/hooks/{hook}/deliveries/{delivery}", rh.Delivery)
	rg.Post("/{owner}/{repo}/settings/hooks/{hook}/deliveries/{delivery}/redeliver", rh.Redeliver)
}

// mountSettings registers the account settings tree under /settings. The bare
// /settings redirects to /settings/profile, the first backed section. Each
// handler gates on the signed-in viewer itself and bounces an anonymous request
// to the sign-in form with return_to carrying the page it wanted.
// The /settings literal is a reserved top-level name (fe/route), so it can
// never be read as a /{owner} profile, and it is registered before the profile
// catch-all. The profile save writes through the user service; the appearance
// save writes cookies the color-mode middleware reads; the keys and tokens pages
// show honest-absence stubs today. See implementation/13 section 2.
func mountSettings(page *mizu.Router, d Deps) {
	sh := websettings.New(websettings.Deps{
		Render: d.Render,
		View:   d.View,
		Flash:  d.Flash,
		Users:  d.Users,
		Tokens: d.Tokens,
		Logger: d.Logger,
	})
	page.Get("/settings", sh.Index)
	page.Get("/settings/profile", sh.Profile)
	page.Post("/settings/profile", sh.SaveProfile)
	page.Get("/settings/appearance", sh.Appearance)
	page.Post("/settings/appearance", sh.SaveAppearance)
	page.Get("/settings/keys", sh.Keys)
	page.Get("/settings/tokens", sh.Tokens)
	if d.Tokens != nil {
		page.Post("/settings/tokens", sh.CreateToken)
		page.Post("/settings/tokens/{id}/delete", sh.DeleteToken)
	}
}

// mountProfile registers the user and organization profile at /{owner}, the
// root-level catch-all. It is mounted last, after every owned top-level name
// (/search and the rest of the reserved set) and every /{owner}/{repo} surface is
// registered, so a single-segment reserved name is never read as a login; the
// profile Resolve middleware double-checks the reserved set as a backstop and 404s
// a reserved name rather than resolving it. The user service is the gate that
// resolves the account; the event service backs the activity feed and the search
// service backs the repositories tab and the overview grid (the same search the
// search page reads, scoped to the owner). With the user service missing the route
// stays unmounted, the same as the other surfaces; a missing event or search
// service degrades a tab body rather than the whole profile. See implementation/12
// sections 5, 6, and 7.
func mountProfile(page *mizu.Router, d Deps) {
	if d.Users == nil {
		return
	}
	ph := webprofile.New(webprofile.Deps{
		Users:  d.Users,
		Events: d.Events,
		Search: d.Search,
		URLs:   d.URLs,
		Render: d.Render,
		View:   d.View,
		Logger: d.Logger,
	})
	pg := page.With(ph.Resolve)
	pg.Get("/{owner}", ph.Show)
}

// mountAuth registers the web auth routes: /login (GET + POST /login/session),
// /join (GET + POST /join), and /logout (GET + POST /logout/session). The auth
// store is the gate: with no Auth service the routes stay unmounted and those
// paths 404. This is F1. See implementation/06.
func mountAuth(page *mizu.Router, d Deps) {
	if d.Auth == nil {
		return
	}
	ah := webauth.New(webauth.Deps{
		Store:    d.Auth,
		Sessions: d.Sessions,
		View:     d.View,
		Render:   d.Render,
		Logger:   d.Logger,
	})
	page.Get("/login", ah.LoginForm)
	page.Post("/login/session", ah.LoginSubmit)
	page.Get("/join", ah.JoinForm)
	page.Post("/join", ah.JoinSubmit)
	page.Get("/logout", ah.LogoutForm)
	page.Post("/logout/session", ah.LogoutSubmit)

	if d.OAuthSvc != nil {
		oh := webauth.NewOAuthHandlers(d.OAuthSvc, d.Render, d.View)
		page.Get("/login/oauth/authorize", oh.AuthorizeForm)
		page.Post("/login/oauth/authorize", oh.AuthorizeSubmit)
		// The device-flow approval page, the verification_uri the device-code
		// response points CLI clients at. Gated to a signed-in viewer inside the
		// handlers; anonymous requests bounce to /login and back.
		page.Get("/login/device", oh.DeviceForm)
		page.Post("/login/device", oh.DeviceSubmit)
	}
}

// mountNotifications registers the /notifications inbox route. The inbox is
// function-private: it exists for every account, so an anonymous request leaks
// nothing by being bounced to the sign-in form with return_to carrying the
// inbox (spec §7.1), the 302 github.com answers. The route exists
// unconditionally but renders the empty inbox when the notifications domain
// layer is not yet backed. The /notifications literal is a reserved top-level
// name (fe/route), so it is registered before the profile catch-all and is
// never read as a login. See implementation/12 section 3.
func mountNotifications(page *mizu.Router, d Deps) {
	page.Get("/notifications", func(c *mizu.Ctx) error {
		if view.ViewerFrom(c.Context()) == nil {
			return c.Redirect(http.StatusFound, route.LoginWithReturn(c.Request().URL.RequestURI()))
		}
		return d.Render.Page(c, "notifications/index", d.View.Notifications(c))
	})
}

// handleHome renders the landing page. A signed-in viewer sees the dashboard
// shell, an anonymous viewer the sign-in blankslate; the difference is driven by
// the viewer the session middleware resolved, so the same handler serves both.
func handleHome(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		return d.Render.Page(c, "home/index", d.View.Home(c))
	}
}

// handleNotFound serves the unmounted URL space. A trailing-slash URL 301s to
// its canonical slash-less form with the query preserved, the redirect
// github.com sends (spec §5.1); only GET and HEAD ever reach it, since the
// catch-all is registered for GET and the mux answers other methods itself.
// The API namespace keeps a machine-shaped 404: with the web front mounted the
// REST surface omits its own root catch-all (api/rest Deps.WebFront) and an
// unknown /api path would otherwise get an HTML page, so this hands API
// clients the same GitHub-shaped body the REST surface uses. Everything else
// renders the themed 404 (spec §7.1).
func handleNotFound(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		r := c.Request()
		if strings.HasPrefix(r.URL.Path, "/api/") {
			return c.Bytes(http.StatusNotFound,
				[]byte(`{"message":"Not Found","documentation_url":"https://docs.github.com/rest"}`),
				"application/json; charset=utf-8")
		}
		// Trim on the escaped path so an encoded segment survives the redirect
		// byte for byte. All trailing slashes collapse, so /owner/repo// also
		// lands on the canonical URL in one hop.
		if ep := r.URL.EscapedPath(); len(ep) > 1 && strings.HasSuffix(ep, "/") {
			target := strings.TrimRight(ep, "/")
			if target == "" {
				target = "/"
			}
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			return c.Redirect(http.StatusMovedPermanently, target)
		}
		return d.Render.NotFoundWithChrome(c, d.View.Chrome(c, ""))
	}
}

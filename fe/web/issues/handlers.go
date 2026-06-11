// Package issues holds the Githome web front's issues handlers: the index with
// its search-and-filter bar, the detail page with its comment timeline and
// sidebar, the comment composer, the reactions, and the new-issue form. Each
// handler resolves and authorizes through the same domain.IssueService the REST
// and GraphQL surfaces use, so the page and the API never disagree, maps the
// result into a fe/view model with every URL precomputed through fe/route, and
// renders through fe/render. A repository the viewer cannot see was turned into a
// hard 404 by the Resolve middleware before any handler ran (the 404-not-403
// rule), and every mutation has a plain HTML form path that works with no
// JavaScript. See implementation/08.
package issues

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/markup"
	"github.com/tamnd/githome/presenter"
)

// Deps are the issues handlers' dependencies: the domain issue service for every
// read and write, the repo service to resolve and read-gate the repository, the
// presenter for avatar URLs, the render set, the view builder for the shell
// chrome, the shared markup renderer for comment bodies, and a logger for the
// notices the list view emits. A nil markup renderer falls back to escaped
// comment source.
type Deps struct {
	Issues *domain.IssueService
	Repos  *domain.RepoService
	URLs   *presenter.URLBuilder
	Render *render.Set
	View   *view.Builder
	Markup *markup.Renderer
	Logger *slog.Logger
}

// Handlers is the issues handler set. One is built at boot and shared; it holds
// no per-request state.
type Handlers struct {
	issues *domain.IssueService
	repos  *domain.RepoService
	urls   *presenter.URLBuilder
	render *render.Set
	view   *view.Builder
	markup *markup.Renderer
	log    *slog.Logger
}

// New wires the handler set from its dependencies.
func New(d Deps) *Handlers {
	return &Handlers{
		issues: d.Issues,
		repos:  d.Repos,
		urls:   d.URLs,
		render: d.Render,
		view:   d.View,
		markup: d.Markup,
		log:    d.Logger,
	}
}

// repoCtxKey carries the resolved repository on the request context between the
// Resolve middleware and the handlers.
type repoCtxKey int

const keyRepo repoCtxKey = iota

// Resolve loads the repository named by the {owner} and {repo} path parameters,
// read-gated for the viewer, and stores it on the context. A missing repository,
// or a private one the viewer cannot see, renders the same 404, so a private repo
// never leaks through the status code. This mirrors the code-browsing Resolve so
// the two surfaces gate identically. It is the one place the repo is loaded; every
// handler reads it back with repoFromContext.
func (h *Handlers) Resolve(next mizu.Handler) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Context()
		repo, err := h.repos.GetRepo(ctx, webmw.ViewerID(ctx), c.Param("owner"), c.Param("repo"))
		if errors.Is(err, domain.ErrRepoNotFound) {
			return h.render.RepoNotFound(c, h.chrome(c, ""))
		}
		if err != nil {
			return err
		}
		// The lookup is case-insensitive; the URL is not. A wrong-cased owner or
		// name 301s to the canonical spelling instead of serving every variant.
		if target, ok := route.CanonicalRepoTarget(c.Request(), c.Param("owner"), c.Param("repo"), ownerLogin(repo), repo.Name); ok {
			return c.Redirect(http.StatusMovedPermanently, target)
		}
		r := c.Request()
		*r = *r.WithContext(context.WithValue(ctx, keyRepo, repo))
		return next(c)
	}
}

// repoFromContext returns the repository the Resolve middleware stored.
func repoFromContext(ctx context.Context) (*domain.Repo, bool) {
	repo, ok := ctx.Value(keyRepo).(*domain.Repo)
	return repo, ok
}

// notFound renders the soft 404 in the repo shell: a missing issue, comment, or
// number inside a repository the viewer can see.
func (h *Handlers) notFound(c *mizu.Ctx) error {
	return h.render.RepoNotFound(c, h.chrome(c, ""))
}

// chrome builds the shell model for an issues page through the view builder, so
// an issues page wears the same header, theme, and viewer menu as any other page.
func (h *Handlers) chrome(c *mizu.Ctx, title string) view.Chrome {
	return h.view.Chrome(c, title)
}

// canWrite reports whether the signed-in viewer may write to the repository. It
// is the same owner-only rule the issue service enforces, read here so the page
// shows the edit affordances (the close button, the title pencil, the sidebar
// pickers) only to a viewer who can actually use them. The service authorizes
// every mutation again, so this is a display gate, never the security boundary.
func canWrite(repo *domain.Repo, viewerPK int64) bool {
	return viewerPK != 0 && viewerPK == repo.OwnerPK
}

// canComment reports whether the viewer may add a comment: any signed-in viewer
// who can see the repo, matching the comment service. An anonymous viewer (PK 0)
// sees the timeline read-only.
func canComment(viewerPK int64) bool {
	return viewerPK != 0
}

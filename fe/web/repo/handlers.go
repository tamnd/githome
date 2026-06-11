// Package repo holds the Githome web front's code-browsing handlers: the repo
// home, the tree and blob views, the raw byte view, commit history, the branch
// and tag overviews, and the file finder. Each handler reads the git layer only
// through the domain repo service (the same service the REST and GraphQL surfaces
// use, so the page and the API never disagree), maps the result into a fe/view
// model with every URL precomputed through fe/route, and renders through
// fe/render. A repository the viewer cannot see was turned into a hard 404 by the
// Resolve middleware before any handler ran, so a handler only ever decides
// whether a ref, path, or object inside a visible repo exists. See
// implementation/07.
package repo

import (
	"context"
	"errors"
	"log/slog"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/markup"
	"github.com/tamnd/githome/presenter"
)

// Deps are the code-browsing handlers' dependencies: the domain repo service for
// every git read, the presenter for clone URLs, the render set, the view builder
// for the shell chrome, the shared markup renderer for the README and Markdown
// blob views, and a logger for the truncation notices the heavy views emit. The
// handler package maps domain data into fe/view models itself, so the view
// builder is needed only for Chrome. A nil markup renderer falls back to the
// escaped-source view.
type Deps struct {
	Repos  *domain.RepoService
	URLs   *presenter.URLBuilder
	Render *render.Set
	View   *view.Builder
	Markup *markup.Renderer
	Logger *slog.Logger
}

// Handlers is the code-browsing handler set. One is built at boot and shared; it
// holds no per-request state.
type Handlers struct {
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
// read-gated for the viewer, and stores it on the context for the handler. A repo
// that does not exist, or a private one the viewer cannot see, renders the same
// 404, so a private repository never leaks through the status code (the
// 404-not-403 rule, implementation/07 section 12). An infrastructure error is
// returned so the recover layer renders a 500. This is the one place the repo is
// loaded; every handler reads it back with repoFromContext.
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
		r := c.Request()
		*r = *r.WithContext(context.WithValue(ctx, keyRepo, repo))
		return next(c)
	}
}

// repoFromContext returns the repository the Resolve middleware stored. The
// boolean is false only if a handler is reached without Resolve, which the mount
// never does; the handlers treat a missing repo as a 404 rather than panicking.
func repoFromContext(ctx context.Context) (*domain.Repo, bool) {
	repo, ok := ctx.Value(keyRepo).(*domain.Repo)
	return repo, ok
}

// notFound renders the soft 404 in the repo shell: a missing ref, path, blob, or
// object inside a repository the viewer can see. It keeps the repo chrome so the
// viewer stays oriented, and it returns status 404.
func (h *Handlers) notFound(c *mizu.Ctx) error {
	return h.render.RepoNotFound(c, h.chrome(c, ""))
}

// chrome builds the shell model for a repo page through the view builder, so a
// repo page wears the same header, theme, and viewer menu as any other page.
func (h *Handlers) chrome(c *mizu.Ctx, title string) view.Chrome {
	return h.view.Chrome(c, title)
}

// resolveRef reads the greedy {rest} tail of a tree/blob/raw URL and splits it
// into a ref and a path, preferring the longest leading segment sequence that
// names a real ref (a branch named feature/x beats the branch feature). The split
// is backed by the request's shared ref set through refExists. A tail that names
// no ref returns ok false, which the caller renders as the soft 404. See
// implementation/07 section 2.
func (h *Handlers) resolveRef(repo *domain.Repo, refs *refSet, rest string) (ref, path string, ok bool) {
	return route.SplitRefPath(rest, h.refExists(repo, refs))
}

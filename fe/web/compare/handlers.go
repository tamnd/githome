// Package compare holds the Githome web front's branch-comparison handlers:
// the branch picker that starts a comparison, and the range view that shows
// the three-dot diff between two branches with an optional pull-request
// creation form. It mirrors the repo and pulls packages: each handler loads
// the repository through its Resolve middleware, maps domain data into fe/view
// models through fe/route, and renders through fe/render. See
// implementation/09 section 8.
package compare

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
)

// Deps are the compare handlers' dependencies.
type Deps struct {
	Repos  *domain.RepoService
	Render *render.Set
	View   *view.Builder
	Logger *slog.Logger
}

// Handlers is the compare handler set. One is built at boot and shared; it
// holds no per-request state.
type Handlers struct {
	repos  *domain.RepoService
	render *render.Set
	view   *view.Builder
	log    *slog.Logger
}

// New wires the handler set from its dependencies.
func New(d Deps) *Handlers {
	return &Handlers{
		repos:  d.Repos,
		render: d.Render,
		view:   d.View,
		log:    d.Logger,
	}
}

type repoCtxKey int

const keyRepo repoCtxKey = iota

// Resolve loads the repository named by the {owner} and {repo} path parameters,
// read-gated for the viewer, and stores it on the context. A missing or private
// repository renders the same 404 to avoid confirming its existence.
func (h *Handlers) Resolve(next mizu.Handler) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Context()
		repo, err := h.repos.GetRepo(ctx, webmw.ViewerID(ctx), c.Param("owner"), c.Param("repo"))
		if errors.Is(err, domain.ErrRepoNotFound) {
			// The name may be a rename's old address: the redirect store keeps
			// old owner/name pairs pointing at the repository they now name.
			if moved, merr := h.repos.RepoRedirect(ctx, webmw.ViewerID(ctx), c.Param("owner"), c.Param("repo")); merr == nil {
				if target, ok := route.CanonicalRepoTarget(c.Request(), c.Param("owner"), c.Param("repo"), ownerLogin(moved), moved.Name); ok {
					return c.Redirect(http.StatusMovedPermanently, target)
				}
			}
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

func repoFromContext(ctx context.Context) (*domain.Repo, bool) {
	repo, ok := ctx.Value(keyRepo).(*domain.Repo)
	return repo, ok
}

func (h *Handlers) notFound(c *mizu.Ctx) error {
	return h.render.RepoNotFound(c, h.chrome(c, ""))
}

func (h *Handlers) chrome(c *mizu.Ctx, title string) view.Chrome {
	return h.view.Chrome(c, title)
}

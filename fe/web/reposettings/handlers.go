// Package reposettings holds the Githome web front's repository settings
// handlers. Githome backs one repository settings section today, the webhooks:
// the domain HookService is the only repo-settings surface with a store behind
// it, so that is the one section the sidebar offers. The unbacked sections
// (general properties, collaborators, branch protection, secrets) get no nav
// entry rather than a dead link, the same honest absence the rest of the front
// took for what it cannot yet back.
//
// The whole surface needs administrative authority over the repository, the same
// authority the HookService authorizes every call against. The Resolve middleware
// loads the repository read-gated for the viewer and then gates it to an
// administrator, turning both a repository the viewer cannot see and one they can
// see but not administer into the same 404: the settings surface never confirms
// its own existence to someone who cannot use it (the 404-not-403 rule). Every
// mutation posts and redirects, so the no-JS flow lands on a clean GET, and the
// CSRF guard the page chain installs verifies each post. The HookService
// re-authorizes every write, so the Resolve gate is the front door, never the only
// lock. See implementation/13.
package reposettings

import (
	"context"
	"errors"
	"log/slog"
	"strconv"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

// Deps are the repository settings handlers' dependencies: the domain hook
// service for every webhook read and write, the repo service to resolve and
// admin-gate the repository, the render set, the view builder for the shell
// chrome, the flash store for the one-shot notice a save reports after its
// redirect, and a logger.
type Deps struct {
	Hooks  *domain.HookService
	Repos  *domain.RepoService
	Render *render.Set
	View   *view.Builder
	Flash  Flasher
	Logger *slog.Logger
}

// Flasher is the slice of the flash store the settings handlers use: stage a
// one-shot message to show on the page the redirect lands on. The webmw.Flash
// satisfies it; the narrow interface keeps the handlers testable without a cookie
// round-trip.
type Flasher interface {
	Add(c *mizu.Ctx, kind, message string)
}

// Handlers is the repository settings handler set. One is built at boot and
// shared; it holds no per-request state.
type Handlers struct {
	hooks  *domain.HookService
	repos  *domain.RepoService
	render *render.Set
	view   *view.Builder
	flash  Flasher
	log    *slog.Logger
}

// New wires the handler set from its dependencies.
func New(d Deps) *Handlers {
	return &Handlers{
		hooks:  d.Hooks,
		repos:  d.Repos,
		render: d.Render,
		view:   d.View,
		flash:  d.Flash,
		log:    d.Logger,
	}
}

// repoCtxKey carries the resolved repository on the request context between the
// Resolve middleware and the handlers.
type repoCtxKey int

const keyRepo repoCtxKey = iota

// Resolve loads the repository named by the {owner} and {repo} path parameters,
// read-gated for the viewer, then gates it to an administrator and stores it on
// the context. A missing repository, a private one the viewer cannot see, and one
// the viewer can see but not administer all render the same 404, so the settings
// surface never leaks a repository's existence or its administrability through the
// status code. It is the one place the repo is loaded and authorized; every
// handler reads it back with repoFromContext and trusts the gate.
func (h *Handlers) Resolve(next mizu.Handler) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Context()
		viewerPK := webmw.ViewerID(ctx)
		repo, err := h.repos.GetRepo(ctx, viewerPK, c.Param("owner"), c.Param("repo"))
		if errors.Is(err, domain.ErrRepoNotFound) {
			return h.notFound(c)
		}
		if err != nil {
			return err
		}
		if !canAdminister(repo, viewerPK) {
			return h.notFound(c)
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

// canAdminister reports whether the viewer administers the repository. It is the
// owner-only rule the hook service authorizes every call against, read here so the
// Resolve gate keeps a non-administrator out of the whole surface rather than
// letting them reach a handler that the service would then reject. An anonymous
// viewer (PK 0) never administers anything.
func canAdminister(repo *domain.Repo, viewerPK int64) bool {
	return viewerPK != 0 && viewerPK == repo.OwnerPK
}

// notFound renders the repository 404 in the page shell, the page a missing or
// unadministrable repository settings route renders.
func (h *Handlers) notFound(c *mizu.Ctx) error {
	return h.render.RepoNotFound(c, h.view.Chrome(c, ""))
}

// owner and name return the path parameters the URL builders and the sidebar
// heading read. They come from the request rather than the resolved repo so a
// link round-trips the exact segments the viewer navigated.
func (h *Handlers) owner(c *mizu.Ctx) string { return c.Param("owner") }
func (h *Handlers) name(c *mizu.Ctx) string  { return c.Param("repo") }

// nav builds the repository settings sidebar: the repository's full name heading
// linking back to the repository, and the section links. Webhooks is the only
// backed section, so it is the only entry; active marks the current page.
func (h *Handlers) nav(c *mizu.Ctx, active string) view.SettingsNav {
	owner, name := h.owner(c), h.name(c)
	hooks := route.RepoHooks(owner, name)
	return view.SettingsNav{
		Heading:    owner + "/" + name,
		HeadingURL: route.Repo(owner, name),
		Items: []view.SettingsNavItem{
			{Label: "Webhooks", URL: hooks, IsActive: active == hooks},
		},
	}
}

// parseID reads a positive integer path parameter, reporting false when it is not
// a number so the handler can 404 rather than pass a bad id to the service.
func parseID(c *mizu.Ctx, key string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(key), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

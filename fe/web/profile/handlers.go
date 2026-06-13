// Package profile holds the Githome web front's profile handlers: the user and
// organization overview at /{owner} and its repositories tab. The catch-all sits
// at the root, after every owned top-level name and every /{owner}/{repo} surface
// is registered, so a reserved name (login, settings, search, an asset) is never
// read as a login. Each handler resolves the account through the domain user
// service, its repositories through the same domain search the search page uses
// (scoped to the owner), and its activity through the domain event service, maps
// the result into a fe/view model with every URL precomputed through fe/route, and
// renders through fe/render. An account that does not exist renders the same 404
// as any other missing page. See implementation/12 sections 5, 6, and 7.
package profile

import (
	"context"
	"errors"
	"log/slog"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/presenter"
)

// Deps are the profile handlers' dependencies: the user service for the identity
// lookup, the event service for the activity feed, the search service for the
// owner's repositories (the same service the search page reads, scoped to the
// owner with a user:/org: qualifier), the presenter for the avatar URL, the render
// set, the view builder for the shell chrome, the shared markup renderer for the
// profile bio, and a logger. The handler package maps domain data into fe/view
// models itself, so the view builder is needed only for Chrome. The bio is a
// single line shown as escaped plain text, so the profile needs no markup
// renderer.
type Deps struct {
	Users  *domain.UserService
	Events *domain.EventService
	Search *domain.SearchService
	Social *domain.SocialService
	URLs   *presenter.URLBuilder
	Render *render.Set
	View   *view.Builder
	Logger *slog.Logger
}

// Handlers is the profile handler set. One is built at boot and shared; it holds
// no per-request state.
type Handlers struct {
	users  *domain.UserService
	events *domain.EventService
	search *domain.SearchService
	social *domain.SocialService
	urls   *presenter.URLBuilder
	render *render.Set
	view   *view.Builder
	log    *slog.Logger
}

// New wires the handler set from its dependencies.
func New(d Deps) *Handlers {
	return &Handlers{
		users:  d.Users,
		events: d.Events,
		search: d.Search,
		social: d.Social,
		urls:   d.URLs,
		render: d.Render,
		view:   d.View,
		log:    d.Logger,
	}
}

// userCtxKey carries the resolved account on the request context between the
// Resolve middleware and the handler.
type userCtxKey int

const keyUser userCtxKey = iota

// Resolve loads the account named by the {owner} path parameter and stores it on
// the context for the handler. A reserved top-level name is never a login, so it
// renders the same 404 as a missing account rather than resolving; this is the
// one place the catch-all double-checks the reserved set, in case a future route
// is added under a reserved name without its own handler. An account that does not
// exist renders the generic 404 (the profile is a public surface, so there is no
// private-versus-missing split to hide here, unlike a repository). An
// infrastructure error is returned so the recover layer renders a 500.
func (h *Handlers) Resolve(next mizu.Handler) mizu.Handler {
	return func(c *mizu.Ctx) error {
		login := c.Param("owner")
		if route.IsReservedTop(login) {
			return h.render.NotFoundWithChrome(c, h.chrome(c, ""))
		}
		ctx := c.Context()
		u, err := h.users.ByLogin(ctx, login)
		if errors.Is(err, domain.ErrUserNotFound) {
			return h.render.NotFoundWithChrome(c, h.chrome(c, ""))
		}
		if err != nil {
			return err
		}
		r := c.Request()
		*r = *r.WithContext(context.WithValue(ctx, keyUser, u))
		return next(c)
	}
}

// userFromContext returns the account the Resolve middleware stored. The boolean
// is false only if a handler is reached without Resolve, which the mount never
// does; the handler treats a missing account as a 404 rather than panicking.
func userFromContext(ctx context.Context) (*domain.User, bool) {
	u, ok := ctx.Value(keyUser).(*domain.User)
	return u, ok
}

// chrome builds the shell model for a profile page through the view builder, so a
// profile wears the same header, theme, and viewer menu as any other page.
func (h *Handlers) chrome(c *mizu.Ctx, title string) view.Chrome {
	return h.view.Chrome(c, title)
}

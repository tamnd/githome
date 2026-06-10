// Package settings holds the Githome web front's account settings handlers. The
// account settings tree lives under /settings and is gated to the signed-in
// viewer: it administers the viewer's own account. The surface is
// function-private rather than secret (every account has settings), so an
// anonymous request is bounced to the sign-in form with return_to carrying the
// page it wanted, the 302 github.com answers; nothing leaks because there is
// nothing to confirm. Githome backs one account section today, the appearance
// preference, since the color mode and themes ride cookies the color-mode
// middleware already reads; the unbacked sections (profile, emails, keys, tokens,
// sessions, security) get no nav entry rather than a dead link, the same honest
// absence the profile took for its unbacked tabs. Every mutation posts and
// redirects, so the no-JS flow lands on a clean GET, and the CSRF guard the page
// chain installs verifies each post. See implementation/13.
package settings

import (
	"log/slog"
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// Deps are the account settings handlers' dependencies: the render set, the view
// builder for the shell chrome, the flash store for the one-shot outcome notice a
// save reports after its redirect, the user service for reading and writing
// account profile fields, and a logger.
type Deps struct {
	Render *render.Set
	View   *view.Builder
	Flash  Flasher
	Users  *domain.UserService
	Tokens TokenService // nil keeps the tokens page on its honest-absence stub
	Logger *slog.Logger
}

// Flasher is the slice of the flash store the settings handlers use: stage a
// one-shot message to show on the page the redirect lands on. The webmw.Flash
// satisfies it; the narrow interface keeps the handler testable without a cookie
// round-trip.
type Flasher interface {
	Add(c *mizu.Ctx, kind, message string)
}

// Handlers is the account settings handler set. One is built at boot and shared;
// it holds no per-request state.
type Handlers struct {
	render *render.Set
	view   *view.Builder
	flash  Flasher
	users  *domain.UserService
	tokens TokenService
	log    *slog.Logger
}

// New wires the handler set from its dependencies.
func New(d Deps) *Handlers {
	return &Handlers{
		render: d.Render,
		view:   d.View,
		flash:  d.Flash,
		users:  d.Users,
		tokens: d.Tokens,
		log:    d.Logger,
	}
}

// prefCookieMaxAge is the lifetime of an appearance preference cookie: about
// thirteen months, the longest a browser will honor, so a returning viewer keeps
// their theme without having to pick it again each session.
const prefCookieMaxAge = 400 * 24 * 60 * 60

// gate resolves the signed-in viewer the session middleware put on the context.
// The boolean is false for an anonymous request, which every handler turns into
// the sign-in bounce: the surface administers the viewer's own account, so the
// only thing to do with no viewer is to go get one.
func (h *Handlers) gate(c *mizu.Ctx) (*view.Viewer, bool) {
	v := view.ViewerFrom(c.Context())
	if v == nil {
		return nil, false
	}
	return v, true
}

// signInBounce sends an anonymous request to the sign-in form, with return_to
// carrying the settings page it wanted so a successful sign-in lands back on
// it. The settings tree is function-private, not secret, so the bounce
// confirms nothing a 404 would have hidden (spec §7.1).
func (h *Handlers) signInBounce(c *mizu.Ctx) error {
	return c.Redirect(http.StatusFound, route.LoginWithReturn(c.Request().URL.RequestURI()))
}

// notFound renders the themed 404, used when a signed-in request hits a state
// that should not happen (for example the viewer row failing to load).
func (h *Handlers) notFound(c *mizu.Ctx) error {
	return h.render.NotFoundWithChrome(c, h.view.Chrome(c, ""))
}

// nav builds the account settings sidebar: the viewer's login heading linking to
// their profile, and the backed section links. Active marks the current page.
func (h *Handlers) nav(v *view.Viewer, active string) view.SettingsNav {
	heading := "Settings"
	headingURL := route.AccountSettings()
	if v != nil && v.Login != "" {
		heading = v.Login
		headingURL = route.Profile(v.Login)
	}
	item := func(label, url string) view.SettingsNavItem {
		return view.SettingsNavItem{Label: label, URL: url, IsActive: active == url}
	}
	return view.SettingsNav{
		Heading:    heading,
		HeadingURL: headingURL,
		Items: []view.SettingsNavItem{
			item("Profile", route.ProfileSettings()),
			item("Appearance", route.Appearance()),
			item("SSH and GPG keys", route.SettingsKeys()),
			item("Personal access tokens", route.SettingsTokens()),
		},
	}
}

// formString reads a trimmed form value, the empty string on a parse failure.
func formString(c *mizu.Ctx, key string) string {
	form, err := c.Form()
	if err != nil {
		return ""
	}
	return form.Get(key)
}

// redirect sends the browser to location with 303 See Other, so a reload after a
// successful post re-fetches with GET rather than re-submitting the form.
func redirect(c *mizu.Ctx, location string) error {
	return c.Redirect(http.StatusSeeOther, location)
}

// setPrefCookie writes one appearance preference cookie. It is HttpOnly because
// only the server (the color-mode middleware) reads it, SameSite Lax and Secure
// to match the front's other cookies, and rooted at / so it applies site-wide.
func setPrefCookie(c *mizu.Ctx, name, value string) {
	http.SetCookie(c.Writer(), &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   prefCookieMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
	})
}

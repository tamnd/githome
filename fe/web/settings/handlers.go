// Package settings holds the Githome web front's account settings handlers. The
// account settings tree lives under /settings and is gated to the signed-in
// viewer: it administers the viewer's own account, so an anonymous request gets
// the same 404 as any page that is not there, never a sign-in wall that confirms
// the surface exists. Githome backs one account section today, the appearance
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

	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// Deps are the account settings handlers' dependencies: the render set, the view
// builder for the shell chrome, the flash store for the one-shot outcome notice a
// save reports after its redirect, and a logger.
type Deps struct {
	Render *render.Set
	View   *view.Builder
	Flash  Flasher
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
	log    *slog.Logger
}

// New wires the handler set from its dependencies.
func New(d Deps) *Handlers {
	return &Handlers{
		render: d.Render,
		view:   d.View,
		flash:  d.Flash,
		log:    d.Logger,
	}
}

// prefCookieMaxAge is the lifetime of an appearance preference cookie: about
// thirteen months, the longest a browser will honor, so a returning viewer keeps
// their theme without having to pick it again each session.
const prefCookieMaxAge = 400 * 24 * 60 * 60

// gate resolves the signed-in viewer the session middleware put on the context.
// The boolean is false for an anonymous request, which every handler turns into a
// 404: the account settings surface administers the viewer's own account, so to
// someone not signed in it simply does not exist.
func (h *Handlers) gate(c *mizu.Ctx) (*view.Viewer, bool) {
	v := view.ViewerFrom(c.Context())
	if v == nil {
		return nil, false
	}
	return v, true
}

// notFound renders the themed 404 for an anonymous request to the settings
// surface, the same page any missing route renders.
func (h *Handlers) notFound(c *mizu.Ctx) error {
	return h.render.NotFoundWithChrome(c, h.view.Chrome(c, ""))
}

// nav builds the account settings sidebar: the viewer's login heading linking to
// their profile, and the section links. Appearance is the only backed section, so
// it is the only entry; active marks the current page.
func (h *Handlers) nav(v *view.Viewer, active string) view.SettingsNav {
	heading := "Settings"
	headingURL := route.AccountSettings()
	if v != nil && v.Login != "" {
		heading = v.Login
		headingURL = route.Profile(v.Login)
	}
	return view.SettingsNav{
		Heading:    heading,
		HeadingURL: headingURL,
		Items: []view.SettingsNavItem{
			{Label: "Appearance", URL: route.Appearance(), IsActive: active == route.Appearance()},
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

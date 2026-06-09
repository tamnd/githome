package settings

import (
	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// appearance.go holds the account appearance section: the form that picks the
// color mode and the light and dark themes, and the save that writes the three
// preference cookies the color-mode middleware reads back on every request. The
// preference rides cookies rather than an account column, so it works with no
// JavaScript and needs no schema change.

// Index redirects the bare /settings root to the first backed section. A bookmark
// of /settings keeps working as Githome adds sections, always landing on a real
// page rather than a blank index.
func (h *Handlers) Index(c *mizu.Ctx) error {
	if _, ok := h.gate(c); !ok {
		return h.notFound(c)
	}
	return redirect(c, route.ProfileSettings())
}

// Appearance renders the appearance form, prefilled from the color mode the
// middleware resolved for this request, so the form opens showing the viewer's
// current choice rather than a default.
func (h *Handlers) Appearance(c *mizu.Ctx) error {
	v, ok := h.gate(c)
	if !ok {
		return h.notFound(c)
	}
	m := view.ColorModeFrom(c.Context())
	vm := view.AppearanceVM{
		Chrome:      h.view.Chrome(c, "Appearance settings"),
		Nav:         h.nav(v, route.Appearance()),
		Action:      route.Appearance(),
		Modes:       view.AppearanceModeOptions(m.Mode),
		LightThemes: view.LightThemeOptions(m.Light),
		DarkThemes:  view.DarkThemeOptions(m.Dark),
	}
	return h.render.Page(c, "settings/appearance", vm)
}

// SaveAppearance validates the submitted mode and themes against the closed
// catalogs the form offered, writes the three cookies, and redirects back to the
// form with a flash. The form can only present valid values, so a value outside
// the catalogs is a forged post: it is rejected with an error flash and no cookie
// is written, rather than poisoning the preference with a theme that does not
// exist.
func (h *Handlers) SaveAppearance(c *mizu.Ctx) error {
	if _, ok := h.gate(c); !ok {
		return h.notFound(c)
	}
	mode := formString(c, "mode")
	light := formString(c, "light_theme")
	dark := formString(c, "dark_theme")
	if !view.ValidMode(mode) || !view.ValidLightTheme(light) || !view.ValidDarkTheme(dark) {
		h.flash.Add(c, "error", "That is not an appearance you can pick.")
		return redirect(c, route.Appearance())
	}
	setPrefCookie(c, "color_mode", mode)
	setPrefCookie(c, "light_theme", light)
	setPrefCookie(c, "dark_theme", dark)
	h.flash.Add(c, "success", "Appearance preferences updated.")
	return redirect(c, route.Appearance())
}

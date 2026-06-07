package webmw

import (
	"context"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/view"
)

// setCtx replaces the request context in place. mizu's Ctx holds the request by
// pointer and exposes no setter, so middleware threads request-scoped values the
// same way the REST layer does: by overwriting the request the pointer addresses.
func setCtx(ctx context.Context, c *mizu.Ctx) {
	r := c.Request()
	*r = *r.WithContext(ctx)
}

// The cookies the color-mode preference reads. They are set by the appearance
// settings form (a later milestone) and default to the OS-following auto mode
// when absent, so a first-time visitor with no cookies still gets a themed page
// that tracks their system setting with no flash and no JavaScript.
const (
	cookieColorMode  = "color_mode"
	cookieLightTheme = "light_theme"
	cookieDarkTheme  = "dark_theme"
)

// validModes is the closed set the color-mode cookie may carry. An unknown value
// is ignored in favor of the default rather than written through to the template.
var validModes = map[string]bool{"auto": true, "light": true, "dark": true}

// validThemes is the closed set of theme ids, matching the nine palettes the
// asset build generates. A cookie naming anything else falls back to the default
// for its slot, so a stale or hand-edited cookie cannot select a theme that does
// not exist.
var validThemes = map[string]bool{
	"light":               true,
	"light_high_contrast": true,
	"light_colorblind":    true,
	"light_tritanopia":    true,
	"dark":                true,
	"dark_dimmed":         true,
	"dark_high_contrast":  true,
	"dark_colorblind":     true,
	"dark_tritanopia":     true,
}

// ColorMode reads the viewer's appearance cookies, validates them against the
// closed mode and theme sets, and stores the resulting ColorMode on the context
// for the view builder. It never errors: an absent or invalid cookie falls back
// to the default for that slot.
func ColorMode() mizu.Middleware {
	return func(next mizu.Handler) mizu.Handler {
		return func(c *mizu.Ctx) error {
			m := view.DefaultColorMode()
			if ck, err := c.Request().Cookie(cookieColorMode); err == nil && validModes[ck.Value] {
				m.Mode = ck.Value
			}
			if ck, err := c.Request().Cookie(cookieLightTheme); err == nil && validThemes[ck.Value] && isLight(ck.Value) {
				m.Light = ck.Value
			}
			if ck, err := c.Request().Cookie(cookieDarkTheme); err == nil && validThemes[ck.Value] && !isLight(ck.Value) {
				m.Dark = ck.Value
			}
			setCtx(view.WithColorMode(c.Context(), m), c)
			return next(c)
		}
	}
}

// isLight reports whether a theme id belongs to the light family, so a light
// cookie cannot select a dark theme for the light slot and the reverse.
func isLight(theme string) bool {
	return len(theme) >= 5 && theme[:5] == "light"
}

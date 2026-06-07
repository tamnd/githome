// Package view builds the view models the render layer turns into HTML. It maps
// the domain types (a Repo, a User, a list of commits) into flat, presentation
// shaped structs with no behavior, and it owns the request-scoped contract the
// middleware fills in: the signed-in viewer, the color mode, the CSRF token and
// the one-shot flash messages. render renders a view model; view never renders.
// The import direction is one way: webmw and the handlers import view, view
// imports neither. See implementation/03 and implementation/06.
package view

import (
	"context"

	"github.com/go-mizu/mizu"
)

// Chrome is the shell view model shared by every full page: the title, the site
// name, the active color mode, the signed-in viewer (nil when anonymous), the
// CSRF token for forms in the shell, the current path for a sign-in return, and
// any flash messages to show once. A page view model embeds a Chrome so the
// layout renders the same shell around any content. Its field names are the
// contract the base layout reads; render keeps a structural fallback that mirrors
// them.
type Chrome struct {
	Title       string
	SiteName    string
	ColorMode   ColorMode
	Viewer      *Viewer
	CSRFToken   string
	CurrentPath string
	Flashes     []Flash
}

// ColorMode is the trio the html element carries so CSS alone picks the theme.
// Mode is auto, light or dark; Light and Dark name the theme used under each. The
// values are validated by the color-mode middleware, so an unknown value never
// reaches a template.
type ColorMode struct {
	Mode  string
	Light string
	Dark  string
}

// Viewer is the signed-in user as the shell needs them: enough to render the
// avatar menu and link to their profile, nothing more.
type Viewer struct {
	Login     string
	Name      string
	AvatarURL string
	SiteAdmin bool
}

// Flash is one server-set message shown once. Kind is success, error or info and
// maps to a CSS modifier.
type Flash struct {
	Kind    string
	Message string
}

// The request-scoped values the middleware sets and the builder reads. view owns
// these keys so the set side (webmw) and the read side (this package) agree
// without a circular import. Each From accessor returns a usable zero value when
// nothing was set, so a handler reached without the middleware still renders.

type ctxKey int

const (
	keyViewer ctxKey = iota
	keyColorMode
	keyCSRF
	keyFlashes
)

// WithViewer stores the resolved viewer (nil for anonymous) on the context.
func WithViewer(ctx context.Context, v *Viewer) context.Context {
	return context.WithValue(ctx, keyViewer, v)
}

// ViewerFrom returns the stored viewer, or nil when anonymous or unset.
func ViewerFrom(ctx context.Context) *Viewer {
	v, _ := ctx.Value(keyViewer).(*Viewer)
	return v
}

// WithColorMode stores the validated color mode on the context.
func WithColorMode(ctx context.Context, m ColorMode) context.Context {
	return context.WithValue(ctx, keyColorMode, m)
}

// ColorModeFrom returns the stored color mode, or the default (auto, light, dark)
// when unset.
func ColorModeFrom(ctx context.Context) ColorMode {
	m, ok := ctx.Value(keyColorMode).(ColorMode)
	if !ok || m.Mode == "" {
		return DefaultColorMode()
	}
	return m
}

// DefaultColorMode follows the operating system with the stock light and dark
// themes, which needs no cookie and no JavaScript.
func DefaultColorMode() ColorMode {
	return ColorMode{Mode: "auto", Light: "light", Dark: "dark"}
}

// WithCSRF stores the per-session CSRF token on the context.
func WithCSRF(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, keyCSRF, token)
}

// CSRFFrom returns the stored CSRF token, or the empty string when unset.
func CSRFFrom(ctx context.Context) string {
	t, _ := ctx.Value(keyCSRF).(string)
	return t
}

// WithFlashes stores the flash messages drained for this request.
func WithFlashes(ctx context.Context, f []Flash) context.Context {
	return context.WithValue(ctx, keyFlashes, f)
}

// FlashesFrom returns the stored flashes, or nil when none.
func FlashesFrom(ctx context.Context) []Flash {
	f, _ := ctx.Value(keyFlashes).([]Flash)
	return f
}

// Builder assembles a Chrome from the request context plus the static site
// configuration. One Builder is constructed at boot and shared; it reads only the
// request, so it is safe for concurrent use.
type Builder struct {
	siteName string
}

// NewBuilder returns a Builder for the given site name (shown in the title and
// the header). An empty name falls back to Githome.
func NewBuilder(siteName string) *Builder {
	if siteName == "" {
		siteName = "Githome"
	}
	return &Builder{siteName: siteName}
}

// Chrome builds the shell model for a request, reading the viewer, color mode,
// CSRF token and flashes the middleware placed on the context. title is the page
// title; an empty title renders just the site name.
func (b *Builder) Chrome(c *mizu.Ctx, title string) Chrome {
	ctx := c.Context()
	return Chrome{
		Title:       title,
		SiteName:    b.siteName,
		ColorMode:   ColorModeFrom(ctx),
		Viewer:      ViewerFrom(ctx),
		CSRFToken:   CSRFFrom(ctx),
		CurrentPath: c.Request().URL.RequestURI(),
		Flashes:     FlashesFrom(ctx),
	}
}

// SiteName returns the configured site name, for the rare caller that needs it
// outside a Chrome.
func (b *Builder) SiteName() string { return b.siteName }

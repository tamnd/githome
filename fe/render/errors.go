package render

import (
	"bytes"
	"net/http"

	"github.com/go-mizu/mizu"
)

// The error helpers render the front's error pages off a single template
// (errors/error). Each sets the right status and a short, privacy-preserving
// message. They take the same view model shape as any page so the shell (header,
// footer, theme) renders around the error exactly as it does around content. See
// implementation/03 section 6.

// errorVM is the view model the error template renders. Chrome carries the shell
// fields (title, color mode, viewer) so an error page is a full, themed page; the
// view layer fills it in. It is an interface-free struct: render owns it because
// render owns the template.
type errorVM struct {
	Chrome any
	Status int
	Title  string
	Detail string
}

// fallbackColorMode and fallbackShell mirror the field names of the view layer's
// Chrome so the base layout renders against either by structure. render keeps its
// own minimal copy rather than importing the view package, which preserves the
// one-way import boundary (view imports render, never the reverse). It is used
// only when an error happens before the view layer built real chrome.
type fallbackColorMode struct {
	Mode  string
	Light string
	Dark  string
}

type fallbackShell struct {
	Title       string
	SiteName    string
	ColorMode   fallbackColorMode
	Viewer      any
	CSRFToken   string
	CurrentPath string
	Flashes     []any
	HideAuth    bool
}

// fallbackChrome is a sane default shell: auto color mode following the OS with
// the stock light and dark themes, no signed-in viewer, no flashes.
func fallbackChrome() fallbackShell {
	return fallbackShell{
		SiteName:  "Githome",
		ColorMode: fallbackColorMode{Mode: "auto", Light: "light", Dark: "dark"},
	}
}

// errorChrome lets the caller hand render a minimal chrome value without render
// importing the view package. The view layer's Chrome satisfies it; in a context
// with no chrome (a failure before the view layer ran) a nil is fine and the
// template falls back to bare defaults.
func (s *Set) renderError(c *mizu.Ctx, status int, title, detail string, chrome any) error {
	if chrome == nil {
		chrome = fallbackChrome()
	}
	vm := errorVM{Chrome: chrome, Status: status, Title: title, Detail: detail}
	var buf bytes.Buffer
	if err := s.execTemplate(&buf, "errors/error", "base", vm); err != nil {
		// The error template itself failed: fall back to plain text so the user
		// still gets the status and we do not recurse.
		return c.Bytes(status, []byte(title+"\n"), "text/plain; charset=utf-8")
	}
	return c.Bytes(status, buf.Bytes(), "text/html; charset=utf-8")
}

// NotFound renders the 404 page. Githome returns 404 for a missing resource and
// for a private one the viewer may not see, so the same page covers both and
// never reveals that a private resource exists. See implementation/06.
func (s *Set) NotFound(c *mizu.Ctx) error {
	return s.NotFoundWithChrome(c, nil)
}

// NotFoundWithChrome is NotFound with the shell pre-filled, used once the view
// layer has built chrome for the request.
func (s *Set) NotFoundWithChrome(c *mizu.Ctx, chrome any) error {
	return s.renderError(c, http.StatusNotFound,
		"This is not the web page you are looking for.",
		"It may have been moved, deleted, or never existed. If you reached it from a link, the link is out of date.",
		chrome)
}

// RepoNotFound is the 404 used for a repository that is missing or private to the
// viewer. It is a distinct entry point so the message can speak to repositories
// while staying identical for the missing and the forbidden case.
func (s *Set) RepoNotFound(c *mizu.Ctx, chrome any) error {
	return s.renderError(c, http.StatusNotFound,
		"This is not the web page you are looking for.",
		"The repository may have been renamed, moved, deleted, or made private.",
		chrome)
}

// MethodNotAllowed renders the 405 page. The mux computed the method mismatch
// and set the Allow header before this runs; the page replaces only the
// plain-text body, so the header contract (spec §7.4) stays intact. It renders
// with the fallback chrome because the route's middleware never ran.
func (s *Set) MethodNotAllowed(c *mizu.Ctx) error {
	return s.renderError(c, http.StatusMethodNotAllowed,
		"That method is not allowed here.",
		"This address exists but does not answer the HTTP method the request used. The Allow header lists the ones it does.",
		nil)
}

// ServerError renders the 500 page. The underlying error is logged by the
// recover/middleware layer, not shown to the user. It returns nil because the
// response is fully written here; returning the original error would let an outer
// handler write a second time.
func (s *Set) ServerError(c *mizu.Ctx, _ error) error {
	return s.renderError(c, http.StatusInternalServerError,
		"Something went wrong on our end.",
		"The error has been logged. Try again in a moment.",
		nil)
}

// Forbidden renders the 403 page, used only where existence is already public so
// hiding the resource behind a 404 would be misleading (for example an action the
// signed-in viewer is not allowed to take on a repository they can see).
func (s *Set) Forbidden(c *mizu.Ctx, chrome any) error {
	return s.renderError(c, http.StatusForbidden,
		"You are not allowed to do that.",
		"Your account does not have permission for this action.",
		chrome)
}

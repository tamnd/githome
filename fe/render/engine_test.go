package render

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/assets"
)

// These tests are the rendered-content arm of the oracle for the F0 shell: they
// render the real templates against the real embedded asset manifest and pin the
// invariants the spec promises, the no-flash theme attributes, the resolved
// hashed asset URLs, the signed-in versus anonymous shell, the htmx fragment
// contract, and the themed error pages. See implementation/15 sections 2 and 3.

// tChrome mirrors the field names the base layout reads. The test defines its own
// shape rather than importing fe/view so the render package's tests stay
// independent of the view package; html/template binds by field name, so a
// structurally matching struct renders identically.
type tChrome struct {
	Title       string
	SiteName    string
	ColorMode   tColorMode
	Viewer      *tViewer
	CSRFToken   string
	CurrentPath string
	Flashes     []tFlash
	HideAuth    bool
}

type tColorMode struct{ Mode, Light, Dark string }
type tViewer struct{ Login, Name, AvatarURL string }
type tFlash struct{ Kind, Message string }

type tHome struct{ Chrome tChrome }

func newTestSet(t *testing.T) *Set {
	t.Helper()
	s, err := New(assets.FS(), false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func newCtx(method, target string) (*mizu.Ctx, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, nil)
	return mizu.NewCtx(rec, req, nil), rec
}

func anonChrome() tChrome {
	return tChrome{
		SiteName:  "Githome",
		ColorMode: tColorMode{Mode: "auto", Light: "light", Dark: "dark"},
	}
}

func TestPageHomeAnonymous(t *testing.T) {
	s := newTestSet(t)
	c, rec := newCtx(http.MethodGet, "/")
	if err := s.Page(c, "home/index", tHome{Chrome: anonChrome()}); err != nil {
		t.Fatalf("Page: %v", err)
	}
	body := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	mustContain(t, body, "<!DOCTYPE html>")
	// No-flash theme attributes drive the active theme from CSS alone.
	mustContain(t, body, `data-color-mode="auto"`)
	mustContain(t, body, `data-light-theme="light"`)
	mustContain(t, body, `data-dark-theme="dark"`)
	// The asset URL is the content-hashed path from the manifest, not the logical
	// name, so a cache-busting deploy never serves a stale bundle.
	mustContain(t, body, `href="/assets/app.`)
	mustContain(t, body, `.css"`)
	mustContain(t, body, `src="/assets/app.`)
	// Anonymous viewers see the sign-in call to action, not the avatar menu.
	mustContain(t, body, "Sign in")
	if strings.Contains(body, "Sign out") {
		t.Error("anonymous page must not show the sign-out control")
	}
}

func TestPageHomeSignedIn(t *testing.T) {
	s := newTestSet(t)
	ch := anonChrome()
	ch.Viewer = &tViewer{Login: "octocat"}
	ch.CSRFToken = "tok123"
	c, rec := newCtx(http.MethodGet, "/")
	if err := s.Page(c, "home/index", tHome{Chrome: ch}); err != nil {
		t.Fatalf("Page: %v", err)
	}
	body := rec.Body.String()

	mustContain(t, body, "Welcome back, octocat")
	mustContain(t, body, "Sign out")
	// The sign-out form carries the CSRF token as a hidden field, so it works with
	// scripting off.
	mustContain(t, body, `name="_csrf" value="tok123"`)
	// The signed-in header links to the viewer's profile.
	mustContain(t, body, `href="/octocat"`)
}

func TestFragmentOmitsLayout(t *testing.T) {
	s := newTestSet(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("HX-Request", "true")
	c := mizu.NewCtx(rec, req, nil)

	if err := s.Page(c, "home/index", tHome{Chrome: anonChrome()}); err != nil {
		t.Fatalf("Page: %v", err)
	}
	body := rec.Body.String()

	// A fragment is the content only: no document shell, but the content is there.
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("htmx fragment must not include the document shell")
	}
	if strings.Contains(body, "<header") {
		t.Error("htmx fragment must not include the app header")
	}
	mustContain(t, body, "blankslate")
}

func TestNotFoundIsThemedAnd404(t *testing.T) {
	s := newTestSet(t)
	c, rec := newCtx(http.MethodGet, "/does-not-exist")
	if err := s.NotFound(c); err != nil {
		t.Fatalf("NotFound: %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	// The error page is a full, themed page (fallback chrome) so it keeps the
	// shell and the no-flash theme attributes.
	mustContain(t, body, "<!DOCTYPE html>")
	mustContain(t, body, `data-color-mode="auto"`)
	mustContain(t, body, "404")
	// The 404 copy is deliberately identical for missing and private resources.
	mustContain(t, body, "not the web page you are looking for")
}

func TestServerErrorIs500(t *testing.T) {
	s := newTestSet(t)
	c, rec := newCtx(http.MethodGet, "/boom")
	if err := s.ServerError(c, nil); err != nil {
		t.Fatalf("ServerError: %v", err)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	mustContain(t, rec.Body.String(), "500")
}

func TestOcticonKnownAndMissing(t *testing.T) {
	known := string(octicon("mark-github", 16))
	if !strings.Contains(known, "<svg") || !strings.Contains(known, "viewBox=\"0 0 16 16\"") {
		t.Errorf("known icon should render an svg: %q", known)
	}
	if !strings.Contains(known, assets.Icons["mark-github"]) {
		t.Error("known icon should embed its registered path body")
	}

	// An unknown icon renders a visible placeholder naming the miss, never an
	// empty string or the raw input, so a typo is caught in review.
	missing := string(octicon("no-such-icon", 16))
	if !strings.Contains(missing, "octicon-missing") || !strings.Contains(missing, "no-such-icon") {
		t.Errorf("missing icon should render a labeled placeholder: %q", missing)
	}

	// A zero size falls back to 16 rather than rendering a zero-sized glyph.
	if !strings.Contains(string(octicon("repo", 0)), `width="16"`) {
		t.Error("zero size should fall back to 16")
	}
}

func TestColorModeAttrsEscapes(t *testing.T) {
	got := string(colorModeAttrs("auto", "light", `da"rk`))
	if strings.Contains(got, `da"rk`) {
		t.Errorf("attribute value must be escaped: %q", got)
	}
}

func TestIsFragment(t *testing.T) {
	// Header form.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("HX-Request", "true")
	if !IsFragment(mizu.NewCtx(httptest.NewRecorder(), req, nil)) {
		t.Error("HX-Request: true should be a fragment")
	}
	// Query form.
	c, _ := newCtx(http.MethodGet, "/?_fragment=1")
	if !IsFragment(c) {
		t.Error("?_fragment=1 should be a fragment")
	}
	// Plain navigation is a full page.
	c, _ = newCtx(http.MethodGet, "/")
	if IsFragment(c) {
		t.Error("a plain request should render the full page")
	}
}

func mustContain(t *testing.T, body, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Errorf("rendered output missing %q", want)
	}
}

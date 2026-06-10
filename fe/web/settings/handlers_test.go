package settings

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/assets"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

// recordedFlash is one staged flash the fake store captured, so a test asserts on
// the outcome notice a handler reported without a cookie round-trip.
type recordedFlash struct {
	kind    string
	message string
}

// fakeFlash is a Flasher that records what it was asked to stage. The real
// webmw.Flash signs a cookie; the settings handlers only need the Add side, so the
// fake keeps the test on the handler's behavior rather than the cookie codec.
type fakeFlash struct {
	added []recordedFlash
}

func (f *fakeFlash) Add(_ *mizu.Ctx, kind, message string) {
	f.added = append(f.added, recordedFlash{kind: kind, message: message})
}

// last returns the most recent flash, or a zero value when none was staged.
func (f *fakeFlash) last() recordedFlash {
	if len(f.added) == 0 {
		return recordedFlash{}
	}
	return f.added[len(f.added)-1]
}

// fixture is the settings web test harness: a live httptest server mounting the
// account settings handlers with a fake flash store, plus the optional viewer the
// chain runs as. The settings tree has no domain backing, so the harness needs no
// store; it only wires the render set, the view builder, and a viewer-injecting
// middleware that stands in for the session layer.
type fixture struct {
	srv   *httptest.Server
	flash *fakeFlash
}

// newFixture mounts the settings handlers as a signed-in viewer when viewer is
// non-nil, or anonymous when it is nil, so one harness drives both the gated and
// the ungated assertions. The color-mode middleware runs first, exactly as the
// real front mounts it, so the appearance form prefills from request cookies.
func newFixture(t *testing.T, viewer *view.Viewer) fixture {
	t.Helper()

	renderSet, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	flash := &fakeFlash{}

	h := New(Deps{
		Render: renderSet,
		View:   view.NewBuilder("Githome"),
		Flash:  flash,
		Logger: discard,
	})

	root := mizu.NewRouter()
	// inject stands in for the session middleware: it puts the test's viewer on the
	// context the handlers read, so a non-nil viewer is signed in and a nil one is
	// anonymous.
	inject := func(next mizu.Handler) mizu.Handler {
		return func(c *mizu.Ctx) error {
			r := c.Request()
			*r = *r.WithContext(view.WithViewer(r.Context(), viewer))
			return next(c)
		}
	}
	page := root.With(webmw.ColorMode()).With(inject)
	page.Get("/settings", h.Index)
	page.Get("/settings/appearance", h.Appearance)
	page.Post("/settings/appearance", h.SaveAppearance)

	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	return fixture{srv: srv, flash: flash}
}

// signedIn is the viewer the gated tests run as.
func signedIn() *view.Viewer {
	return &view.Viewer{Login: "octocat", Name: "The Octocat"}
}

// noRedirectClient issues requests without following redirects, so a test sees the
// 303 a save returns rather than the page it lands on.
func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// get issues a no-redirect GET and returns the response and body.
func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	resp, err := noRedirectClient().Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp, string(b)
}

// postForm issues a no-redirect POST of form values and returns the response.
func postForm(t *testing.T, srv *httptest.Server, path string, form url.Values) *http.Response {
	t.Helper()
	resp, err := noRedirectClient().PostForm(srv.URL+path, form)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp
}

// cookie returns the named Set-Cookie from a response, or nil when it is absent.
func cookie(resp *http.Response, name string) *http.Cookie {
	for _, ck := range resp.Cookies() {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}

func TestAppearanceRendersForm(t *testing.T) {
	fx := newFixture(t, signedIn())
	resp, body := get(t, fx.srv, "/settings/appearance")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// The form posts to the appearance route and carries the three preference
	// controls.
	for _, want := range []string{
		`action="/settings/appearance"`,
		`name="mode"`,
		`name="light_theme"`,
		`name="dark_theme"`,
		"Update preferences",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("appearance form is missing %q:\n%s", want, body)
		}
	}
	// The sidebar heading links to the viewer's profile, and the Appearance entry is
	// marked current.
	if !strings.Contains(body, `href="/octocat"`) {
		t.Errorf("settings nav is missing the profile heading link:\n%s", body)
	}
	if !strings.Contains(body, `aria-current="page"`) {
		t.Errorf("settings nav is missing the active-section marker:\n%s", body)
	}
}

func TestAppearancePrefillsFromCookies(t *testing.T) {
	fx := newFixture(t, signedIn())
	req, err := http.NewRequest(http.MethodGet, fx.srv.URL+"/settings/appearance", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	// The color-mode middleware reads these cookies, so the form opens on the
	// viewer's saved choice rather than the default.
	req.AddCookie(&http.Cookie{Name: "color_mode", Value: "dark"})
	req.AddCookie(&http.Cookie{Name: "dark_theme", Value: "dark_dimmed"})
	resp, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatalf("GET appearance: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if !strings.Contains(body, `value="dark" checked`) {
		t.Errorf("mode radio did not prefill dark:\n%s", body)
	}
	if !strings.Contains(body, `value="dark_dimmed" selected`) {
		t.Errorf("dark theme select did not prefill dark_dimmed:\n%s", body)
	}
}

func TestSaveAppearanceSetsCookiesAndRedirects(t *testing.T) {
	fx := newFixture(t, signedIn())
	resp := postForm(t, fx.srv, "/settings/appearance", url.Values{
		"mode":        {"dark"},
		"light_theme": {"light"},
		"dark_theme":  {"dark_dimmed"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/settings/appearance" {
		t.Errorf("redirect Location = %q, want /settings/appearance", got)
	}
	// All three preference cookies are written with the chosen values.
	for name, want := range map[string]string{
		"color_mode":  "dark",
		"light_theme": "light",
		"dark_theme":  "dark_dimmed",
	} {
		ck := cookie(resp, name)
		if ck == nil {
			t.Errorf("save did not set the %s cookie", name)
			continue
		}
		if ck.Value != want {
			t.Errorf("%s cookie = %q, want %q", name, ck.Value, want)
		}
	}
	if got := fx.flash.last(); got.kind != "success" {
		t.Errorf("flash kind = %q, want success", got.kind)
	}
}

func TestSaveAppearanceRejectsUnknownTheme(t *testing.T) {
	fx := newFixture(t, signedIn())
	resp := postForm(t, fx.srv, "/settings/appearance", url.Values{
		"mode":        {"dark"},
		"light_theme": {"light"},
		"dark_theme":  {"not_a_real_theme"},
	})
	// A value outside the catalog is a forged post: it redirects with an error flash
	// and writes no cookie, so the preference is never poisoned.
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", resp.StatusCode)
	}
	if cookie(resp, "dark_theme") != nil {
		t.Errorf("save wrote a cookie for an invalid theme")
	}
	if got := fx.flash.last(); got.kind != "error" {
		t.Errorf("flash kind = %q, want error", got.kind)
	}
}

func TestIndexRedirectsToProfile(t *testing.T) {
	fx := newFixture(t, signedIn())
	resp, _ := get(t, fx.srv, "/settings")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/settings/profile" {
		t.Errorf("redirect Location = %q, want /settings/profile", got)
	}
}

func TestAnonymousBouncesToLogin(t *testing.T) {
	fx := newFixture(t, nil)
	// Every settings route bounces an anonymous request to the sign-in form,
	// with return_to carrying the page it wanted so a successful sign-in lands
	// back on it. The surface is function-private, not secret, so the bounce
	// confirms nothing.
	for _, path := range []string{"/settings", "/settings/appearance"} {
		resp, _ := get(t, fx.srv, path)
		if resp.StatusCode != http.StatusFound {
			t.Errorf("anonymous GET %s = %d, want 302", path, resp.StatusCode)
			continue
		}
		want := "/login?return_to=" + url.QueryEscape(path)
		if got := resp.Header.Get("Location"); got != want {
			t.Errorf("anonymous GET %s Location = %q, want %q", path, got, want)
		}
	}
	resp := postForm(t, fx.srv, "/settings/appearance", url.Values{"mode": {"dark"}})
	if resp.StatusCode != http.StatusFound {
		t.Errorf("anonymous POST appearance = %d, want 302", resp.StatusCode)
	}
	// The bounce happens before any work: no flash staged, no cookie written.
	if len(fx.flash.added) != 0 {
		t.Errorf("anonymous post staged a flash: %+v", fx.flash.added)
	}
	if cookie(resp, "color_mode") != nil {
		t.Errorf("anonymous post wrote a preference cookie")
	}
}

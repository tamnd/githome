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
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

// sectionFixture mounts the stubbed account-settings sections and the
// tokens/new mint form as the given viewer (nil for anonymous), so the tests
// drive both the gated bounce and the rendered stub.
func sectionFixture(t *testing.T, viewer *view.Viewer) *httptest.Server {
	t.Helper()
	renderSet, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	h := New(Deps{
		Render: renderSet,
		View:   view.NewBuilder("Githome"),
		Flash:  &fakeFlash{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	inject := func(next mizu.Handler) mizu.Handler {
		return func(c *mizu.Ctx) error {
			r := c.Request()
			*r = *r.WithContext(view.WithViewer(r.Context(), viewer))
			return next(c)
		}
	}
	root := mizu.NewRouter()
	page := root.With(webmw.ColorMode()).With(inject)
	page.Get("/settings/tokens/new", h.NewToken)
	for _, sec := range AccountSections() {
		page.Get(sec.Path, h.Section(sec))
	}
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)
	return srv
}

// TestSectionStubsRenderInChrome shows each stubbed section resolves to a 200
// page inside the settings nav rather than a site-wide 404, carrying its title
// and the honest-absence message.
func TestSectionStubsRenderInChrome(t *testing.T) {
	srv := sectionFixture(t, signedIn())
	cases := []struct {
		path  string
		title string
	}{
		{route.SettingsNotifications(), "Notification settings"},
		{route.SettingsEmails(), "Email settings"},
		{route.SettingsSecurity(), "Password and authentication"},
		{route.SettingsOrganizations(), "Organizations"},
		{route.SettingsApplications(), "Authorized applications"},
		{route.SettingsDevelopers(), "Developer settings"},
	}
	for _, tc := range cases {
		resp, body := get(t, srv, tc.path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s status %d, want 200", tc.path, resp.StatusCode)
			continue
		}
		if !strings.Contains(body, tc.title) {
			t.Errorf("%s missing title %q", tc.path, tc.title)
		}
		// The stub sits inside the settings chrome, so the nav is present.
		if !strings.Contains(body, "settings-nav") {
			t.Errorf("%s not rendered inside the settings nav", tc.path)
		}
		if !strings.Contains(body, "not available yet") {
			t.Errorf("%s missing the honest-absence message", tc.path)
		}
	}
}

// TestSectionNavLinksBackedAndStubbed shows the sidebar links the nav-backed
// stub sections alongside the backed ones, so the settings tree reads like
// github.com's.
func TestSectionNavLinksBackedAndStubbed(t *testing.T) {
	srv := sectionFixture(t, signedIn())
	_, body := get(t, srv, route.SettingsSecurity())
	for _, want := range []string{
		`href="/settings/profile"`,
		`href="/settings/appearance"`,
		`href="/settings/notifications"`,
		`href="/settings/emails"`,
		`href="/settings/security"`,
		`href="/settings/keys"`,
		`href="/settings/organizations"`,
		`href="/settings/tokens"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings nav missing %s", want)
		}
	}
}

// TestSectionAnonymousBounces shows an anonymous request to a stubbed section
// bounces to the sign-in form with return_to, the function-private behavior the
// rest of the settings tree takes.
func TestSectionAnonymousBounces(t *testing.T) {
	srv := sectionFixture(t, nil)
	resp, _ := get(t, srv, route.SettingsEmails())
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status %d, want 302", resp.StatusCode)
	}
	want := "/login?return_to=" + url.QueryEscape(route.SettingsEmails())
	if loc := resp.Header.Get("Location"); loc != want {
		t.Errorf("Location = %q, want %q", loc, want)
	}
}

// TestTokensNewRendersMintForm shows /settings/tokens/new renders the mint form
// even with no token service wired (the honest-absence stub still shows the
// form's framing), the dedicated create page github.com links to.
func TestTokensNewRendersMintForm(t *testing.T) {
	srv := sectionFixture(t, signedIn())
	resp, body := get(t, srv, route.SettingsTokenNew())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Personal access tokens") {
		t.Errorf("tokens/new missing the tokens page title:\n%s", body)
	}
}

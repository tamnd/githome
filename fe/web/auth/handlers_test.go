package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-mizu/mizu"
	"golang.org/x/crypto/bcrypt"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/assets"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/store"
)

func TestSafeReturn(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// The good shapes: a plain absolute path, with or without a query.
		{"", "/"},
		{"/", "/"},
		{"/octocat/hello", "/octocat/hello"},
		{"/octocat/hello?tab=readme", "/octocat/hello?tab=readme"},
		// Anything carrying a scheme or relative form is out.
		{"https://evil.example", "/"},
		{"javascript:alert(1)", "/"},
		{"relative/path", "/"},
		// Protocol-relative and its backslash disguises. Browsers treat \ after
		// the authority cut like /, so each of these can leave the origin.
		{"//evil.example", "/"},
		{`/\evil.example`, "/"},
		{`\/evil.example`, "/"},
		{`\\evil.example`, "/"},
		{`/\\evil.example`, "/"},
		// Encoded backslashes, either case, anywhere in the URL.
		{"/%5Cevil.example", "/"},
		{"/%5cevil.example", "/"},
		{"/%5C%5Cevil.example", "/"},
		{"/ok/%5Cpath", "/"},
		// A raw backslash later in the path is rejected too.
		{`/ok\evil`, "/"},
	}
	for _, tc := range cases {
		if got := safeReturn(tc.in); got != tc.want {
			t.Errorf("safeReturn(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// authServer mounts the login and join handlers over a real sqlite store, the
// way fe.Mount does, with one seeded user (octocat / "correct horse").
func authServer(t *testing.T) *httptest.Server {
	t.Helper()
	ctx := context.Background()

	st, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "githome.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("correct horse"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertUserWithPassword(ctx, "octocat", "octo@example.com", string(hash)); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	renderSet, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	users := domain.NewUserService(st)
	lookup := func(ctx context.Context, pk int64) (*view.Viewer, error) {
		u, err := users.Viewer(ctx, pk)
		if err != nil {
			if errors.Is(err, domain.ErrUserNotFound) {
				return nil, nil
			}
			return nil, err
		}
		return &view.Viewer{Login: u.Login}, nil
	}
	sessions := webmw.NewSessions([]byte("0123456789abcdef0123456789abcdef"), 0, lookup)
	h := New(Deps{
		Store:    st,
		Sessions: sessions,
		View:     view.NewBuilder("Githome"),
		Render:   renderSet,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	root := mizu.NewRouter()
	page := root.With(sessions.Middleware(), webmw.ColorMode())
	page.Get("/login", h.LoginForm)
	page.Post("/login/session", h.LoginSubmit)
	page.Get("/join", h.JoinForm)
	page.Post("/join", h.JoinSubmit)
	page.Get("/logout", h.LogoutForm)
	page.Post("/logout/session", h.LogoutSubmit)
	// The github.com-shaped aliases mountAuth also registers.
	page.Post("/session", h.LoginSubmit)
	page.Post("/logout", h.LogoutSubmit)
	page.Get("/signup", h.JoinForm)

	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)
	return srv
}

// TestSessionAliasSignsIn checks the github.com-shaped POST /session accepts the
// same credentials and issues a session, exactly like /login/session.
func TestSessionAliasSignsIn(t *testing.T) {
	srv := authServer(t)
	resp, _ := postForm(t, srv, "/session", url.Values{
		"login":    {"octocat"},
		"password": {"correct horse"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", resp.StatusCode)
	}
	if !hasSessionCookie(resp) {
		t.Error("POST /session did not issue a session cookie")
	}
}

// TestLogoutAliasClearsSession checks the github.com-shaped POST /logout clears
// the session, like /logout/session.
func TestLogoutAliasClearsSession(t *testing.T) {
	srv := authServer(t)
	login, _ := postForm(t, srv, "/session", url.Values{
		"login":    {"octocat"},
		"password": {"correct horse"},
	})
	if !hasSessionCookie(login) {
		t.Fatal("precondition: sign-in did not set a session cookie")
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/logout", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, ck := range login.Cookies() {
		req.AddCookie(ck)
	}
	resp, err := noFollow().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", resp.StatusCode)
	}
	if !clearsSessionCookie(resp) {
		t.Error("POST /logout did not clear the session cookie")
	}
}

// TestSignupAliasRendersJoin checks GET /signup serves the same sign-up form as
// /join.
func TestSignupAliasRendersJoin(t *testing.T) {
	srv := authServer(t)
	resp, err := noFollow().Get(srv.URL + "/signup")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(b), `name="email"`) {
		t.Errorf("/signup did not render the join form:\n%s", b)
	}
}

// TestJoinReturnTo checks the sign-up form carries a safe return_to through to
// the post-join redirect, and falls back to / for an open-redirect attempt.
func TestJoinReturnTo(t *testing.T) {
	srv := authServer(t)

	// The GET form embeds a safe return_to as a hidden field.
	resp, err := noFollow().Get(srv.URL + "/join?return_to=/octocat/hello")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(b), `name="return_to" value="/octocat/hello"`) {
		t.Errorf("join form did not carry return_to as a hidden field:\n%s", b)
	}

	// A successful sign-up lands on the safe return_to.
	resp2, _ := postForm(t, srv, "/join", url.Values{
		"login":     {"newbie"},
		"email":     {"newbie@example.com"},
		"password":  {"long enough password"},
		"return_to": {"/octocat/hello"},
	})
	if loc := resp2.Header.Get("Location"); loc != "/octocat/hello" {
		t.Errorf("join Location = %q, want /octocat/hello", loc)
	}

	// An open-redirect return_to falls back to /.
	resp3, _ := postForm(t, srv, "/join", url.Values{
		"login":     {"newbie2"},
		"email":     {"newbie2@example.com"},
		"password":  {"long enough password"},
		"return_to": {"https://evil.example/x"},
	})
	if loc := resp3.Header.Get("Location"); loc != "/" {
		t.Errorf("join open-redirect Location = %q, want /", loc)
	}
}

// hasSessionCookie reports whether the response sets a non-empty session cookie.
func hasSessionCookie(resp *http.Response) bool {
	for _, ck := range resp.Cookies() {
		if ck.Name == webmw.DefaultSessionCookie && ck.Value != "" {
			return true
		}
	}
	return false
}

// clearsSessionCookie reports whether the response expires the session cookie.
func clearsSessionCookie(resp *http.Response) bool {
	for _, ck := range resp.Cookies() {
		if ck.Name == webmw.DefaultSessionCookie && (ck.Value == "" || ck.MaxAge < 0) {
			return true
		}
	}
	return false
}

func noFollow() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// postForm posts urlencoded form values without following redirects and
// returns the response and its body.
func postForm(t *testing.T, srv *httptest.Server, path string, form url.Values) (*http.Response, string) {
	t.Helper()
	resp, err := noFollow().PostForm(srv.URL+path, form)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp, string(b)
}

func TestJoinSubmitReservedLogin(t *testing.T) {
	srv := authServer(t)
	// Reserved names are rejected case-insensitively, before any uniqueness
	// probe, so the profile URL can never be shadowed by a route the front owns.
	for _, login := range []string{"settings", "Settings", "marketplace", "api"} {
		resp, body := postForm(t, srv, "/join", url.Values{
			"login":    {login},
			"email":    {"new@example.com"},
			"password": {"long enough password"},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("join %q: status %d, want a re-rendered form", login, resp.StatusCode)
		}
		if !strings.Contains(body, "This name is reserved.") {
			t.Errorf("join %q: form is missing the reserved-name error:\n%s", login, body)
		}
	}
}

func TestJoinSubmitAcceptsOrdinaryLogin(t *testing.T) {
	srv := authServer(t)
	resp, _ := postForm(t, srv, "/join", url.Values{
		"login":    {"hubber"},
		"email":    {"hubber@example.com"},
		"password": {"long enough password"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("join hubber: status %d, want 303", resp.StatusCode)
	}
}

func TestLoginSubmitReturnTo(t *testing.T) {
	srv := authServer(t)
	cases := []struct {
		name     string
		returnTo string
		want     string
	}{
		{"same-origin path survives", "/octocat/hello", "/octocat/hello"},
		{"protocol-relative falls back", "//evil.example", "/"},
		{"backslash variant falls back", `/\evil.example`, "/"},
		{"encoded backslash falls back", "/%5Cevil.example", "/"},
		{"absolute URL falls back", "https://evil.example/x", "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := postForm(t, srv, "/login/session", url.Values{
				"login":     {"octocat"},
				"password":  {"correct horse"},
				"return_to": {tc.returnTo},
			})
			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("status %d, want 303", resp.StatusCode)
			}
			if loc := resp.Header.Get("Location"); loc != tc.want {
				t.Errorf("Location = %q, want %q", loc, tc.want)
			}
		})
	}
}

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

	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)
	return srv
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

package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/config"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/jsondiff"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// authConfig is testConfig plus the resolved base URLs the presenter needs to
// build a user's links.
func authConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := testConfig()
	mustURL := func(s string) *url.URL {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return u
	}
	cfg.URLs = config.URLs{
		API:     mustURL("https://git.test.internal/api/v3"),
		HTML:    mustURL("https://git.test.internal"),
		GraphQL: mustURL("https://git.test.internal/api/graphql"),
		SSHHost: "git.test.internal",
		SSHPort: 22,
	}
	return cfg
}

// authServer opens a fresh SQLite store, seeds one user with a classic PAT, and
// mounts the full authenticated REST surface over it. It returns the server and
// the plaintext token for that user.
func authServer(t *testing.T) (*httptest.Server, string) {
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

	hireable := true
	name, company := "The Octocat", "Acme"
	location, email := "San Francisco", "octocat@example.com"
	bio, twitter := "There once was...", "monatheoctocat"
	u := &store.UserRow{
		Login: "octocat", Type: "User",
		Name: &name, Company: &company, Blog: "https://git.test.internal/blog",
		Location: &location, Email: &email, Hireable: &hireable,
		Bio: &bio, TwitterUsername: &twitter,
		PublicRepos: 2, PublicGists: 1, Followers: 20, Following: 0,
	}
	if err := st.InsertUser(ctx, u); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	g, err := auth.GenerateToken(auth.PrefixClassicPAT)
	if err != nil {
		t.Fatal(err)
	}
	hash := g.Hash
	if err := st.InsertToken(ctx, &store.TokenRow{
		UserPK:      &u.PK,
		TokenHash:   hash[:],
		TokenPrefix: auth.PrefixClassicPAT,
		LastEight:   g.Last8,
		Kind:        "pat",
		Scopes:      "gist, repo",
	}); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	authSvc := auth.NewService(st, "https://git.test.internal")
	t.Cleanup(authSvc.Close)

	cfg := authConfig(t)
	root := mizu.NewRouter()
	Mount(root, Deps{
		Config:     cfg,
		Ready:      st,
		Auth:       authSvc,
		Users:      domain.NewUserService(st),
		URLs:       presenter.NewURLBuilder(cfg.URLs),
		NodeFormat: nodeid.FormatNew,
	})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)
	return srv, g.Plaintext
}

func authedGet(t *testing.T, srv *httptest.Server, path, authorization string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		body = append(body, buf[:n]...)
		if err != nil {
			break
		}
	}
	return resp, body
}

func TestUserContract(t *testing.T) {
	srv, token := authServer(t)

	// Both the GHES prefix and the bare root serve the authenticated viewer.
	for _, path := range []string{"/api/v3/user", "/user"} {
		resp, body := authedGet(t, srv, path, "token "+token)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d, body %s", path, resp.StatusCode, body)
		}
		jsondiff.AssertCompatible(t, golden(t, "user.golden.json"), body, jsondiff.Default("git.test.internal"))

		if got := resp.Header.Get("X-OAuth-Scopes"); got != "gist, repo" {
			t.Errorf("%s: X-OAuth-Scopes = %q, want %q", path, got, "gist, repo")
		}
		if _, ok := resp.Header["X-Accepted-Oauth-Scopes"]; !ok {
			t.Errorf("%s: X-Accepted-OAuth-Scopes header must be present", path)
		}
	}
}

func TestUserAnonymousUnauthorized(t *testing.T) {
	srv, _ := authServer(t)
	resp, body := authedGet(t, srv, "/user", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
	jsondiff.AssertCompatible(t, golden(t, "requires_authentication.golden.json"), body, jsondiff.Default("git.test.internal"))
}

func TestUserBadCredentials(t *testing.T) {
	srv, _ := authServer(t)
	resp, body := authedGet(t, srv, "/user", "token ghp_000000000000000000000000000000000000")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
	jsondiff.AssertCompatible(t, golden(t, "bad_credentials.golden.json"), body, jsondiff.Default("git.test.internal"))
}

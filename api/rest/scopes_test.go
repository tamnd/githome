package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// scopeServer mounts a REST surface seeded with owner octocat and repo hello,
// and returns a token minter so each test can pick the exact classic scopes
// its credential carries.
func scopeServer(t *testing.T) (*httptest.Server, func(scopes string) string) {
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

	owner := &store.UserRow{Login: "octocat", Type: "User"}
	if err := st.InsertUser(ctx, owner); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	repo := &store.RepoRow{OwnerPK: owner.PK, Name: "hello", DefaultBranch: "master"}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("insert repo: %v", err)
	}
	gitStore := git.NewStore(t.TempDir())
	bareTwoCommits(t, gitStore, repo.PK)

	authSvc := auth.NewService(st, "https://git.test.internal")
	t.Cleanup(authSvc.Close)

	cfg := authConfig(t)
	root := mizu.NewRouter()
	Mount(root, Deps{
		Config:     cfg,
		Ready:      st,
		Auth:       authSvc,
		Users:      domain.NewUserService(st),
		Repos:      domain.NewRepoService(st, gitStore),
		Keys:       domain.NewKeyService(st),
		URLs:       presenter.NewURLBuilder(cfg.URLs),
		NodeFormat: nodeid.FormatNew,
	})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	mint := func(scopes string) string {
		g, err := auth.GenerateToken(auth.PrefixClassicPAT)
		if err != nil {
			t.Fatal(err)
		}
		hash := g.Hash
		if err := st.InsertToken(ctx, &store.TokenRow{
			UserPK: &owner.PK, TokenHash: hash[:], TokenPrefix: auth.PrefixClassicPAT,
			LastEight: g.Last8, Kind: "pat", Scopes: scopes,
		}); err != nil {
			t.Fatalf("insert token: %v", err)
		}
		return g.Plaintext
	}
	return srv, mint
}

func TestScopeGateRefusesInsufficientToken(t *testing.T) {
	srv, mint := scopeServer(t)
	token := mint("gist")

	resp, body := authedSend(t, srv, http.MethodPatch, "/repos/octocat/hello", token,
		`{"description":"hi"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, body %s, want 403", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Accepted-OAuth-Scopes"); got != "repo, public_repo" {
		t.Errorf("X-Accepted-OAuth-Scopes = %q, want %q", got, "repo, public_repo")
	}
	if got := resp.Header.Get("X-OAuth-Scopes"); got != "gist" {
		t.Errorf("X-OAuth-Scopes = %q, want %q", got, "gist")
	}
	var env struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode body %s: %v", body, err)
	}
	if env.Message == "" {
		t.Error("403 body carries no message")
	}
}

func TestScopeGatePassesSufficientToken(t *testing.T) {
	srv, mint := scopeServer(t)
	token := mint("repo")

	resp, body := authedSend(t, srv, http.MethodPatch, "/repos/octocat/hello", token,
		`{"description":"hi"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s, want 200", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Accepted-OAuth-Scopes"); got != "repo, public_repo" {
		t.Errorf("X-Accepted-OAuth-Scopes = %q, want %q", got, "repo, public_repo")
	}
	if got := resp.Header.Get("X-OAuth-Scopes"); got != "repo" {
		t.Errorf("X-OAuth-Scopes = %q, want %q", got, "repo")
	}
}

func TestScopeGateLatticeParentPassesChildGate(t *testing.T) {
	// GET /user/keys accepts read:public_key; a token holding the
	// admin:public_key parent must pass through the lattice.
	srv, mint := scopeServer(t)
	token := mint("admin:public_key")

	resp, body := authedSend(t, srv, http.MethodGet, "/user/keys", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin:public_key on read gate: status %d, body %s, want 200", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Accepted-OAuth-Scopes"); got != "read:public_key" {
		t.Errorf("X-Accepted-OAuth-Scopes = %q, want %q", got, "read:public_key")
	}

	// And the reverse direction stays shut: read:public_key cannot delete.
	reader := mint("read:public_key")
	resp, body = authedSend(t, srv, http.MethodDelete, "/user/keys/1", reader, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("read:public_key on admin gate: status %d, body %s, want 403", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Accepted-OAuth-Scopes"); got != "admin:public_key" {
		t.Errorf("X-Accepted-OAuth-Scopes = %q, want %q", got, "admin:public_key")
	}
}

func TestScopeGateAnonymousFlowsToHandlerAuth(t *testing.T) {
	// The gate only judges scoped user tokens. An anonymous request reaches
	// the handler, whose own auth answers; it must not get the 403 scope
	// error, and the accepted header still advertises the family.
	srv, _ := scopeServer(t)

	req, err := http.NewRequest(http.MethodPatch, srv.URL+"/repos/octocat/hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden {
		t.Fatalf("anonymous request hit the scope gate (403)")
	}
	if got := resp.Header.Get("X-Accepted-OAuth-Scopes"); got != "repo, public_repo" {
		t.Errorf("X-Accepted-OAuth-Scopes = %q, want %q", got, "repo, public_repo")
	}
}

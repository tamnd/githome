package graphql_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/go-git/go-billy/v5/util"

	graphqlapi "github.com/tamnd/githome/api/graphql"
	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/config"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/jsondiff"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// repoViewQuery is the document gh repo view sends, reduced to the fields the M2
// schema serves. gh selects name, description, and defaultBranchRef plus the
// node id; the contract is that this document resolves field for field.
const repoViewQuery = `query RepositoryInfo($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    id
    name
    nameWithOwner
    description
    isPrivate
    createdAt
    pushedAt
    url
    defaultBranchRef {
      name
      target {
        oid
      }
    }
  }
}`

func graphqlServer(t *testing.T) (*httptest.Server, string) {
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

	u := &store.UserRow{Login: "octocat", Type: "User"}
	if err := st.InsertUser(ctx, u); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	g, err := auth.GenerateToken(auth.PrefixClassicPAT)
	if err != nil {
		t.Fatal(err)
	}
	hash := g.Hash
	if err := st.InsertToken(ctx, &store.TokenRow{
		UserPK: &u.PK, TokenHash: hash[:], TokenPrefix: auth.PrefixClassicPAT,
		LastEight: g.Last8, Kind: "pat", Scopes: "repo",
	}); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	desc := "the hello repo"
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	repo := &store.RepoRow{OwnerPK: u.PK, Name: "hello", Description: &desc, DefaultBranch: "master", PushedAt: &when}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("insert repo: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	commitOne(t, gitStore.Dir(repo.PK), when)

	authSvc := auth.NewService(st, "https://git.test.internal")
	t.Cleanup(authSvc.Close)

	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	h := graphqlapi.NewHandler(graphqlapi.Deps{
		Auth:       authSvc,
		Repos:      repoSvc,
		Issues:     issueSvc,
		Search:     domain.NewSearchService(st, repoSvc, issueSvc, gitStore),
		URLs:       presenter.NewURLBuilder(graphqlURLs(t)),
		NodeFormat: nodeid.FormatNew,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, g.Plaintext
}

func graphqlURLs(t *testing.T) config.URLs {
	t.Helper()
	mustURL := func(s string) *url.URL {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return u
	}
	return config.URLs{
		API:     mustURL("https://git.test.internal/api/v3"),
		HTML:    mustURL("https://git.test.internal"),
		GraphQL: mustURL("https://git.test.internal/api/graphql"),
		SSHHost: "git.test.internal",
		SSHPort: 22,
	}
}

// golden reads a recorded GraphQL response under testdata. With RECORD=1 the
// contract tests write their responses here instead of asserting against them.
func golden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return b
}

func commitOne(t *testing.T, dir string, when time.Time) {
	t.Helper()
	r, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if err := util.WriteFile(wt.Filesystem, "README.md", []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	sig := &object.Signature{Name: "Octo Cat", Email: "octo@example.com", When: when}
	if _, err := wt.Commit("initial commit", &gogit.CommitOptions{Author: sig, Committer: sig}); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func post(t *testing.T, srv *httptest.Server, token, query string, vars map[string]any) []byte {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	out := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			break
		}
	}
	return out
}

// TestRepositoryQuery confirms the gh repo view document resolves field for
// field against the recorded golden.
func TestRepositoryQuery(t *testing.T) {
	srv, token := graphqlServer(t)
	got := post(t, srv, token, repoViewQuery, map[string]any{"owner": "octocat", "name": "hello"})
	if os.Getenv("RECORD") == "1" {
		norm := strings.ReplaceAll(string(got), "git.test.internal", "HOST")
		if err := os.WriteFile(filepath.Join("testdata", "repository.golden.json"), append([]byte(norm), '\n'), 0o644); err != nil {
			t.Fatalf("record: %v", err)
		}
		return
	}
	jsondiff.AssertCompatible(t, golden(t, "repository.golden.json"), got, jsondiff.Default("git.test.internal"))
}

// TestMissingRepositoryIsNull confirms a repository the actor cannot see comes
// back as a null data.repository plus the NOT_FOUND error GitHub returns, with
// the type at the top level of the error object where gh's matcher reads it.
func TestMissingRepositoryIsNull(t *testing.T) {
	srv, token := graphqlServer(t)
	got := post(t, srv, token, repoViewQuery, map[string]any{"owner": "octocat", "name": "nope"})
	var env struct {
		Data struct {
			Repository *json.RawMessage `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Path    []any  `json:"path"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal: %v, body %s", err, got)
	}
	if env.Data.Repository != nil {
		t.Fatalf("repository = %s, want null", *env.Data.Repository)
	}
	if len(env.Errors) != 1 {
		t.Fatalf("errors = %v, want exactly one, body %s", env.Errors, got)
	}
	e := env.Errors[0]
	if e.Type != "NOT_FOUND" {
		t.Errorf("error type = %q, want NOT_FOUND at the top level, body %s", e.Type, got)
	}
	if want := "Could not resolve to a Repository with the name 'octocat/nope'."; e.Message != want {
		t.Errorf("error message = %q, want %q", e.Message, want)
	}
	if len(e.Path) != 1 || e.Path[0] != "repository" {
		t.Errorf("error path = %v, want [repository]", e.Path)
	}
}

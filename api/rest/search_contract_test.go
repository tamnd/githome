package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
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

// searchFixture is a REST server whose store holds two of octocat's public
// repositories (hello, world) and one private repository owned by another user
// (hubot/secret), so the search tests can assert both matching and the
// visibility rule that a private repository never leaks into an anonymous or
// other-user search. The owner's token authenticates the issue seeds.
type searchFixture struct {
	srv      *httptest.Server
	token    string
	st       *store.Store
	helloPK  int64
	gitStore *git.Store
}

func searchServer(t *testing.T) searchFixture {
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

	octocat := &store.UserRow{Login: "octocat", Type: "User"}
	if err := st.InsertUser(ctx, octocat); err != nil {
		t.Fatalf("insert octocat: %v", err)
	}
	hubot := &store.UserRow{Login: "hubot", Type: "User"}
	if err := st.InsertUser(ctx, hubot); err != nil {
		t.Fatalf("insert hubot: %v", err)
	}

	g, err := auth.GenerateToken(auth.PrefixClassicPAT)
	if err != nil {
		t.Fatal(err)
	}
	hash := g.Hash
	if err := st.InsertToken(ctx, &store.TokenRow{
		UserPK: &octocat.PK, TokenHash: hash[:], TokenPrefix: auth.PrefixClassicPAT,
		LastEight: g.Last8, Kind: "pat", Scopes: "repo",
	}); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	desc := "My first repository on Githome."
	hello := &store.RepoRow{OwnerPK: octocat.PK, Name: "hello", DefaultBranch: "master", Description: &desc}
	if err := st.InsertRepo(ctx, hello); err != nil {
		t.Fatalf("insert hello: %v", err)
	}
	world := &store.RepoRow{OwnerPK: octocat.PK, Name: "world", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, world); err != nil {
		t.Fatalf("insert world: %v", err)
	}
	secret := &store.RepoRow{OwnerPK: hubot.PK, Name: "secret", DefaultBranch: "main", Private: true}
	if err := st.InsertRepo(ctx, secret); err != nil {
		t.Fatalf("insert secret: %v", err)
	}

	authSvc := auth.NewService(st, "https://git.test.internal")
	t.Cleanup(authSvc.Close)
	cfg := authConfig(t)
	gitStore := git.NewStore(t.TempDir())
	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	root := mizu.NewRouter()
	Mount(root, Deps{
		Config:     cfg,
		Ready:      st,
		Auth:       authSvc,
		Users:      domain.NewUserService(st),
		Repos:      repoSvc,
		Issues:     issueSvc,
		Search:     domain.NewSearchService(st, repoSvc, issueSvc, gitStore),
		URLs:       presenter.NewURLBuilder(cfg.URLs),
		NodeFormat: nodeid.FormatNew,
	})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	return searchFixture{srv: srv, token: g.Plaintext, st: st, helloPK: hello.PK, gitStore: gitStore}
}

// decodeBody unmarshals a JSON response body into v, failing the test on error.
func decodeBody(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode body: %v\n%s", err, body)
	}
}

// seedIssue opens an issue in octocat/hello through the API, failing the test on
// a non-201.
func (fx searchFixture) seedIssue(t *testing.T, repo, body string) {
	t.Helper()
	resp, out := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/"+repo+"/issues", fx.token, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issue in %s: status %d, body %s", repo, resp.StatusCode, out)
	}
}

func TestSearchIssuesContract(t *testing.T) {
	fx := searchServer(t)
	fx.seedIssue(t, "hello", `{"title":"Login crashes on start","body":"It crashes when I open the app."}`)
	fx.seedIssue(t, "hello", `{"title":"Typo in the readme","body":"Spelling slip in the intro."}`)

	resp, body := get(t, fx.srv, "/search/issues?q=crashes")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "search_issues.golden.json", body)
}

func TestSearchIssuesRepoQualifier(t *testing.T) {
	fx := searchServer(t)
	fx.seedIssue(t, "hello", `{"title":"Shared word here"}`)
	fx.seedIssue(t, "world", `{"title":"Shared word elsewhere"}`)

	// repo: scopes the search to one repository, so only its issue matches.
	resp, body := get(t, fx.srv, "/search/issues?q=shared+repo:octocat/hello")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	var env struct {
		TotalCount int `json:"total_count"`
		Items      []struct {
			Title string `json:"title"`
		} `json:"items"`
	}
	decodeBody(t, body, &env)
	if env.TotalCount != 1 || len(env.Items) != 1 {
		t.Fatalf("repo-scoped search returned %d items (total %d), want 1", len(env.Items), env.TotalCount)
	}
	if env.Items[0].Title != "Shared word here" {
		t.Errorf("matched the wrong issue: %q", env.Items[0].Title)
	}
}

func TestSearchRepositoriesContract(t *testing.T) {
	fx := searchServer(t)
	resp, body := get(t, fx.srv, "/search/repositories?q=hello+user:octocat")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "search_repositories.golden.json", body)
}

func TestSearchRepositoriesHidesPrivate(t *testing.T) {
	fx := searchServer(t)
	// An anonymous search over hubot's repositories must not surface the private
	// one, even though it would match the term.
	resp, body := get(t, fx.srv, "/search/repositories?q=secret+user:hubot")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	var env struct {
		TotalCount int `json:"total_count"`
	}
	decodeBody(t, body, &env)
	if env.TotalCount != 0 {
		t.Errorf("anonymous search exposed %d repositories, want 0 (private hidden)", env.TotalCount)
	}
}

func TestSearchCodeContract(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	fx := searchServer(t)
	bareTwoCommits(t, fx.gitStore, fx.helloPK)

	// README.md from bareTwoCommits holds "# Hello", so a scoped term matches it.
	resp, body := get(t, fx.srv, "/search/code?q=hello+repo:octocat/hello")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "search_code.golden.json", body)
}

func TestSearchCodeRequiresScope(t *testing.T) {
	fx := searchServer(t)
	resp, body := get(t, fx.srv, "/search/code?q=hello")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422, body %s", resp.StatusCode, body)
	}
}

func TestSearchMissingQuery(t *testing.T) {
	fx := searchServer(t)
	for _, path := range []string{"/search/issues", "/search/repositories", "/search/code"} {
		resp, body := get(t, fx.srv, path)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status %d, want 422, body %s", path, resp.StatusCode, body)
		}
	}
}

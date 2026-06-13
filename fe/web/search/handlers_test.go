package search

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/go-git/go-billy/v5/util"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/config"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/assets"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// fixedWhen pins every commit time so the seeded tree's object ids are stable
// across runs.
var fixedWhen = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// fixture is the search web test harness: a live httptest server mounting the
// global and the scoped search handlers over a real sqlite store, a real domain
// search service, and a real git store, plus the seeded names the assertions
// address. The viewer is anonymous, the visibility floor: the public repo's
// metadata, issues, and code are searchable and the private repo never appears in
// a result or behind the scoped page (a hard 404, not a 403).
type fixture struct {
	srv     *httptest.Server
	owner   string
	repo    string
	private string
}

func newFixture(t *testing.T) fixture {
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
		t.Fatalf("insert user: %v", err)
	}

	desc := "the hello repo"
	hello := &store.RepoRow{OwnerPK: owner.PK, Name: "hello", Description: &desc, DefaultBranch: "master"}
	if err := st.InsertRepo(ctx, hello); err != nil {
		t.Fatalf("insert hello: %v", err)
	}
	secret := &store.RepoRow{OwnerPK: owner.PK, Name: "secret", Private: true, DefaultBranch: "master"}
	if err := st.InsertRepo(ctx, secret); err != nil {
		t.Fatalf("insert secret: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	buildGitFixture(t, gitStore.Dir(hello.PK))
	if _, err := gitStore.Init(secret.PK); err != nil {
		t.Fatalf("init secret git: %v", err)
	}

	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	searchSvc := domain.NewSearchService(st, repoSvc, issueSvc, gitStore)

	// One issue whose title carries a distinctive term, so the issues rail and the
	// row both have real data to match.
	if _, err := issueSvc.CreateIssue(ctx, owner.PK, "octocat", "hello", domain.IssueInput{Title: "first findme issue"}); err != nil {
		t.Fatalf("create issue: %v", err)
	}

	renderSet, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	h := New(Deps{
		Search: searchSvc,
		Repos:  repoSvc,
		URLs:   presenter.NewURLBuilder(testURLs(t)),
		Render: renderSet,
		View:   view.NewBuilder("Githome"),
		Logger: discard,
	})

	root := mizu.NewRouter()
	page := root.With(webmw.ColorMode())
	page.Get("/search", h.Global)
	sg := page.With(h.Resolve)
	sg.Get("/{owner}/{repo}/search", h.Scoped)

	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	return fixture{srv: srv, owner: "octocat", repo: "hello", private: "secret"}
}

// buildGitFixture seeds the hello repository with a README and a Go source file
// so the code search has a real tree to walk on the default branch.
func buildGitFixture(t *testing.T, dir string) {
	t.Helper()
	r, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	fs := wt.Filesystem
	sig := &object.Signature{Name: "Octo Cat", Email: "octo@example.com", When: fixedWhen}

	if err := util.WriteFile(fs, "README.md", []byte("# Hello\n\nwelcome aboard\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	if err := util.WriteFile(fs, "main.go", []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("main.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("initial commit", &gogit.CommitOptions{Author: sig, Committer: sig}); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func testURLs(t *testing.T) config.URLs {
	t.Helper()
	must := func(s string) *url.URL {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return u
	}
	return config.URLs{
		API:     must("https://git.test.internal/api/v3"),
		HTML:    must("https://git.test.internal"),
		GraphQL: must("https://git.test.internal/api/graphql"),
		SSHHost: "git.test.internal",
		SSHPort: 22,
	}
}

// get issues a no-redirect GET and returns the response and body.
func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(srv.URL + path)
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

func TestGlobalLandingOnEmptyQuery(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/search")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// An empty query renders the landing, not an empty result set.
	if !strings.Contains(body, "search-landing") {
		t.Errorf("empty query did not render the landing:\n%s", body)
	}
	if strings.Contains(body, "search-results-count") {
		t.Errorf("the landing should not show a result count")
	}
}

func TestGlobalRepositoriesDefault(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/search?q=hello")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// Repositories is the default global type: the hello repo is listed.
	if !strings.Contains(body, "octocat/hello") {
		t.Errorf("global default search is missing the repository result:\n%s", body)
	}
	// The Repositories tab is the active one in the rail.
	if !strings.Contains(body, `aria-current="page"`) {
		t.Errorf("rail is missing the active-tab marker")
	}
}

func TestGlobalLegacyPageAlias(t *testing.T) {
	fx := newFixture(t)
	// page 1 lists the only repo; the legacy p= alias must reach the same pager
	// as page=, so p=2 pages past the single result and drops the repo. If p=
	// were ignored the handler would default to page 1 and still show it.
	_, first := get(t, fx.srv, "/search?q=hello&type=repositories&p=1")
	if !strings.Contains(first, "octocat/hello") {
		t.Fatalf("p=1 should list the repo:\n%s", first)
	}
	_, second := get(t, fx.srv, "/search?q=hello&type=repositories&p=2")
	if strings.Contains(second, "octocat/hello") {
		t.Errorf("p=2 should page past the single result, but the repo is still listed:\n%s", second)
	}
}

func TestGlobalIssuesType(t *testing.T) {
	fx := newFixture(t)
	_, body := get(t, fx.srv, "/search?q=findme&type=issues")
	if !strings.Contains(body, "first findme issue") {
		t.Errorf("issues search is missing the matching issue:\n%s", body)
	}
}

func TestGlobalCodeNeedsScope(t *testing.T) {
	fx := newFixture(t)
	_, body := get(t, fx.srv, "/search?q=package&type=code")
	// An unscoped code search renders the honest scope-required blankslate, not a
	// 500 and not a misleading empty list.
	if !strings.Contains(body, "needs a repo:") {
		t.Errorf("unscoped code search is missing the scope-required blankslate:\n%s", body)
	}
}

func TestGlobalPrivateRepoExcluded(t *testing.T) {
	fx := newFixture(t)
	_, body := get(t, fx.srv, "/search?q=secret")
	// The private repo never appears in an anonymous viewer's results.
	if strings.Contains(body, "octocat/secret") {
		t.Errorf("private repo leaked into anonymous search results:\n%s", body)
	}
}

func TestScopedCodeDefault(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/search?q=package")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// Code is the default type inside a repo: the matching file is listed by path.
	if !strings.Contains(body, "main.go") {
		t.Errorf("scoped code search is missing the matching file:\n%s", body)
	}
	// The scoped page wears the repo header.
	if !strings.Contains(body, "octocat/hello") {
		t.Errorf("scoped search is missing the repo header context:\n%s", body)
	}
}

func TestScopedIssuesType(t *testing.T) {
	fx := newFixture(t)
	_, body := get(t, fx.srv, "/octocat/hello/search?q=findme&type=issues")
	if !strings.Contains(body, "first findme issue") {
		t.Errorf("scoped issues search is missing the matching issue:\n%s", body)
	}
}

func TestScopedEmptyResultsBlankslate(t *testing.T) {
	fx := newFixture(t)
	_, body := get(t, fx.srv, "/octocat/hello/search?q=nothingmatchesthisxyzzy&type=issues")
	if !strings.Contains(body, "search-empty") {
		t.Errorf("a query that matched nothing did not render the empty blankslate:\n%s", body)
	}
}

func TestScopedPrivateRepoNotFound(t *testing.T) {
	fx := newFixture(t)
	resp, _ := get(t, fx.srv, "/octocat/secret/search?q=anything")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("scoped search on a private repo status = %d, want 404", resp.StatusCode)
	}
}

func TestScopedUnknownRepoNotFound(t *testing.T) {
	fx := newFixture(t)
	resp, _ := get(t, fx.srv, "/octocat/nope/search?q=anything")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("scoped search on an unknown repo status = %d, want 404", resp.StatusCode)
	}
}

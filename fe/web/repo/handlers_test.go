package repo

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
	"github.com/tamnd/githome/markup"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// fixedWhen pins every commit and tag time so the object ids are stable and the
// rendered dates do not drift across runs.
var fixedWhen = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// fixture is the web code-browsing test harness: a live httptest server mounting
// the repo handlers over a real sqlite store and a real git store, plus the names
// the seed produced so the assertions can address them.
type fixture struct {
	srv     *httptest.Server
	owner   string
	repo    string
	private string
	blank   string
	headSHA string
	branch  string
}

// newFixture seeds one owner with three repositories: a populated public repo
// (two commits, a docs directory, a lightweight and an annotated tag), a private
// repo, and an empty repo. It mounts the F1 routes the same way fe.Mount does, so
// the test exercises the real router chain, the real templates, and the real
// domain reads. The viewer is anonymous, which is the visibility floor: the
// public repo is fully readable and the private one is a hard 404.
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

	u := &store.UserRow{Login: "octocat", Type: "User"}
	if err := st.InsertUser(ctx, u); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	desc := "the hello repo"
	pushed := fixedWhen
	hello := &store.RepoRow{OwnerPK: u.PK, Name: "hello", Description: &desc, DefaultBranch: "master", PushedAt: &pushed}
	if err := st.InsertRepo(ctx, hello); err != nil {
		t.Fatalf("insert hello: %v", err)
	}
	secret := &store.RepoRow{OwnerPK: u.PK, Name: "secret", Private: true, DefaultBranch: "master"}
	if err := st.InsertRepo(ctx, secret); err != nil {
		t.Fatalf("insert secret: %v", err)
	}
	blank := &store.RepoRow{OwnerPK: u.PK, Name: "blank", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, blank); err != nil {
		t.Fatalf("insert blank: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	buildGitFixture(t, gitStore.Dir(hello.PK))
	buildGitFixture(t, gitStore.Dir(secret.PK))
	if _, err := gitStore.Init(blank.PK); err != nil {
		t.Fatalf("init blank git: %v", err)
	}

	gr, err := gitStore.Open(hello.PK)
	if err != nil {
		t.Fatalf("open hello git: %v", err)
	}
	head, _ := gr.HEAD()

	renderSet, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}

	h := New(Deps{
		Repos:  domain.NewRepoService(st, gitStore),
		URLs:   presenter.NewURLBuilder(testURLs(t)),
		Render: renderSet,
		View:   view.NewBuilder("Githome"),
		Markup: markup.New(markup.Config{
			BaseURL: testURLs(t).HTML.String(),
			Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		}),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	root := mizu.NewRouter()
	page := root.With(webmw.ColorMode())
	rg := page.With(h.Resolve)
	rg.Get("/{owner}/{repo}", h.Home)
	rg.Get("/{owner}/{repo}/tree/{rest...}", h.Tree)
	rg.Get("/{owner}/{repo}/blob/{rest...}", h.Blob)
	rg.Get("/{owner}/{repo}/raw/{rest...}", h.Raw)
	rg.Get("/{owner}/{repo}/commits", h.Commits)
	rg.Get("/{owner}/{repo}/commits/{rest...}", h.Commits)
	rg.Get("/{owner}/{repo}/branches", h.Branches)
	rg.Get("/{owner}/{repo}/tags", h.Tags)
	rg.Get("/{owner}/{repo}/find/{rest...}", h.FileFinder)

	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	return fixture{
		srv: srv, owner: "octocat", repo: "hello", private: "secret", blank: "blank",
		headSHA: head.Commit, branch: head.Name,
	}
}

// buildGitFixture writes two commits, a docs directory, and two tags into a fresh
// repository at dir. The read methods behave the same on this worktree repo as on
// a bare one.
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
	// A Go source file gives the highlighted-source blob path a target with a real
	// grammar, so the syntax highlighter is exercised through the handler.
	if err := util.WriteFile(fs, "main.go", []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("main.go"); err != nil {
		t.Fatal(err)
	}
	first, err := wt.Commit("initial commit", &gogit.CommitOptions{Author: sig, Committer: sig})
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}
	if err := util.WriteFile(fs, "docs/guide.md", []byte("guide body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("docs/guide.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("add the guide", &gogit.CommitOptions{Author: sig, Committer: sig}); err != nil {
		t.Fatalf("second commit: %v", err)
	}
	if _, err := r.CreateTag("v0.1.0", first, nil); err != nil {
		t.Fatalf("lightweight tag: %v", err)
	}
	if _, err := r.CreateTag("v1.0.0", first, &gogit.CreateTagOptions{Tagger: sig, Message: "release one"}); err != nil {
		t.Fatalf("annotated tag: %v", err)
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

// get issues a GET to the test server and returns the response and the body. It
// does not follow redirects, so a test can assert the 302 the tree/blob
// auto-correct emits.
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

func TestHomeRendersReadme(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// The repo home renders the README as GFM through the markup package: the body
	// text survives and the markdown-body container marks it as rendered, not the
	// escaped-source fallback.
	if !strings.Contains(body, "welcome aboard") {
		t.Errorf("home is missing the README body:\n%s", body)
	}
	if !strings.Contains(body, "markdown-body") {
		t.Errorf("home README did not render through markup:\n%s", body)
	}
	// The default-root listing shows the docs directory and the README entry.
	if !strings.Contains(body, "docs") || !strings.Contains(body, "README.md") {
		t.Errorf("home is missing the tree entries:\n%s", body)
	}
	// The address bar stays at the bare repo URL: the home does not redirect.
	if !strings.Contains(body, `href="/octocat/hello/tree/`) {
		t.Errorf("home is missing a tree link into the docs directory")
	}
}

func TestTreeListsDirectory(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/tree/master/docs")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "guide.md") {
		t.Errorf("docs tree is missing guide.md:\n%s", body)
	}
}

func TestTreeOnBlobRedirectsToBlob(t *testing.T) {
	fx := newFixture(t)
	resp, _ := get(t, fx.srv, "/octocat/hello/tree/master/README.md")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/octocat/hello/blob/master/README.md" {
		t.Errorf("redirect Location = %q", loc)
	}
}

func TestBlobShowsFileContent(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/blob/master/main.go")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "func") || !strings.Contains(body, "main") {
		t.Errorf("blob is missing the file content:\n%s", body)
	}
	// Line numbers anchor the source lines.
	if !strings.Contains(body, `id="L1"`) {
		t.Errorf("blob is missing line anchors")
	}
	// A Go source blob is syntax-highlighted: the keyword spans carry the pl-k
	// class the highlighter emits, so the source path runs through markup.
	if !strings.Contains(body, `class="pl-k"`) {
		t.Errorf("Go blob is not syntax-highlighted (no pl-k spans):\n%s", body)
	}
}

func TestBlobRendersMarkdown(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/blob/master/README.md")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// A markdown blob renders as GFM by default: the heading becomes an <h1> with
	// the generated anchor id, inside the markdown-body container.
	if !strings.Contains(body, "markdown-body") {
		t.Errorf("markdown blob is missing the markdown-body container:\n%s", body)
	}
	if !strings.Contains(body, `id="user-content-hello"`) {
		t.Errorf("markdown blob did not render the heading anchor:\n%s", body)
	}
	// The Code toggle drops to the plain source view.
	if !strings.Contains(body, `href="?plain=1"`) {
		t.Errorf("markdown blob is missing the plain-source toggle:\n%s", body)
	}
}

func TestBlobPlainShowsSource(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/blob/master/README.md?plain=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// ?plain=1 shows the escaped source in the line table, not the rendered GFM:
	// the literal "# Hello" appears and there is no markdown-body container.
	if !strings.Contains(body, "# Hello") {
		t.Errorf("plain markdown blob is missing the raw source:\n%s", body)
	}
	if strings.Contains(body, "markdown-body") {
		t.Errorf("plain markdown blob unexpectedly rendered GFM:\n%s", body)
	}
	if !strings.Contains(body, `id="L1"`) {
		t.Errorf("plain markdown blob is missing line anchors:\n%s", body)
	}
}

func TestBlobOnDirRedirectsToTree(t *testing.T) {
	fx := newFixture(t)
	resp, _ := get(t, fx.srv, "/octocat/hello/blob/master/docs")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/octocat/hello/tree/master/docs" {
		t.Errorf("redirect Location = %q", loc)
	}
}

func TestRawServesDefensively(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/raw/master/README.md")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("raw nosniff header = %q", got)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("raw content-type = %q, want text/plain", ct)
	}
	if !strings.Contains(body, "welcome aboard") {
		t.Errorf("raw body = %q", body)
	}
}

func TestCommitsHistory(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/commits")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "add the guide") || !strings.Contains(body, "initial commit") {
		t.Errorf("commits page is missing the history:\n%s", body)
	}
}

func TestBranchesAndTags(t *testing.T) {
	fx := newFixture(t)
	_, branches := get(t, fx.srv, "/octocat/hello/branches")
	if !strings.Contains(branches, "master") || !strings.Contains(branches, "default") {
		t.Errorf("branches page is missing the default branch:\n%s", branches)
	}
	_, tags := get(t, fx.srv, "/octocat/hello/tags")
	// Version-aware descending: v1.0.0 sorts before v0.1.0.
	if i, j := strings.Index(tags, "v1.0.0"), strings.Index(tags, "v0.1.0"); i < 0 || j < 0 || i > j {
		t.Errorf("tags are not version-sorted descending (v1=%d v0=%d):\n%s", i, j, tags)
	}
}

func TestFileFinderListsBlobs(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/find/master")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "docs/guide.md") || !strings.Contains(body, "README.md") {
		t.Errorf("finder is missing the file list:\n%s", body)
	}
}

func TestPrivateRepoIsNotFound(t *testing.T) {
	fx := newFixture(t)
	// Anonymous: the private repo is a 404, never a 403, so its existence does not
	// leak through the status code.
	resp, _ := get(t, fx.srv, "/octocat/secret")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("private repo status = %d, want 404", resp.StatusCode)
	}
}

func TestMissingRepoIsNotFound(t *testing.T) {
	fx := newFixture(t)
	resp, _ := get(t, fx.srv, "/octocat/nope")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing repo status = %d, want 404", resp.StatusCode)
	}
}

func TestEmptyRepoShowsQuickSetup(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/blank")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Quick setup") {
		t.Errorf("empty repo did not render quick setup:\n%s", body)
	}
}

func TestUnknownRefIsNotFound(t *testing.T) {
	fx := newFixture(t)
	resp, _ := get(t, fx.srv, "/octocat/hello/tree/no-such-branch")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown ref status = %d, want 404", resp.StatusCode)
	}
}

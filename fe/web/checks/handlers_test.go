package checks

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/assets"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
)

// fixture is the checks-page test harness: a live httptest server mounting the
// checks handler over a real sqlite store and a real bare git repository, with one
// public repo carrying a seeded check run and commit status at its head, and one
// private repo the anonymous viewer cannot see.
type fixture struct {
	srv     *httptest.Server
	owner   string
	repo    string
	private string
	headSHA string
}

// newFixture seeds one owner with a public repo (one commit, a seeded successful
// check run and a successful commit status at its head) and a private repo. It
// mounts the checks route the same way fe.Mount does, so the test exercises the
// real router chain, the real templates, and the real domain reads. The viewer is
// anonymous, the visibility floor: the public repo's checks are readable and the
// private repo is a hard 404. The fixture needs the git binary to resolve a sha,
// the same path the rollup read takes, so it skips where git is absent.
func newFixture(t *testing.T) fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
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

	hello := &store.RepoRow{OwnerPK: owner.PK, Name: "hello", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, hello); err != nil {
		t.Fatalf("insert hello: %v", err)
	}
	secret := &store.RepoRow{OwnerPK: owner.PK, Name: "secret", Private: true, DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, secret); err != nil {
		t.Fatalf("insert secret: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	bareRepo(t, gitStore, hello.PK)
	bareRepo(t, gitStore, secret.PK)

	headSHA, err := gitStore.RefSHA(ctx, hello.PK, "refs/heads/main")
	if err != nil {
		t.Fatalf("resolve head: %v", err)
	}

	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	checksSvc := domain.NewChecksService(st, repoSvc, issueSvc, gitStore)

	// Seed the head sha with one completed check run and one successful commit
	// status, written through the same service the page reads back, so the row the
	// test asserts on travels the real write-then-read path.
	if _, err := checksSvc.CreateCheckRun(ctx, owner.PK, "octocat", "hello", domain.CheckRunInput{
		Name:        "build",
		HeadSHA:     headSHA,
		Status:      "completed",
		Conclusion:  "success",
		DetailsURL:  "https://ci.example.com/build/1",
		OutputTitle: "Build passed",
	}); err != nil {
		t.Fatalf("seed check run: %v", err)
	}
	if _, err := checksSvc.CreateStatus(ctx, owner.PK, "octocat", "hello", headSHA, domain.StatusInput{
		State:       "success",
		Context:     "ci/lint",
		TargetURL:   "https://ci.example.com/lint/1",
		Description: "Lint clean",
	}); err != nil {
		t.Fatalf("seed commit status: %v", err)
	}

	renderSet, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	h := New(Deps{
		Checks: checksSvc,
		Repos:  repoSvc,
		Render: renderSet,
		View:   view.NewBuilder("Githome"),
		Logger: discard,
	})

	root := mizu.NewRouter()
	page := root.With(webmw.ColorMode())
	cg := page.With(h.Resolve)
	cg.Get("/{owner}/{repo}/checks/{rest...}", h.Index)

	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	return fixture{srv: srv, owner: "octocat", repo: "hello", private: "secret", headSHA: headSHA}
}

// bareRepo builds a bare repository at the store's path for pk with one commit on
// main, the layout the git binary reads. It mirrors the domain test fixtures so
// the sha the checks service resolves is a real object the binary can find.
func bareRepo(t *testing.T, gs *git.Store, pk int64) {
	t.Helper()
	src := t.TempDir()
	gitCmd(t, src, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(src, "README.md"), "# Hello\n")
	gitCmd(t, src, "add", "-A")
	gitCmd(t, src, "commit", "-q", "-m", "initial commit")

	bare := gs.Dir(pk)
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, "", "clone", "-q", "--bare", src, bare)
}

// gitCmd runs a git command in dir (or the process directory when dir is empty)
// with a fixed identity so commits are deterministic.
func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Octo Cat", "GIT_AUTHOR_EMAIL=octo@example.com",
		"GIT_COMMITTER_NAME=Octo Cat", "GIT_COMMITTER_EMAIL=octo@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// get issues a GET to the test server and returns the response and the body. It
// does not follow redirects so a test can assert a redirect status directly.
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

// TestChecksPageRendersTheRollup is the happy path: the checks page at the head
// sha renders the seeded check run and commit status with the success token, so
// the page, the shared vocabulary, and the seed all agree.
func TestChecksPageRendersTheRollup(t *testing.T) {
	fx := newFixture(t)

	resp, body := get(t, fx.srv, "/"+fx.owner+"/"+fx.repo+"/checks/"+fx.headSHA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	// The seeded check run and commit status both render their names.
	for _, want := range []string{"build", "Build passed", "ci/lint", "Lint clean"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	// Both rows and the rollup pill are success, so the success token's color class
	// is on the page; the danger class is not.
	if !strings.Contains(body, "check-state-success") {
		t.Errorf("body missing the success color class")
	}
	if strings.Contains(body, "check-state-danger") {
		t.Errorf("body carries a danger class for an all-green rollup")
	}
	// The external details and target links survive the absolute-url guard.
	if !strings.Contains(body, "https://ci.example.com/build/1") {
		t.Errorf("body missing the check run details link")
	}
	if !strings.Contains(body, "https://ci.example.com/lint/1") {
		t.Errorf("body missing the commit status target link")
	}
}

// TestChecksPagePrivateRepoIs404 holds the 404-not-403 rule: a private repo the
// anonymous viewer cannot see renders the repository 404, never confirming the
// repo exists.
func TestChecksPagePrivateRepoIs404(t *testing.T) {
	fx := newFixture(t)

	resp, _ := get(t, fx.srv, "/"+fx.owner+"/"+fx.private+"/checks/"+fx.headSHA)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a private repo", resp.StatusCode)
	}
}

// TestChecksPageUnresolvableRefIs404 is the repo-scoped soft 404: a ref that does
// not resolve to a sha (the service reports ErrValidation) renders the repository
// 404 inside a repo the viewer can otherwise see.
func TestChecksPageUnresolvableRefIs404(t *testing.T) {
	fx := newFixture(t)

	resp, _ := get(t, fx.srv, "/"+fx.owner+"/"+fx.repo+"/checks/does-not-exist")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unresolvable ref", resp.StatusCode)
	}
}

// TestChecksPageNoJavaScript holds the no-JS completeness rule: the page is a
// static read with no form and no script-only control, so the full check state is
// present in the first response a scriptless client receives.
func TestChecksPageNoJavaScript(t *testing.T) {
	fx := newFixture(t)

	_, body := get(t, fx.srv, "/"+fx.owner+"/"+fx.repo+"/checks/"+fx.headSHA)
	// The page carries no checks-mutation control: the re-run, cancel, approve, and
	// dispatch affordances doc 11 sketches need the unbacked run engine and are
	// absent, so a scriptless client misses nothing the page would otherwise offer.
	for _, absent := range []string{"Re-run", "Cancel workflow", "Dispatch workflow"} {
		if strings.Contains(body, absent) {
			t.Errorf("read-only checks page should not offer %q", absent)
		}
	}
	// The full check state arrives in the first response: the run row's timestamp
	// renders as a relative-time element with no script needed to populate it.
	if !strings.Contains(body, "relative-time") {
		t.Errorf("body missing the relative-time element the run row renders")
	}
}

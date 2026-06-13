package pulls

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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

// fixture is the pulls web test harness: a live httptest server mounting the
// pull-request read handlers over a real sqlite store, a real domain PR service,
// and a real bare git repository with a main branch and a one-commit-ahead
// feature branch. The viewer is anonymous, the visibility floor: the public
// repo's pulls are readable and the private repo is a hard 404. The seeding needs
// the git binary, so the whole suite skips when git is unavailable.
type fixture struct {
	srv      *httptest.Server
	pulls    *domain.PRService
	checks   *domain.ChecksService
	ownerPK  int64
	owner    string
	repo     string
	private  string
	prNum    int64
	issueNum int64
	headSHA  string
}

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

	desc := "the hello repo"
	hello := &store.RepoRow{OwnerPK: owner.PK, Name: "hello", Description: &desc, DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, hello); err != nil {
		t.Fatalf("insert hello: %v", err)
	}
	secret := &store.RepoRow{OwnerPK: owner.PK, Name: "secret", Private: true, DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, secret); err != nil {
		t.Fatalf("insert secret: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	prBareRepo(t, gitStore, hello.PK)

	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	prSvc := domain.NewPRService(st, repoSvc, issueSvc, gitStore)
	checksSvc := domain.NewChecksService(st, repoSvc, issueSvc, gitStore)

	// Seed one open pull request from feature into main, with an opening body, so
	// the index row, the conversation timeline, the commits tab and the files diff
	// all have real data to render.
	body := "please review the new file"
	pr, err := prSvc.CreatePR(ctx, owner.PK, "octocat", "hello", domain.PRInput{
		Title: "add b", Body: &body, Base: "main", Head: "feature",
	})
	if err != nil {
		t.Fatalf("create pr: %v", err)
	}
	// Resolve mergeability the way the worker would, so the merge box reaches a
	// clean state and the diff stats fill in.
	iss, err := st.GetIssueByNumber(ctx, hello.PK, pr.Number)
	if err != nil {
		t.Fatalf("get issue by number: %v", err)
	}
	if err := prSvc.RecomputeMergeability(ctx, iss.PK); err != nil {
		t.Fatalf("recompute mergeability: %v", err)
	}
	// Seed one plain issue after the PR; it takes the next number in the shared
	// sequence, so the crossover test can address it through /pull/{n}.
	plain, err := issueSvc.CreateIssue(ctx, owner.PK, "octocat", "hello", domain.IssueInput{Title: "just an issue"})
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}

	renderSet, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	h := New(Deps{
		Pulls:  prSvc,
		Issues: issueSvc,
		Checks: checksSvc,
		Repos:  repoSvc,
		URLs:   presenter.NewURLBuilder(testURLs(t)),
		Render: renderSet,
		View:   view.NewBuilder("Githome"),
		Markup: markup.New(markup.Config{BaseURL: testURLs(t).HTML.String(), Logger: discard}),
		Logger: discard,
	})

	root := mizu.NewRouter()
	page := root.With(webmw.ColorMode())
	pg := page.With(h.Resolve)
	pg.Get("/{owner}/{repo}/pulls", h.Index)
	pg.Get("/{owner}/{repo}/pull/{number}", h.Conversation)
	pg.Get("/{owner}/{repo}/pull/{number}/commits", h.Commits)
	pg.Get("/{owner}/{repo}/pull/{number}/commits/{sha}", h.CommitRedirect)
	pg.Get("/{owner}/{repo}/pull/{number}/files", h.Files)
	pg.Get("/{owner}/{repo}/pull/{number}/files/expand", h.ExpandDiff)
	pg.Get("/{owner}/{repo}/pull/{number}/checks", h.Checks)
	pg.Get("/{owner}/{repo}/pull/{number}/partials/merge-box", h.MergeBox)

	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	return fixture{
		srv: srv, pulls: prSvc, checks: checksSvc, ownerPK: owner.PK,
		owner: "octocat", repo: "hello", private: "secret",
		prNum: pr.Number, issueNum: plain.Number, headSHA: pr.Head.SHA,
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

// prBareRepo builds a bare repository at gs.Dir(pk) with a main branch and a
// feature branch one commit ahead of it, a clean merge into main. It mirrors the
// domain fixture so the web tests exercise the same diff the service yields.
func prBareRepo(t *testing.T, gs *git.Store, pk int64) {
	t.Helper()
	src := t.TempDir()
	gitCmd(t, src, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(src, "a.txt"), "one\n")
	gitCmd(t, src, "add", "-A")
	gitCmd(t, src, "commit", "-q", "-m", "first")
	gitCmd(t, src, "checkout", "-q", "-b", "feature")
	writeFile(t, filepath.Join(src, "b.txt"), "two\n")
	gitCmd(t, src, "add", "-A")
	gitCmd(t, src, "commit", "-q", "-m", "add b")
	gitCmd(t, src, "checkout", "-q", "main")

	bare := gs.Dir(pk)
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, "", "clone", "-q", "--bare", src, bare)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=Octo Cat", "GIT_AUTHOR_EMAIL=octo@example.com",
		"GIT_COMMITTER_NAME=Octo Cat", "GIT_COMMITTER_EMAIL=octo@example.com",
		"GIT_AUTHOR_DATE=2026-01-02T03:04:05Z", "GIT_COMMITTER_DATE=2026-01-02T03:04:05Z",
	)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, errb.String())
	}
	return strings.TrimSpace(out.String())
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

func TestIndexListsOpenPulls(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/pulls")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "add b") {
		t.Errorf("index is missing the open pull request:\n%s", body)
	}
	// The Pull requests tab is current in the shared repo header.
	if !strings.Contains(body, `aria-current="page"`) {
		t.Errorf("index header is missing the current-tab marker")
	}
}

func TestIndexClosedFilterEmpty(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/pulls?state=closed")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// The only pull request is open, so the closed view lists nothing.
	if strings.Contains(body, "add b") {
		t.Errorf("closed filter unexpectedly listed the open pull request")
	}
}

func TestConversationRendersShellAndMergeBox(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/pull/"+itoa(fx.prNum))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// The opening body renders through markup in the timeline.
	if !strings.Contains(body, "please review the new file") {
		t.Errorf("conversation is missing the opening body:\n%s", body)
	}
	if !strings.Contains(body, "markdown-body") {
		t.Errorf("conversation did not render the body through markup:\n%s", body)
	}
	// The PR shell tab bar and the merge box are both present, and the open pill shows.
	if !strings.Contains(body, "pr-tabs") {
		t.Errorf("conversation is missing the PR tab bar:\n%s", body)
	}
	if !strings.Contains(body, "pr_merge_box") && !strings.Contains(body, "merge") {
		t.Errorf("conversation is missing the merge box:\n%s", body)
	}
}

func TestConversationAnonymousCannotComment(t *testing.T) {
	fx := newFixture(t)
	_, body := get(t, fx.srv, "/octocat/hello/pull/"+itoa(fx.prNum))
	if strings.Contains(body, `name="body"`) {
		t.Errorf("anonymous viewer was shown a comment composer")
	}
	if !strings.Contains(body, "to comment") {
		t.Errorf("anonymous viewer is missing the sign-in prompt:\n%s", body)
	}
}

func TestCommitsTabListsFeatureCommit(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/pull/"+itoa(fx.prNum)+"/commits")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "add b") {
		t.Errorf("commits tab is missing the feature commit:\n%s", body)
	}
}

func TestFilesTabRendersDiff(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/pull/"+itoa(fx.prNum)+"/files")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// The feature branch adds b.txt, so its path and the diff table both render.
	if !strings.Contains(body, "b.txt") {
		t.Errorf("files tab is missing the added file path:\n%s", body)
	}
	if !strings.Contains(body, "diff-table") {
		t.Errorf("files tab is missing the diff table:\n%s", body)
	}
}

func TestFilesTabSplitMode(t *testing.T) {
	fx := newFixture(t)

	// Default (no ?diff): unified table, Unified option selected.
	_, unified := get(t, fx.srv, "/octocat/hello/pull/"+itoa(fx.prNum)+"/files")
	if strings.Contains(unified, "diff-table-split") {
		t.Errorf("default files tab rendered the split table:\n%s", unified)
	}

	// ?diff=split: the side-by-side table renders and the Split option is current.
	resp, split := get(t, fx.srv, "/octocat/hello/pull/"+itoa(fx.prNum)+"/files?diff=split")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("split files tab status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(split, "diff-table-split") {
		t.Errorf("split files tab is missing the split table:\n%s", split)
	}
	// The toggle marks Split selected (aria-current on the split link's href).
	if !strings.Contains(split, `href="/octocat/hello/pull/`+itoa(fx.prNum)+`/files?diff=split" aria-current="true"`) {
		t.Errorf("split files tab toggle does not mark Split current:\n%s", split)
	}
	// The split view never disturbs the path or the file content.
	if !strings.Contains(split, "b.txt") {
		t.Errorf("split files tab dropped the changed file path:\n%s", split)
	}
}

func TestFilesTabHideWhitespace(t *testing.T) {
	fx := newFixture(t)
	base := "/octocat/hello/pull/" + itoa(fx.prNum) + "/files"

	// Default: the control offers to hide whitespace, pointing at ?w=1.
	_, def := get(t, fx.srv, base)
	if !strings.Contains(def, "Hide whitespace") {
		t.Errorf("default files tab is missing the hide-whitespace control:\n%s", def)
	}
	if !strings.Contains(def, `href="`+base+`?w=1"`) {
		t.Errorf("hide-whitespace link does not point at ?w=1:\n%s", def)
	}

	// ?w=1: the page renders and the control now offers to show whitespace again.
	resp, ws := get(t, fx.srv, base+"?w=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("hide-whitespace files tab status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(ws, "Show whitespace") {
		t.Errorf("?w=1 files tab is missing the show-whitespace control:\n%s", ws)
	}
	if !strings.Contains(ws, "b.txt") {
		t.Errorf("?w=1 files tab dropped the changed file path:\n%s", ws)
	}

	// The two axes compose: with ?w=1 set, the Split link keeps ?w=1 alongside it.
	if !strings.Contains(ws, `href="`+base+`?diff=split&amp;w=1"`) {
		t.Errorf("?w=1 split link does not preserve the whitespace axis:\n%s", ws)
	}
}

func TestExpandDiffFragment(t *testing.T) {
	fx := newFixture(t)

	// a.txt is unchanged on the feature branch and has one line; unfolding line 1
	// reads it from the head blob and returns a context row, not the page chrome.
	path := "/octocat/hello/pull/" + itoa(fx.prNum) + "/files/expand"
	resp, body := get(t, fx.srv, path+"?path=a.txt&sha="+fx.headSHA+"&os=1&ns=1&n=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expand status %d, want 200", resp.StatusCode)
	}
	if strings.Contains(body, "<html") {
		t.Errorf("expand fragment leaked the page chrome:\n%s", body)
	}
	if !strings.Contains(body, "diff-row-context") {
		t.Errorf("expand fragment is missing the context row:\n%s", body)
	}
	if !strings.Contains(body, "one") {
		t.Errorf("expand fragment did not read the blob line:\n%s", body)
	}

	// A missing path is the soft 404 the diff page uses, not a 500.
	bad, _ := get(t, fx.srv, path+"?path=nope.txt&sha="+fx.headSHA+"&os=1&ns=1&n=1")
	if bad.StatusCode != http.StatusNotFound {
		t.Errorf("expand of a missing file status = %d, want 404", bad.StatusCode)
	}

	// A request missing the required line params is rejected, not served empty.
	missing, _ := get(t, fx.srv, path+"?path=a.txt&sha="+fx.headSHA)
	if missing.StatusCode != http.StatusNotFound {
		t.Errorf("expand without line params status = %d, want 404", missing.StatusCode)
	}
}

func TestMergeBoxFragment(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/pull/"+itoa(fx.prNum)+"/partials/merge-box")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// The poll fragment renders the merge box standalone, not the whole page.
	if strings.Contains(body, "<html") {
		t.Errorf("merge-box fragment leaked the page chrome:\n%s", body)
	}
	if !strings.Contains(body, "merge") {
		t.Errorf("merge-box fragment is missing the merge box content:\n%s", body)
	}
}

func TestPrivateRepoPullsNotFound(t *testing.T) {
	fx := newFixture(t)
	resp, _ := get(t, fx.srv, "/octocat/secret/pulls")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("private repo pulls status = %d, want 404", resp.StatusCode)
	}
}

func TestMissingPullIsNotFound(t *testing.T) {
	fx := newFixture(t)
	resp, _ := get(t, fx.srv, "/octocat/hello/pull/9999")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing pull status = %d, want 404", resp.StatusCode)
	}
}

// Issues and pull requests share one number sequence, so a plain issue's number
// addressed through /pull/{n} redirects to the issue page, like github.com.
// The tab paths under the number redirect the same way, and a real PR number
// keeps rendering in place.
func TestPullNumberOfIssueRedirects(t *testing.T) {
	fx := newFixture(t)
	for _, path := range []string{
		"/octocat/hello/pull/" + itoa(fx.issueNum),
		"/octocat/hello/pull/" + itoa(fx.issueNum) + "/files",
	} {
		resp, _ := get(t, fx.srv, path)
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("GET %s status %d, want 302", path, resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != "/octocat/hello/issues/"+itoa(fx.issueNum) {
			t.Errorf("GET %s Location = %q, want the issue page", path, loc)
		}
	}

	resp, _ := get(t, fx.srv, "/octocat/hello/pull/"+itoa(fx.prNum))
	if resp.StatusCode != http.StatusOK {
		t.Errorf("real pull status = %d, want 200", resp.StatusCode)
	}
}

// itoa is a tiny local int64-to-string for building test paths.
func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

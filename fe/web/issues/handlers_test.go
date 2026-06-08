package issues

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// fixture is the issues web test harness: a live httptest server mounting the
// issues read handlers over a real sqlite store and a real domain issue service,
// plus the seeded names so the assertions can address them. The viewer is
// anonymous, the visibility floor: the public repo's issues are readable and the
// private repo is a hard 404.
type fixture struct {
	srv      *httptest.Server
	issues   *domain.IssueService
	ownerPK  int64
	owner    string
	repo     string
	private  string
	openNum  int64
	closeNum int64
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
	if _, err := gitStore.Init(hello.PK); err != nil {
		t.Fatalf("init hello git: %v", err)
	}
	if _, err := gitStore.Init(secret.PK); err != nil {
		t.Fatalf("init secret git: %v", err)
	}

	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)

	// Seed one labeled open issue with a comment, and one closed issue, so the
	// index tabs, the row chips, and the detail timeline all have real data.
	if _, err := issueSvc.CreateLabel(ctx, owner.PK, "octocat", "hello", domain.LabelInput{Name: "bug", Color: "d73a4a"}); err != nil {
		t.Fatalf("create label: %v", err)
	}
	openBody := "the open issue body"
	open, err := issueSvc.CreateIssue(ctx, owner.PK, "octocat", "hello", domain.IssueInput{
		Title:  "first issue",
		Body:   &openBody,
		Labels: []string{"bug"},
	})
	if err != nil {
		t.Fatalf("create open issue: %v", err)
	}
	if _, err := issueSvc.CreateComment(ctx, owner.PK, "octocat", "hello", open.Number, "a thoughtful reply"); err != nil {
		t.Fatalf("create comment: %v", err)
	}
	closed, err := issueSvc.CreateIssue(ctx, owner.PK, "octocat", "hello", domain.IssueInput{Title: "second issue"})
	if err != nil {
		t.Fatalf("create closed issue: %v", err)
	}
	closedState := "closed"
	if _, err := issueSvc.EditIssue(ctx, owner.PK, "octocat", "hello", closed.Number, domain.IssuePatch{State: &closedState}); err != nil {
		t.Fatalf("close issue: %v", err)
	}

	renderSet, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	h := New(Deps{
		Issues: issueSvc,
		Repos:  repoSvc,
		URLs:   presenter.NewURLBuilder(testURLs(t)),
		Render: renderSet,
		View:   view.NewBuilder("Githome"),
		Markup: markup.New(markup.Config{BaseURL: testURLs(t).HTML.String(), Logger: discard}),
		Logger: discard,
	})

	root := mizu.NewRouter()
	page := root.With(webmw.ColorMode())
	ig := page.With(h.Resolve)
	ig.Get("/{owner}/{repo}/issues", h.Index)
	ig.Get("/{owner}/{repo}/issues/new", h.New)
	ig.Get("/{owner}/{repo}/issues/{number}", h.Show)

	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	return fixture{
		srv: srv, issues: issueSvc, ownerPK: owner.PK,
		owner: "octocat", repo: "hello", private: "secret",
		openNum: open.Number, closeNum: closed.Number,
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

func TestIndexListsOpenIssues(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/issues")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// The default index shows open issues: the open one is listed, the closed one
	// is not, and the label chip rides along.
	if !strings.Contains(body, "first issue") {
		t.Errorf("index is missing the open issue:\n%s", body)
	}
	if strings.Contains(body, "second issue") {
		t.Errorf("index unexpectedly listed the closed issue in the open view")
	}
	if !strings.Contains(body, ">bug<") {
		t.Errorf("index is missing the label chip:\n%s", body)
	}
	// The Issues tab is current in the shared repo header.
	if !strings.Contains(body, `aria-current="page"`) {
		t.Errorf("index header is missing the current-tab marker")
	}
}

func TestIndexClosedFilter(t *testing.T) {
	fx := newFixture(t)
	_, body := get(t, fx.srv, "/octocat/hello/issues?q=is%3Aissue+is%3Aclosed")
	if !strings.Contains(body, "second issue") {
		t.Errorf("closed filter is missing the closed issue:\n%s", body)
	}
	if strings.Contains(body, "first issue") {
		t.Errorf("closed filter unexpectedly listed the open issue")
	}
}

func TestIndexLabelFilter(t *testing.T) {
	fx := newFixture(t)
	_, body := get(t, fx.srv, "/octocat/hello/issues?q=is%3Aissue+is%3Aopen+label%3Abug")
	if !strings.Contains(body, "first issue") {
		t.Errorf("label filter dropped the matching issue:\n%s", body)
	}
	// The active label chip carries a remove link.
	if !strings.Contains(body, "Remove label filter bug") {
		t.Errorf("label filter is missing the active-chip remove link:\n%s", body)
	}
}

func TestShowRendersTimeline(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/issues/"+itoa(fx.openNum))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// The opening body and the comment both render through markup.
	if !strings.Contains(body, "the open issue body") {
		t.Errorf("show is missing the opening body:\n%s", body)
	}
	if !strings.Contains(body, "a thoughtful reply") {
		t.Errorf("show is missing the comment:\n%s", body)
	}
	if !strings.Contains(body, "markdown-body") {
		t.Errorf("show did not render the body through markup:\n%s", body)
	}
	// The open badge shows.
	if !strings.Contains(body, "issue-state-open") {
		t.Errorf("show is missing the open state badge:\n%s", body)
	}
}

func TestShowClosedBadge(t *testing.T) {
	fx := newFixture(t)
	_, body := get(t, fx.srv, "/octocat/hello/issues/"+itoa(fx.closeNum))
	if !strings.Contains(body, "issue-state-closed") {
		t.Errorf("closed issue is missing the closed state badge:\n%s", body)
	}
}

func TestShowAnonymousCannotComment(t *testing.T) {
	fx := newFixture(t)
	_, body := get(t, fx.srv, "/octocat/hello/issues/"+itoa(fx.openNum))
	// Anonymous: no composer, a sign-in prompt instead.
	if strings.Contains(body, `name="body"`) {
		t.Errorf("anonymous viewer was shown a comment composer")
	}
	if !strings.Contains(body, "to comment") {
		t.Errorf("anonymous viewer is missing the sign-in prompt:\n%s", body)
	}
}

func TestNewIssueForm(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/issues/new")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "New issue") || !strings.Contains(body, `name="title"`) {
		t.Errorf("new-issue form is missing fields:\n%s", body)
	}
	// Anonymous viewers see the gate note and a disabled submit.
	if !strings.Contains(body, "write access") {
		t.Errorf("new-issue form is missing the write-access note for anon:\n%s", body)
	}
}

func TestNewBeforeNumberRouting(t *testing.T) {
	fx := newFixture(t)
	// "new" must reach the form, not the show handler as if it were a number.
	resp, body := get(t, fx.srv, "/octocat/hello/issues/new")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "New issue") {
		t.Errorf("/issues/new routed to the wrong handler (status %d)", resp.StatusCode)
	}
}

func TestPrivateRepoIssuesNotFound(t *testing.T) {
	fx := newFixture(t)
	resp, _ := get(t, fx.srv, "/octocat/secret/issues")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("private repo issues status = %d, want 404", resp.StatusCode)
	}
}

func TestMissingIssueIsNotFound(t *testing.T) {
	fx := newFixture(t)
	resp, _ := get(t, fx.srv, "/octocat/hello/issues/9999")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing issue status = %d, want 404", resp.StatusCode)
	}
}

// itoa is a tiny local int64-to-string for building test paths.
func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

package dashboard

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// fixture mounts the /issues and /pulls dashboards over a real store, the real
// domain search, and the real session middleware on a TLS server (the session
// cookie is Secure). It seeds two users and three rows in octocat's repo: an
// issue octocat created, an issue hubot created with octocat assigned, and a
// pull request octocat created, so the Created/Assigned tabs and the
// issue/pull split all have something to disagree about. /_test/login issues
// octocat a session.
type fixture struct {
	srv    *httptest.Server
	client *http.Client
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

	octocat := &store.UserRow{Login: "octocat", Type: "User"}
	if err := st.InsertUser(ctx, octocat); err != nil {
		t.Fatalf("insert octocat: %v", err)
	}
	hubot := &store.UserRow{Login: "hubot", Type: "User"}
	if err := st.InsertUser(ctx, hubot); err != nil {
		t.Fatalf("insert hubot: %v", err)
	}
	hello := &store.RepoRow{OwnerPK: octocat.PK, Name: "hello", DefaultBranch: "master"}
	if err := st.InsertRepo(ctx, hello); err != nil {
		t.Fatalf("insert hello: %v", err)
	}
	gitStore := git.NewStore(t.TempDir())
	if _, err := gitStore.Init(hello.PK); err != nil {
		t.Fatalf("init hello git: %v", err)
	}

	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	searchSvc := domain.NewSearchService(st, repoSvc, issueSvc, gitStore)

	if _, err := issueSvc.CreateIssue(ctx, octocat.PK, "octocat", "hello", domain.IssueInput{Title: "my own bug"}); err != nil {
		t.Fatalf("create octocat issue: %v", err)
	}
	// hubot's issue is inserted directly: the domain gates creation on write
	// access this fixture does not grant, and the dashboard only needs the row.
	var fromNum int64
	if err := st.WithTx(ctx, func(tx *store.Tx) error {
		n, err := tx.AllocIssueNumber(ctx, hello.PK)
		if err != nil {
			return err
		}
		fromNum = n
		return tx.InsertIssue(ctx, &store.IssueRow{
			RepoPK: hello.PK, Number: n, Title: "handed to octocat", UserPK: hubot.PK,
		})
	}); err != nil {
		t.Fatalf("insert hubot issue: %v", err)
	}
	assignees := []string{"octocat"}
	if _, err := issueSvc.EditIssue(ctx, octocat.PK, "octocat", "hello", fromNum, domain.IssuePatch{AssigneeLogins: &assignees}); err != nil {
		t.Fatalf("assign octocat: %v", err)
	}
	// A pull-request row shares the issue number sequence; it is inserted
	// directly because the PR service needs real branches this fixture does
	// not build.
	if err := st.WithTx(ctx, func(tx *store.Tx) error {
		n, err := tx.AllocIssueNumber(ctx, hello.PK)
		if err != nil {
			return err
		}
		return tx.InsertIssue(ctx, &store.IssueRow{
			RepoPK: hello.PK, Number: n, IsPull: true, Title: "my pull request", UserPK: octocat.PK,
		})
	}); err != nil {
		t.Fatalf("insert pull row: %v", err)
	}

	renderSet, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	h := New(Deps{
		Search: searchSvc,
		URLs:   presenter.NewURLBuilder(testURLs(t)),
		Render: renderSet,
		View:   view.NewBuilder("Githome"),
		Logger: discard,
	})

	sessions := webmw.NewSessions(testSessionKey, time.Hour, func(_ context.Context, pk int64) (*view.Viewer, error) {
		if pk == octocat.PK {
			return &view.Viewer{Login: "octocat", Name: "The Octocat"}, nil
		}
		return nil, nil
	})

	root := mizu.NewRouter()
	page := root.With(sessions.Middleware(), webmw.ColorMode())
	page.Get("/_test/login", func(c *mizu.Ctx) error {
		sessions.Issue(c, octocat.PK, time.Now())
		return c.Text(http.StatusOK, "ok")
	})
	page.Get("/issues", h.Issues)
	page.Get("/pulls", h.Pulls)

	srv := httptest.NewTLSServer(root)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := srv.Client()
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return fixture{srv: srv, client: client}
}

var testSessionKey = []byte("githome-dashbrd-test-session-ky!")

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

// login hits the test login route so the client jar carries octocat's session.
func (fx fixture) login(t *testing.T) {
	t.Helper()
	resp, err := fx.client.Get(fx.srv.URL + "/_test/login")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status %d", resp.StatusCode)
	}
}

// get issues a no-redirect GET through the fixture client and returns the
// response and body.
func (fx fixture) get(t *testing.T, path string) (*http.Response, string) {
	t.Helper()
	resp, err := fx.client.Get(fx.srv.URL + path)
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

// TestDashboardAnonymousBounces sends an anonymous viewer to the sign-in form
// with return_to carrying the dashboard, the 302 github.com answers.
func TestDashboardAnonymousBounces(t *testing.T) {
	fx := newFixture(t)
	for _, path := range []string{"/issues", "/pulls"} {
		resp, _ := fx.get(t, path)
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("%s status %d, want 302", path, resp.StatusCode)
		}
		want := "/login?return_to=" + url.QueryEscape(path)
		if loc := resp.Header.Get("Location"); loc != want {
			t.Errorf("%s Location = %q, want %q", path, loc, want)
		}
	}
}

// TestDashboardIssuesCreatedTab lists the issues the viewer created, not the
// ones they were handed and not their pull requests.
func TestDashboardIssuesCreatedTab(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	resp, body := fx.get(t, "/issues")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "my own bug") {
		t.Errorf("created tab lost the viewer's issue:\n%s", body)
	}
	if strings.Contains(body, "handed to octocat") {
		t.Error("created tab leaked an issue the viewer did not create")
	}
	if strings.Contains(body, "my pull request") {
		t.Error("issues dashboard leaked a pull request")
	}
	// The repo line rides each cross-repo row.
	if !strings.Contains(body, "octocat/hello") {
		t.Errorf("row is missing its repository line:\n%s", body)
	}
}

// TestDashboardIssuesAssignedTab flips the scope to what the viewer is
// assigned and keeps the tab on the pagination-and-filter URLs.
func TestDashboardIssuesAssignedTab(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	resp, body := fx.get(t, "/issues?tab=assigned")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "handed to octocat") {
		t.Errorf("assigned tab lost the assigned issue:\n%s", body)
	}
	if strings.Contains(body, "my own bug") {
		t.Error("assigned tab leaked an unassigned issue")
	}
}

// TestDashboardPulls lists the viewer's pull requests, not their issues, and
// links each row at /pull/{n}.
func TestDashboardPulls(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	resp, body := fx.get(t, "/pulls")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "my pull request") {
		t.Errorf("pulls dashboard lost the viewer's pull request:\n%s", body)
	}
	if strings.Contains(body, "my own bug") {
		t.Error("pulls dashboard leaked an issue")
	}
	if !strings.Contains(body, "/octocat/hello/pull/") {
		t.Errorf("pull row does not link the pull page:\n%s", body)
	}
}

// TestDashboardExtraQueryNarrows runs the viewer's extra q within their slice:
// is:closed empties the open seed data and renders the blankslate.
func TestDashboardExtraQueryNarrows(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	resp, body := fx.get(t, "/issues?q=is%3Aclosed")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if strings.Contains(body, "my own bug") {
		t.Error("is:closed filter leaked an open issue")
	}
	if !strings.Contains(body, "No issues you created matched.") {
		t.Errorf("empty filter did not render the blankslate:\n%s", body)
	}
}

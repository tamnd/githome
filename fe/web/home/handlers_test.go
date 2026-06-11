package home

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

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/assets"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
)

// homeFixture is the landing-surface test harness: a live TLS httptest server
// mounting / and /dashboard with the real session middleware over a real sqlite
// store, the real repo and event services, and one seeded viewer with two
// repositories (one private) and two stored events. The TLS server matters
// because the session cookie is Secure; the test login route issues the session
// the signed-in tests ride.
type homeFixture struct {
	srv    *httptest.Server
	client *http.Client
}

func newHomeFixture(t *testing.T) homeFixture {
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
		t.Fatalf("insert user: %v", err)
	}

	hello := &store.RepoRow{OwnerPK: octocat.PK, Name: "hello", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, hello); err != nil {
		t.Fatalf("insert hello: %v", err)
	}
	// The viewer's own private repo: the dashboard is the owner's view, so it
	// shows here with the lock, unlike on a stranger's profile.
	secret := &store.RepoRow{OwnerPK: octocat.PK, Name: "secret", Private: true, DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, secret); err != nil {
		t.Fatalf("insert secret: %v", err)
	}

	// Two stored events with the rendered Events-API payloads the fan-out worker
	// writes, so the feed has a push line with its branch and an opened-issue
	// line with its numbered subject.
	push := &store.EventRow{
		Event:   domain.EventPush,
		ActorPK: octocat.PK,
		RepoPK:  hello.PK,
		Payload: `{"ref":"refs/heads/main"}`,
		Public:  true,
	}
	if err := st.InsertEvent(ctx, push); err != nil {
		t.Fatalf("insert push event: %v", err)
	}
	issue := &store.EventRow{
		Event:   domain.EventIssues,
		Action:  "opened",
		ActorPK: octocat.PK,
		RepoPK:  hello.PK,
		Payload: `{"action":"opened","issue":{"number":1,"title":"first findme issue"}}`,
		Public:  true,
	}
	if err := st.InsertEvent(ctx, issue); err != nil {
		t.Fatalf("insert issues event: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	repoSvc := domain.NewRepoService(st, gitStore)
	eventSvc := domain.NewEventService(st, repoSvc)

	renderSet, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	h := New(Deps{
		Repos:  repoSvc,
		Events: eventSvc,
		Render: renderSet,
		View:   view.NewBuilder("Githome"),
		Logger: discard,
	})

	sessions := webmw.NewSessions(testHomeSessionKey, time.Hour, func(_ context.Context, pk int64) (*view.Viewer, error) {
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

	page.Get("/{$}", h.Index)
	page.Get("/dashboard", h.Dashboard)

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

	return homeFixture{srv: srv, client: client}
}

var testHomeSessionKey = []byte("githome-homedash-test-sessionkey")

// login hits the test login route so the client jar carries the viewer's session.
func (fx homeFixture) login(t *testing.T) {
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

// getBody issues a GET and returns the response and body.
func (fx homeFixture) getBody(t *testing.T, path string) (*http.Response, string) {
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

func TestDashboardAnonymousBouncesToLogin(t *testing.T) {
	fx := newHomeFixture(t)
	resp, _ := fx.getBody(t, "/dashboard")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("anonymous /dashboard status = %d, want 302", resp.StatusCode)
	}
	want := "/login?return_to=" + url.QueryEscape("/dashboard")
	if got := resp.Header.Get("Location"); got != want {
		t.Errorf("anonymous /dashboard location = %q, want %q", got, want)
	}
}

func TestDashboardRendersReposAndFeed(t *testing.T) {
	fx := newHomeFixture(t)
	fx.login(t)
	resp, body := fx.getBody(t, "/dashboard")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/dashboard status = %d, want 200", resp.StatusCode)
	}
	// The sidebar lists both repositories: the viewer reads their own list, so
	// the private one shows too, marked with the lock.
	if !strings.Contains(body, "octocat/hello") || !strings.Contains(body, "octocat/secret") {
		t.Errorf("/dashboard is missing the repository list:\n%s", body)
	}
	if !strings.Contains(body, "home-repo-lock") {
		t.Errorf("/dashboard private repo is missing its lock:\n%s", body)
	}
	if !strings.Contains(body, `href="/new"`) {
		t.Errorf("/dashboard is missing the new-repository link:\n%s", body)
	}
	// The feed carries the seeded push line with its branch and the opened-issue
	// line with its numbered subject, the same catalog the profile renders.
	if !strings.Contains(body, "pushed to") || !strings.Contains(body, "opened an issue in") {
		t.Errorf("/dashboard is missing the feed lines:\n%s", body)
	}
	if !strings.Contains(body, "#1 first findme issue") {
		t.Errorf("/dashboard is missing the issue subject:\n%s", body)
	}
}

func TestRootMatchesDashboardForViewer(t *testing.T) {
	fx := newHomeFixture(t)
	fx.login(t)

	_, root := fx.getBody(t, "/")
	_, dash := fx.getBody(t, "/dashboard")
	if root != dash {
		t.Errorf("/ and /dashboard differ for a signed-in viewer:\n--- /\n%s\n--- /dashboard\n%s", root, dash)
	}
	if !strings.Contains(root, "home-dashboard") {
		t.Errorf("/ is missing the dashboard for a signed-in viewer:\n%s", root)
	}
}

func TestRootAnonymousLanding(t *testing.T) {
	fx := newHomeFixture(t)
	resp, body := fx.getBody(t, "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous / status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Sign in") {
		t.Errorf("anonymous / is missing the sign-in landing:\n%s", body)
	}
	if strings.Contains(body, "home-dashboard") {
		t.Errorf("anonymous / leaked the dashboard:\n%s", body)
	}
}

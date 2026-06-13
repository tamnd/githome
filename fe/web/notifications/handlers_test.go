package notifications

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

// fixture mounts /notifications over a real store, the real notifications
// domain service, and the real session middleware on a TLS server (the session
// cookie is Secure). It seeds octocat with two threads in their hello repo: an
// unread issue thread and a read pull-request thread, so the Inbox (unread) and
// All filters disagree about what they list. /_test/login issues octocat a
// session.
type fixture struct {
	srv     *httptest.Server
	client  *http.Client
	octocat int64
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
	hello := &store.RepoRow{OwnerPK: octocat.PK, Name: "hello", DefaultBranch: "master"}
	if err := st.InsertRepo(ctx, hello); err != nil {
		t.Fatalf("insert hello: %v", err)
	}

	// Two issue rows: a plain issue and a pull request sharing the number
	// sequence. Each backs one notification thread.
	issuePK := seedIssue(t, ctx, st, hello.PK, "the unread bug", false)
	pullPK := seedIssue(t, ctx, st, hello.PK, "the read pull", true)

	// The unread thread shows in both the Inbox and the All filter; the read one
	// only in All.
	upsertThread(t, ctx, st, octocat.PK, hello.PK, issuePK, "mention")
	readPK := upsertThread(t, ctx, st, octocat.PK, hello.PK, pullPK, "review_requested")
	if err := st.MarkNotificationThreadRead(ctx, readPK); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	repoSvc := domain.NewRepoService(st, gitStore)
	notifSvc := domain.NewNotificationService(st)

	renderSet, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	h := New(Deps{
		Notifications: notifSvc,
		Repos:         repoSvc,
		Render:        renderSet,
		View:          view.NewBuilder("Githome"),
		Logger:        discard,
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
	page.Get("/notifications", h.Index)

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
	return fixture{srv: srv, client: client, octocat: octocat.PK}
}

// seedIssue inserts an issue (or pull-request) row directly, allocating its
// number from the repo's sequence, and returns its PK. The domain gates
// creation on access this fixture does not grant, and the inbox only needs the
// row to resolve a thread's subject.
func seedIssue(t *testing.T, ctx context.Context, st *store.Store, repoPK int64, title string, isPull bool) int64 {
	t.Helper()
	var pk int64
	if err := st.WithTx(ctx, func(tx *store.Tx) error {
		n, err := tx.AllocIssueNumber(ctx, repoPK)
		if err != nil {
			return err
		}
		row := &store.IssueRow{RepoPK: repoPK, Number: n, Title: title, IsPull: isPull, UserPK: 1}
		if err := tx.InsertIssue(ctx, row); err != nil {
			return err
		}
		pk = row.PK
		return nil
	}); err != nil {
		t.Fatalf("seed issue %q: %v", title, err)
	}
	return pk
}

// upsertThread records a notification thread for the user on the issue and
// returns its PK.
func upsertThread(t *testing.T, ctx context.Context, st *store.Store, userPK, repoPK, issuePK int64, reason string) int64 {
	t.Helper()
	row := &store.NotificationThreadRow{UserPK: userPK, RepoPK: repoPK, IssuePK: issuePK, Reason: reason}
	if err := st.UpsertNotificationThread(ctx, row); err != nil {
		t.Fatalf("upsert thread: %v", err)
	}
	return row.PK
}

var testSessionKey = []byte("githome-notifs-test-session-key!")

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

// TestNotificationsAnonymousBounces sends an anonymous viewer to the sign-in
// form with return_to carrying the inbox, the 302 github.com answers.
func TestNotificationsAnonymousBounces(t *testing.T) {
	fx := newFixture(t)
	resp, _ := fx.get(t, "/notifications")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status %d, want 302", resp.StatusCode)
	}
	want := "/login?return_to=" + url.QueryEscape("/notifications")
	if loc := resp.Header.Get("Location"); loc != want {
		t.Errorf("Location = %q, want %q", loc, want)
	}
}

// TestNotificationsInboxListsUnread shows the Inbox (default) filter lists the
// unread thread and hides the read one, with the humanized reason and a link
// into the subject.
func TestNotificationsInboxListsUnread(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	resp, body := fx.get(t, "/notifications")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "the unread bug") {
		t.Errorf("Inbox missing the unread thread:\n%s", body)
	}
	if strings.Contains(body, "the read pull") {
		t.Errorf("Inbox should hide the read thread")
	}
	if !strings.Contains(body, "mentioned") {
		t.Errorf("Inbox missing the humanized reason 'mentioned'")
	}
	// The dead is:saved rail link the empty blankslate used to carry is gone.
	if strings.Contains(body, "is%3Asaved") || strings.Contains(body, "is:saved") {
		t.Errorf("Inbox still carries the dead Saved link")
	}
	// The subject links into the repo's issue.
	if !strings.Contains(body, "/octocat/hello/issues/") {
		t.Errorf("Inbox missing the issue link:\n%s", body)
	}
}

// TestNotificationsAllListsRead shows the All filter adds the read thread the
// Inbox hides.
func TestNotificationsAllListsRead(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	resp, body := fx.get(t, "/notifications?all=true")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "the read pull") {
		t.Errorf("All filter missing the read thread:\n%s", body)
	}
	if !strings.Contains(body, "the unread bug") {
		t.Errorf("All filter missing the unread thread")
	}
	// The read pull request links into the repo's pull route, not the issue one.
	if !strings.Contains(body, "/octocat/hello/pull/") {
		t.Errorf("All filter missing the pull link:\n%s", body)
	}
}

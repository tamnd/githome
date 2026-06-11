package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// notifFixture is a REST server with the notifications service wired over two
// users: octocat owns the public repo hello, hubot is a second user who can see
// it. Each carries their own token so a test can act as either side.
type notifFixture struct {
	srv         *httptest.Server
	ownerToken  string // octocat
	memberToken string // hubot
}

func notifServer(t testing.TB) notifFixture {
	t.Helper()
	ctx := context.Background()
	cfg := authConfig(t)

	st, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "githome.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tokenFor := func(login string) string {
		u := &store.UserRow{Login: login, Type: "User"}
		if err := st.InsertUser(ctx, u); err != nil {
			t.Fatalf("insert user %s: %v", login, err)
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
			t.Fatalf("insert token for %s: %v", login, err)
		}
		return g.Plaintext
	}
	ownerToken := tokenFor("octocat")
	memberToken := tokenFor("hubot")

	owner, err := st.UserByLogin(ctx, "octocat")
	if err != nil {
		t.Fatalf("owner lookup: %v", err)
	}
	if err := st.InsertRepo(ctx, &store.RepoRow{OwnerPK: owner.PK, Name: "hello", DefaultBranch: "main"}); err != nil {
		t.Fatalf("insert repo: %v", err)
	}

	authSvc := auth.NewService(st, "https://git.test.internal")
	t.Cleanup(authSvc.Close)
	repoSvc := domain.NewRepoService(st, git.NewStore(t.TempDir()))
	root := mizu.NewRouter()
	Mount(root, Deps{
		Config:        cfg,
		Ready:         st,
		Auth:          authSvc,
		Users:         domain.NewUserService(st),
		Repos:         repoSvc,
		Issues:        domain.NewIssueService(st, repoSvc),
		Notifications: domain.NewNotificationService(st),
		URLs:          presenter.NewURLBuilder(cfg.URLs),
		NodeFormat:    nodeid.FormatNew,
	})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	return notifFixture{srv: srv, ownerToken: ownerToken, memberToken: memberToken}
}

// seedAuthorThread has octocat open an issue and hubot comment on it, the
// population path that should leave octocat, the issue author, with one unread
// thread. Issue creation is owner-only for now, so the roles run this way
// around; commenting only needs visibility.
func seedAuthorThread(t *testing.T, fx notifFixture) {
	t.Helper()
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.ownerToken,
		`{"title":"Crash on start","body":"It crashes immediately."}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issue status %d, body %s", resp.StatusCode, body)
	}
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues/1/comments", fx.memberToken,
		`{"body":"Taking a look."}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed comment status %d, body %s", resp.StatusCode, body)
	}
}

func listThreads(t *testing.T, fx notifFixture, path, token string) []map[string]any {
	t.Helper()
	resp, body := authedGet(t, fx.srv, path, "token "+token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status %d, body %s", path, resp.StatusCode, body)
	}
	var threads []map[string]any
	if err := json.Unmarshal(body, &threads); err != nil {
		t.Fatalf("GET %s: %v, body %s", path, err, body)
	}
	return threads
}

// TestNotificationsPopulateOnIssueComment is the population contract: a comment
// on an issue puts an unread thread in the author's inbox, shaped the way
// GitHub shapes it, and never in the commenter's own.
func TestNotificationsPopulateOnIssueComment(t *testing.T) {
	fx := notifServer(t)
	seedAuthorThread(t, fx)

	threads := listThreads(t, fx, "/notifications", fx.ownerToken)
	if len(threads) != 1 {
		t.Fatalf("octocat threads = %d, want 1", len(threads))
	}
	th := threads[0]
	if id, ok := th["id"].(string); !ok || id == "" {
		t.Errorf("id = %#v, want a non-empty string", th["id"])
	}
	if th["reason"] != "author" {
		t.Errorf("reason = %v, want author", th["reason"])
	}
	if th["unread"] != true {
		t.Errorf("unread = %v, want true", th["unread"])
	}
	if th["last_read_at"] != nil {
		t.Errorf("last_read_at = %v, want null", th["last_read_at"])
	}
	subject, _ := th["subject"].(map[string]any)
	if subject == nil {
		t.Fatalf("subject missing: %v", th)
	}
	if subject["title"] != "Crash on start" {
		t.Errorf("subject.title = %v", subject["title"])
	}
	if subject["type"] != "Issue" {
		t.Errorf("subject.type = %v, want Issue", subject["type"])
	}
	wantSubject := "https://git.test.internal/api/v3/repos/octocat/hello/issues/1"
	if subject["url"] != wantSubject {
		t.Errorf("subject.url = %v, want %s", subject["url"], wantSubject)
	}
	if subject["latest_comment_url"] != wantSubject {
		t.Errorf("subject.latest_comment_url = %v, want %s", subject["latest_comment_url"], wantSubject)
	}
	repo, _ := th["repository"].(map[string]any)
	if repo == nil || repo["full_name"] != "octocat/hello" {
		t.Errorf("repository.full_name = %v, want octocat/hello", th["repository"])
	}
	wantURL := "https://git.test.internal/api/v3/notifications/threads/" + th["id"].(string)
	if th["url"] != wantURL {
		t.Errorf("url = %v, want %s", th["url"], wantURL)
	}
	if th["subscription_url"] != wantURL+"/subscription" {
		t.Errorf("subscription_url = %v", th["subscription_url"])
	}

	// The commenter does not notify themselves.
	if got := listThreads(t, fx, "/notifications", fx.memberToken); len(got) != 0 {
		t.Errorf("hubot threads = %d, want 0", len(got))
	}
}

// TestNotificationsMention checks the @mention fan-out: a comment naming hubot
// on an issue hubot otherwise has nothing to do with still reaches him.
func TestNotificationsMention(t *testing.T) {
	fx := notifServer(t)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.ownerToken,
		`{"title":"Docs pass"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issue status %d, body %s", resp.StatusCode, body)
	}
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues/1/comments", fx.ownerToken,
		`{"body":"ping @hubot could you take this?"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed comment status %d, body %s", resp.StatusCode, body)
	}
	threads := listThreads(t, fx, "/notifications", fx.memberToken)
	if len(threads) != 1 {
		t.Fatalf("threads = %d, want 1", len(threads))
	}
	if threads[0]["reason"] != "mention" {
		t.Errorf("reason = %v, want mention", threads[0]["reason"])
	}
}

// TestNotificationsAssigned checks assignment fan-out through the issue PATCH.
func TestNotificationsAssigned(t *testing.T) {
	fx := notifServer(t)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.ownerToken,
		`{"title":"Needs an owner"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issue status %d, body %s", resp.StatusCode, body)
	}
	if resp, body := authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello/issues/1", fx.ownerToken,
		`{"assignees":["hubot"]}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("assign status %d, body %s", resp.StatusCode, body)
	}
	threads := listThreads(t, fx, "/notifications", fx.memberToken)
	if len(threads) != 1 {
		t.Fatalf("threads = %d, want 1", len(threads))
	}
	if threads[0]["reason"] != "assign" {
		t.Errorf("reason = %v, want assign", threads[0]["reason"])
	}
}

// TestNotificationThreadReadFlow walks one thread through its lifecycle: fetch,
// mark read, reappear under ?all=true with last_read_at set, mark done.
func TestNotificationThreadReadFlow(t *testing.T) {
	fx := notifServer(t)
	seedAuthorThread(t, fx)
	threads := listThreads(t, fx, "/notifications", fx.ownerToken)
	if len(threads) != 1 {
		t.Fatalf("threads = %d, want 1", len(threads))
	}
	id := threads[0]["id"].(string)

	resp, body := authedGet(t, fx.srv, "/notifications/threads/"+id, "token "+fx.ownerToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get thread status %d, body %s", resp.StatusCode, body)
	}

	// Another user's id resolves to 404, never 403, so ids cannot be probed.
	if resp, _ := authedGet(t, fx.srv, "/notifications/threads/"+id, "token "+fx.memberToken); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign thread status %d, want 404", resp.StatusCode)
	}

	if resp, body := authedSend(t, fx.srv, http.MethodPatch, "/notifications/threads/"+id, fx.ownerToken, ""); resp.StatusCode != http.StatusResetContent {
		t.Fatalf("mark read status %d, want 205, body %s", resp.StatusCode, body)
	}
	if got := listThreads(t, fx, "/notifications", fx.ownerToken); len(got) != 0 {
		t.Fatalf("unread threads after mark read = %d, want 0", len(got))
	}
	all := listThreads(t, fx, "/notifications?all=true", fx.ownerToken)
	if len(all) != 1 {
		t.Fatalf("all threads = %d, want 1", len(all))
	}
	if all[0]["unread"] != false {
		t.Errorf("unread = %v, want false", all[0]["unread"])
	}
	if all[0]["last_read_at"] == nil {
		t.Errorf("last_read_at still null after mark read")
	}

	if resp, body := authedSend(t, fx.srv, http.MethodDelete, "/notifications/threads/"+id, fx.ownerToken, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("mark done status %d, want 204, body %s", resp.StatusCode, body)
	}
	if got := listThreads(t, fx, "/notifications?all=true", fx.ownerToken); len(got) != 0 {
		t.Fatalf("threads after done = %d, want 0", len(got))
	}
}

// TestNotificationsMarkAllRead checks PUT /notifications and the repo-scoped
// list and PUT variants.
func TestNotificationsMarkAllRead(t *testing.T) {
	fx := notifServer(t)
	seedAuthorThread(t, fx)

	if got := listThreads(t, fx, "/repos/octocat/hello/notifications", fx.ownerToken); len(got) != 1 {
		t.Fatalf("repo threads = %d, want 1", len(got))
	}
	if resp, _ := authedGet(t, fx.srv, "/repos/octocat/nope/notifications", "token "+fx.ownerToken); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown repo status %d, want 404", resp.StatusCode)
	}

	if resp, body := authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/notifications", fx.ownerToken, `{"last_read_at":"2026-06-11T00:00:00Z"}`); resp.StatusCode != http.StatusResetContent {
		t.Fatalf("repo mark all read status %d, want 205, body %s", resp.StatusCode, body)
	}
	if got := listThreads(t, fx, "/notifications", fx.ownerToken); len(got) != 0 {
		t.Fatalf("unread after repo mark all = %d, want 0", len(got))
	}

	// A fresh comment bumps the thread back to unread; the global PUT clears it.
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues/1/comments", fx.memberToken,
		`{"body":"One more thing."}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("second comment status %d, body %s", resp.StatusCode, body)
	}
	if got := listThreads(t, fx, "/notifications", fx.ownerToken); len(got) != 1 {
		t.Fatalf("unread after second comment = %d, want 1", len(got))
	}
	if resp, body := authedSend(t, fx.srv, http.MethodPut, "/notifications", fx.ownerToken, ""); resp.StatusCode != http.StatusResetContent {
		t.Fatalf("mark all read status %d, want 205, body %s", resp.StatusCode, body)
	}
	if got := listThreads(t, fx, "/notifications", fx.ownerToken); len(got) != 0 {
		t.Fatalf("unread after mark all = %d, want 0", len(got))
	}
}

// TestThreadSubscriptionContract round-trips the subscription endpoints:
// default state, ignore, and reset. An ignored thread stays read through the
// next event; a subscribed one goes unread again.
func TestThreadSubscriptionContract(t *testing.T) {
	fx := notifServer(t)
	seedAuthorThread(t, fx)
	id := listThreads(t, fx, "/notifications", fx.ownerToken)[0]["id"].(string)
	subPath := "/notifications/threads/" + id + "/subscription"

	resp, body := authedGet(t, fx.srv, subPath, "token "+fx.ownerToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get subscription status %d, body %s", resp.StatusCode, body)
	}
	var sub map[string]any
	if err := json.Unmarshal(body, &sub); err != nil {
		t.Fatal(err)
	}
	if sub["subscribed"] != true || sub["ignored"] != false || sub["reason"] != nil {
		t.Errorf("default subscription = %v", sub)
	}
	wantThreadURL := "https://git.test.internal/api/v3/notifications/threads/" + id
	if sub["thread_url"] != wantThreadURL {
		t.Errorf("thread_url = %v, want %s", sub["thread_url"], wantThreadURL)
	}
	if sub["url"] != wantThreadURL+"/subscription" {
		t.Errorf("url = %v", sub["url"])
	}
	if sub["created_at"] == nil {
		t.Errorf("created_at missing")
	}

	resp, body = authedSend(t, fx.srv, http.MethodPut, subPath, fx.ownerToken, `{"ignored":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put subscription status %d, body %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &sub); err != nil {
		t.Fatal(err)
	}
	if sub["ignored"] != true || sub["subscribed"] != false {
		t.Errorf("ignored subscription = %v", sub)
	}

	// Mark read, then a new comment: the ignored thread must stay read.
	if resp, _ := authedSend(t, fx.srv, http.MethodPatch, "/notifications/threads/"+id, fx.ownerToken, ""); resp.StatusCode != http.StatusResetContent {
		t.Fatalf("mark read status %d", resp.StatusCode)
	}
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues/1/comments", fx.memberToken,
		`{"body":"Still there?"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("comment status %d, body %s", resp.StatusCode, body)
	}
	if got := listThreads(t, fx, "/notifications", fx.ownerToken); len(got) != 0 {
		t.Fatalf("ignored thread went unread: %d threads", len(got))
	}

	if resp, _ := authedSend(t, fx.srv, http.MethodDelete, subPath, fx.ownerToken, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete subscription status %d, want 204", resp.StatusCode)
	}
	resp, body = authedGet(t, fx.srv, subPath, "token "+fx.ownerToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get subscription status %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, &sub); err != nil {
		t.Fatal(err)
	}
	if sub["subscribed"] != true || sub["ignored"] != false {
		t.Errorf("reset subscription = %v", sub)
	}
}

// TestNotificationsRequireAuth checks the whole surface is 401 for anonymous
// callers and that unknown thread ids are 404.
func TestNotificationsRequireAuth(t *testing.T) {
	fx := notifServer(t)
	for _, probe := range []struct{ method, path string }{
		{http.MethodGet, "/notifications"},
		{http.MethodPut, "/notifications"},
		{http.MethodGet, "/notifications/threads/1"},
		{http.MethodPatch, "/notifications/threads/1"},
		{http.MethodGet, "/notifications/threads/1/subscription"},
		{http.MethodGet, "/repos/octocat/hello/notifications"},
	} {
		resp, _ := authedSend(t, fx.srv, probe.method, probe.path, "", "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s anonymous status %d, want 401", probe.method, probe.path, resp.StatusCode)
		}
	}
	if resp, _ := authedGet(t, fx.srv, "/notifications/threads/999", "token "+fx.memberToken); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown thread status %d, want 404", resp.StatusCode)
	}
}

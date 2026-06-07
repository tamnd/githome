package issues

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
	"github.com/tamnd/githome/markup"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// authedFixture mounts the issues surface with the real session and CSRF
// middleware over a TLS server (the cookies are Secure, so plain HTTP would drop
// them). It seeds the repo owner and exposes a /_test/login route that issues the
// owner a session, so a test can act as the writer and exercise the mutation
// handlers end to end: the form post carries the double-submit token, the service
// authorizes the write, and the handler redirects.
type authedFixture struct {
	srv     *httptest.Server
	client  *http.Client
	issues  *domain.IssueService
	owner   string
	repo    string
	ownerPK int64
	openNum int64
}

func newAuthedFixture(t *testing.T) authedFixture {
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
	hello := &store.RepoRow{OwnerPK: owner.PK, Name: "hello", DefaultBranch: "master"}
	if err := st.InsertRepo(ctx, hello); err != nil {
		t.Fatalf("insert hello: %v", err)
	}
	gitStore := git.NewStore(t.TempDir())
	if _, err := gitStore.Init(hello.PK); err != nil {
		t.Fatalf("init hello git: %v", err)
	}

	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)

	if _, err := issueSvc.CreateLabel(ctx, owner.PK, "octocat", "hello", domain.LabelInput{Name: "bug", Color: "d73a4a"}); err != nil {
		t.Fatalf("create label: %v", err)
	}
	body := "the open issue body"
	open, err := issueSvc.CreateIssue(ctx, owner.PK, "octocat", "hello", domain.IssueInput{Title: "first issue", Body: &body})
	if err != nil {
		t.Fatalf("create issue: %v", err)
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

	// The session middleware needs a viewer lookup; map the seeded owner pk to a
	// viewer whose login matches, so the comment-author edit gate and the @me
	// rewrite resolve to the real user.
	sessions := webmw.NewSessions(testSessionKey, time.Hour, func(_ context.Context, pk int64) (*view.Viewer, error) {
		if pk == owner.PK {
			return &view.Viewer{Login: "octocat", Name: "The Octocat"}, nil
		}
		return nil, nil
	})
	csrf := webmw.NewCSRF(renderSet)

	root := mizu.NewRouter()
	page := root.With(sessions.Middleware(), webmw.ColorMode(), csrf.Middleware())

	// A tiny login route stands in for the real auth flow: it issues the owner a
	// session cookie so the rest of the test acts as the writer.
	page.Get("/_test/login", func(c *mizu.Ctx) error {
		sessions.Issue(c, owner.PK, time.Now())
		return c.Text(http.StatusOK, "ok")
	})

	ig := page.With(h.Resolve)
	ig.Get("/{owner}/{repo}/issues/{number}", h.Show)
	ig.Get("/{owner}/{repo}/issues/new", h.New)
	ig.Post("/{owner}/{repo}/issues", h.Create)
	ig.Post("/{owner}/{repo}/issues/{number}/comments", h.CreateComment)
	ig.Post("/{owner}/{repo}/issues/{number}/state", h.ToggleState)
	ig.Post("/{owner}/{repo}/issues/{number}/title", h.EditTitle)
	ig.Post("/{owner}/{repo}/issues/{number}/edit", h.EditSidebar)
	ig.Post("/{owner}/{repo}/issues/{number}/reactions", h.ToggleIssueReaction)
	ig.Post("/{owner}/{repo}/issues/{number}/comments/{comment}", h.EditComment)
	ig.Post("/{owner}/{repo}/issues/{number}/comments/{comment}/delete", h.DeleteComment)
	ig.Post("/{owner}/{repo}/issues/{number}/comments/{comment}/reactions", h.ToggleCommentReaction)

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

	fx := authedFixture{
		srv: srv, client: client, issues: issueSvc,
		owner: "octocat", repo: "hello", ownerPK: owner.PK, openNum: open.Number,
	}
	fx.login(t)
	return fx
}

// testSessionKey is a fixed 32-byte key for the test session signer.
var testSessionKey = []byte("githome-issues-test-session-key!")

// login hits the test login route so the client jar carries the owner's session.
func (fx authedFixture) login(t *testing.T) {
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

// csrfToken issues a GET so the CSRF cookie is set, then reads the token the form
// echoes into its hidden field. The double submit needs both halves, and the form
// field is the half a no-JS post would carry.
func (fx authedFixture) csrfToken(t *testing.T, path string) string {
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
	token := extractCSRF(string(b))
	if token == "" {
		t.Fatalf("no csrf token in %s", path)
	}
	return token
}

// post submits a form with the CSRF field included and returns the response.
func (fx authedFixture) post(t *testing.T, path, csrf string, form url.Values) *http.Response {
	t.Helper()
	form.Set("_csrf", csrf)
	resp, err := fx.client.PostForm(fx.srv.URL+path, form)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// extractCSRF pulls the first _csrf hidden-field value out of rendered HTML.
func extractCSRF(html string) string {
	const marker = `name="_csrf" value="`
	i := strings.Index(html, marker)
	if i < 0 {
		return ""
	}
	rest := html[i+len(marker):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func TestCreateIssueRedirectsToDetail(t *testing.T) {
	fx := newAuthedFixture(t)
	token := fx.csrfToken(t, "/octocat/hello/issues/new")
	resp := fx.post(t, "/octocat/hello/issues", token, url.Values{
		"title": {"a brand new issue"},
		"body":  {"with a body"},
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create status %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/octocat/hello/issues/") {
		t.Fatalf("create redirected to %q, want the issue detail", loc)
	}
	// The new issue is really there.
	issues, _, err := fx.issues.ListIssues(context.Background(), fx.ownerPK, "octocat", "hello", domain.IssueQuery{State: "open"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, iss := range issues {
		if iss.Title == "a brand new issue" {
			found = true
		}
	}
	if !found {
		t.Errorf("the created issue is not in the open list")
	}
}

func TestCreateIssueEmptyTitleReRenders(t *testing.T) {
	fx := newAuthedFixture(t)
	token := fx.csrfToken(t, "/octocat/hello/issues/new")
	resp := fx.post(t, "/octocat/hello/issues", token, url.Values{"title": {"   "}})
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("empty-title status %d, want 200 re-render", resp.StatusCode)
	}
	if !strings.Contains(string(body), "An issue needs a title") {
		t.Errorf("empty-title re-render is missing the inline message:\n%s", body)
	}
}

func TestCreateCommentRedirectsToAnchor(t *testing.T) {
	fx := newAuthedFixture(t)
	path := "/octocat/hello/issues/" + itoa(fx.openNum)
	token := fx.csrfToken(t, path)
	resp := fx.post(t, path+"/comments", token, url.Values{"body": {"a fresh comment"}})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("comment status %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "#issuecomment-") {
		t.Errorf("comment redirected to %q, want a comment anchor", loc)
	}
	// Reload and see the comment.
	_, page := fx.getBody(t, path)
	if !strings.Contains(page, "a fresh comment") {
		t.Errorf("the new comment is missing from the reloaded issue")
	}
}

func TestToggleStateClosesAndReopens(t *testing.T) {
	fx := newAuthedFixture(t)
	path := "/octocat/hello/issues/" + itoa(fx.openNum)

	token := fx.csrfToken(t, path)
	resp := fx.post(t, path+"/state", token, url.Values{})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("close status %d, want 303", resp.StatusCode)
	}
	iss, err := fx.issues.GetIssue(context.Background(), fx.ownerPK, "octocat", "hello", fx.openNum)
	if err != nil {
		t.Fatalf("get after close: %v", err)
	}
	if iss.State != "closed" {
		t.Fatalf("state after close = %q, want closed", iss.State)
	}

	// Toggling again reopens it.
	token = fx.csrfToken(t, path)
	resp = fx.post(t, path+"/state", token, url.Values{})
	_ = resp.Body.Close()
	iss, err = fx.issues.GetIssue(context.Background(), fx.ownerPK, "octocat", "hello", fx.openNum)
	if err != nil {
		t.Fatalf("get after reopen: %v", err)
	}
	if iss.State != "open" {
		t.Errorf("state after reopen = %q, want open", iss.State)
	}
}

func TestEditTitleSaves(t *testing.T) {
	fx := newAuthedFixture(t)
	path := "/octocat/hello/issues/" + itoa(fx.openNum)
	token := fx.csrfToken(t, path)
	resp := fx.post(t, path+"/title", token, url.Values{"title": {"a renamed issue"}})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("title status %d, want 303", resp.StatusCode)
	}
	iss, err := fx.issues.GetIssue(context.Background(), fx.ownerPK, "octocat", "hello", fx.openNum)
	if err != nil {
		t.Fatalf("get after rename: %v", err)
	}
	if iss.Title != "a renamed issue" {
		t.Errorf("title = %q, want renamed", iss.Title)
	}
}

func TestEditSidebarReplacesLabels(t *testing.T) {
	fx := newAuthedFixture(t)
	ctx := context.Background()
	if _, err := fx.issues.CreateLabel(ctx, fx.ownerPK, "octocat", "hello", domain.LabelInput{Name: "wontfix", Color: "ffffff"}); err != nil {
		t.Fatalf("create label: %v", err)
	}
	path := "/octocat/hello/issues/" + itoa(fx.openNum)
	token := fx.csrfToken(t, path)
	resp := fx.post(t, path+"/edit", token, url.Values{"labels": {"wontfix"}})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("edit status %d, want 303", resp.StatusCode)
	}
	iss, err := fx.issues.GetIssue(ctx, fx.ownerPK, "octocat", "hello", fx.openNum)
	if err != nil {
		t.Fatalf("get after label edit: %v", err)
	}
	if len(iss.Labels) != 1 || iss.Labels[0].Name != "wontfix" {
		t.Errorf("labels after edit = %+v, want [wontfix]", iss.Labels)
	}
}

func TestToggleIssueReaction(t *testing.T) {
	fx := newAuthedFixture(t)
	ctx := context.Background()
	path := "/octocat/hello/issues/" + itoa(fx.openNum)

	token := fx.csrfToken(t, path)
	resp := fx.post(t, path+"/reactions", token, url.Values{"content": {"heart"}})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("react status %d, want 303", resp.StatusCode)
	}
	list, err := fx.issues.ListIssueReactions(ctx, fx.ownerPK, "octocat", "hello", fx.openNum)
	if err != nil {
		t.Fatalf("list reactions: %v", err)
	}
	if len(list) != 1 || list[0].Content != "heart" {
		t.Fatalf("after react, reactions = %+v, want one heart", list)
	}

	// Toggling the same content again removes it.
	token = fx.csrfToken(t, path)
	resp = fx.post(t, path+"/reactions", token, url.Values{"content": {"heart"}})
	_ = resp.Body.Close()
	list, err = fx.issues.ListIssueReactions(ctx, fx.ownerPK, "octocat", "hello", fx.openNum)
	if err != nil {
		t.Fatalf("list reactions after toggle: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("after toggle off, reactions = %+v, want none", list)
	}
}

func TestEditAndDeleteComment(t *testing.T) {
	fx := newAuthedFixture(t)
	ctx := context.Background()
	cm, err := fx.issues.CreateComment(ctx, fx.ownerPK, "octocat", "hello", fx.openNum, "original text")
	if err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	path := "/octocat/hello/issues/" + itoa(fx.openNum)

	token := fx.csrfToken(t, path)
	resp := fx.post(t, path+"/comments/"+itoa(cm.ID), token, url.Values{"body": {"edited text"}})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("edit comment status %d, want 303", resp.StatusCode)
	}
	_, page := fx.getBody(t, path)
	if !strings.Contains(page, "edited text") || strings.Contains(page, "original text") {
		t.Errorf("comment edit did not take in the reloaded page")
	}

	token = fx.csrfToken(t, path)
	resp = fx.post(t, path+"/comments/"+itoa(cm.ID)+"/delete", token, url.Values{})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete comment status %d, want 303", resp.StatusCode)
	}
	_, page = fx.getBody(t, path)
	if strings.Contains(page, "edited text") {
		t.Errorf("deleted comment is still on the page")
	}
}

func TestMutationWithoutCSRFIsForbidden(t *testing.T) {
	fx := newAuthedFixture(t)
	path := "/octocat/hello/issues/" + itoa(fx.openNum)
	// Post with no token at all: the guard rejects with the themed 403.
	resp, err := fx.client.PostForm(fx.srv.URL+path+"/comments", url.Values{"body": {"sneaky"}})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("missing-CSRF status %d, want 403", resp.StatusCode)
	}
}

// getBody issues a GET through the authed client and returns the response and
// body, for asserting on a reloaded page.
func (fx authedFixture) getBody(t *testing.T, path string) (*http.Response, string) {
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

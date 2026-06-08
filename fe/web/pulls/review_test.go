package pulls

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os/exec"
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

// reviewWebFixture mounts the pull-request surface with the live review service
// and the real session and CSRF middleware over a TLS server (the cookies are
// Secure). It seeds two users: octocat, the repo owner and the pull request's
// author, and hubot, a signed-in reviewer who is not the author. A /_test/login
// route issues either one a session, so a test can act as the author or the
// reviewer and exercise the F5 mutations end to end: open an inline thread, reply,
// resolve and unresolve, and submit a verdict. The seeding needs the git binary,
// so the suite skips when git is unavailable.
type reviewWebFixture struct {
	srv        *httptest.Server
	client     *http.Client
	reviews    *domain.ReviewService
	owner      string
	repo       string
	ownerPK    int64
	reviewerPK int64
	prNum      int64
}

func newReviewWebFixture(t *testing.T) reviewWebFixture {
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
		t.Fatalf("insert owner: %v", err)
	}
	reviewer := &store.UserRow{Login: "hubot", Type: "User"}
	if err := st.InsertUser(ctx, reviewer); err != nil {
		t.Fatalf("insert reviewer: %v", err)
	}
	hello := &store.RepoRow{OwnerPK: owner.PK, Name: "hello", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, hello); err != nil {
		t.Fatalf("insert hello: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	prBareRepo(t, gitStore, hello.PK)

	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	prSvc := domain.NewPRService(st, repoSvc, issueSvc, gitStore)
	reviewSvc := domain.NewReviewService(st, repoSvc, prSvc, issueSvc, gitStore)

	body := "please review the new file"
	pr, err := prSvc.CreatePR(ctx, owner.PK, "octocat", "hello", domain.PRInput{
		Title: "add b", Body: &body, Base: "main", Head: "feature",
	})
	if err != nil {
		t.Fatalf("create pr: %v", err)
	}

	renderSet, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	h := New(Deps{
		Pulls:   prSvc,
		Issues:  issueSvc,
		Reviews: reviewSvc,
		Repos:   repoSvc,
		URLs:    presenter.NewURLBuilder(testURLs(t)),
		Render:  renderSet,
		View:    view.NewBuilder("Githome"),
		Markup:  markup.New(markup.Config{BaseURL: testURLs(t).HTML.String(), Logger: discard}),
		Logger:  discard,
	})

	// The session middleware maps either seeded pk to a viewer whose login matches,
	// so the author gate (octocat) and the non-author reviewer (hubot) both resolve.
	sessions := webmw.NewSessions(reviewTestSessionKey, time.Hour, func(_ context.Context, pk int64) (*view.Viewer, error) {
		switch pk {
		case owner.PK:
			return &view.Viewer{Login: "octocat", Name: "The Octocat"}, nil
		case reviewer.PK:
			return &view.Viewer{Login: "hubot"}, nil
		}
		return nil, nil
	})
	csrf := webmw.NewCSRF(renderSet)

	root := mizu.NewRouter()
	page := root.With(sessions.Middleware(), webmw.ColorMode(), csrf.Middleware())

	// A tiny login route stands in for the real auth flow: it issues the user named
	// by ?pk a session cookie, so the rest of the test acts as that user.
	page.Get("/_test/login", func(c *mizu.Ctx) error {
		pk := owner.PK
		if c.Query("pk") == itoa(reviewer.PK) {
			pk = reviewer.PK
		}
		sessions.Issue(c, pk, time.Now())
		return c.Text(http.StatusOK, "ok")
	})

	pg := page.With(h.Resolve)
	pg.Get("/{owner}/{repo}/pull/{number}", h.Conversation)
	pg.Get("/{owner}/{repo}/pull/{number}/files", h.Files)
	pg.Post("/{owner}/{repo}/pull/{number}/review-comments", h.CreateReviewComment)
	pg.Post("/{owner}/{repo}/pull/{number}/review-comments/{comment}/replies", h.ReplyReviewComment)
	pg.Post("/{owner}/{repo}/pull/{number}/review-threads/{root}/resolve", h.ToggleReviewThread)
	pg.Post("/{owner}/{repo}/pull/{number}/reviews", h.SubmitReview)

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

	return reviewWebFixture{
		srv: srv, client: client, reviews: reviewSvc,
		owner: "octocat", repo: "hello",
		ownerPK: owner.PK, reviewerPK: reviewer.PK, prNum: pr.Number,
	}
}

// reviewTestSessionKey is a fixed 32-byte key for the test session signer.
var reviewTestSessionKey = []byte("githome-review-test-session-key!")

// loginAs hits the test login route so the client jar carries the named user's
// session. An empty pk logs in the owner.
func (fx reviewWebFixture) loginAs(t *testing.T, pk int64) {
	t.Helper()
	resp, err := fx.client.Get(fx.srv.URL + "/_test/login?pk=" + itoa(pk))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status %d", resp.StatusCode)
	}
}

// pullPath is the PR's Conversation path, the base the tab paths hang off.
func (fx reviewWebFixture) pullPath() string {
	return "/" + fx.owner + "/" + fx.repo + "/pull/" + itoa(fx.prNum)
}

// reviewCSRF issues a GET so the CSRF cookie is set and reads the token a form on
// the page echoes into its hidden field, the half a no-JS post carries.
func (fx reviewWebFixture) reviewCSRF(t *testing.T, path string) string {
	t.Helper()
	resp, body := fx.getBody(t, path)
	_ = resp
	token := reviewExtractCSRF(body)
	if token == "" {
		t.Fatalf("no csrf token in %s", path)
	}
	return token
}

// post submits a form with the CSRF field included and returns the response.
func (fx reviewWebFixture) post(t *testing.T, path, csrf string, form url.Values) *http.Response {
	t.Helper()
	form.Set("_csrf", csrf)
	resp, err := fx.client.PostForm(fx.srv.URL+path, form)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// getBody issues a GET through the authed client and returns the response and body.
func (fx reviewWebFixture) getBody(t *testing.T, path string) (*http.Response, string) {
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

// reviewExtractCSRF pulls the first _csrf hidden-field value out of rendered HTML.
func reviewExtractCSRF(html string) string {
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

// seedThread opens an inline thread on b.txt line 1 (the added line) through the
// live service as the given user, returning the root comment's id, the anchor the
// reply and resolve tests address.
func (fx reviewWebFixture) seedThread(t *testing.T, actorPK int64, body string) int64 {
	t.Helper()
	line := int64(1)
	cm, err := fx.reviews.CreateComment(context.Background(), actorPK, fx.owner, fx.repo, fx.prNum, domain.ReviewCommentInput{
		Path: "b.txt", Body: body, Side: "RIGHT", Line: &line,
	})
	if err != nil {
		t.Fatalf("seed thread: %v", err)
	}
	return cm.ID
}

func TestInlineCommentOpensThread(t *testing.T) {
	fx := newReviewWebFixture(t)
	fx.loginAs(t, fx.reviewerPK)
	filesPath := fx.pullPath() + "/files"
	token := fx.reviewCSRF(t, filesPath)

	resp := fx.post(t, fx.pullPath()+"/review-comments", token, url.Values{
		"path": {"b.txt"}, "side": {"RIGHT"}, "line": {"1"},
		"body": {"this needs a test"},
	})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("inline comment status %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "#discussion_r") {
		t.Errorf("inline comment redirected to %q, want a discussion anchor", loc)
	}
	// The thread renders against the diff on a reload.
	_, page := fx.getBody(t, filesPath)
	if !strings.Contains(page, "this needs a test") {
		t.Errorf("the new inline comment is missing from the Files tab:\n%s", page)
	}
	if !strings.Contains(page, "review-thread") {
		t.Errorf("the Files tab did not render the thread shell")
	}
}

func TestInlineCommentBlankBodyIsNotFound(t *testing.T) {
	fx := newReviewWebFixture(t)
	fx.loginAs(t, fx.reviewerPK)
	token := fx.reviewCSRF(t, fx.pullPath()+"/files")

	resp := fx.post(t, fx.pullPath()+"/review-comments", token, url.Values{
		"path": {"b.txt"}, "side": {"RIGHT"}, "line": {"1"}, "body": {"   "},
	})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("blank inline body status %d, want 404", resp.StatusCode)
	}
}

func TestInlineCommentOffDiffRedirectsToFiles(t *testing.T) {
	fx := newReviewWebFixture(t)
	fx.loginAs(t, fx.reviewerPK)
	token := fx.reviewCSRF(t, fx.pullPath()+"/files")

	// Line 9999 is outside the rendered hunk, so the domain rejects the anchor as a
	// validation miss and the handler lands the viewer back on the Files tab.
	resp := fx.post(t, fx.pullPath()+"/review-comments", token, url.Values{
		"path": {"b.txt"}, "side": {"RIGHT"}, "line": {"9999"},
		"body": {"off the diff"},
	})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("off-diff status %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.HasSuffix(loc, "/files") {
		t.Errorf("off-diff redirected to %q, want the Files tab", loc)
	}
}

func TestReplyAppendsToThread(t *testing.T) {
	fx := newReviewWebFixture(t)
	root := fx.seedThread(t, fx.reviewerPK, "the root comment")
	fx.loginAs(t, fx.ownerPK)
	token := fx.reviewCSRF(t, fx.pullPath()+"/files")

	resp := fx.post(t, fx.pullPath()+"/review-comments/"+itoa(root)+"/replies", token, url.Values{
		"body": {"agreed, on it"},
	})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("reply status %d, want 303", resp.StatusCode)
	}
	_, page := fx.getBody(t, fx.pullPath()+"/files")
	if !strings.Contains(page, "the root comment") || !strings.Contains(page, "agreed, on it") {
		t.Errorf("the reply is missing from the reloaded thread:\n%s", page)
	}
}

func TestResolveAndUnresolveThread(t *testing.T) {
	fx := newReviewWebFixture(t)
	root := fx.seedThread(t, fx.reviewerPK, "resolve me")
	fx.loginAs(t, fx.ownerPK) // the owner has write access, the resolve gate

	token := fx.reviewCSRF(t, fx.pullPath()+"/files")
	resp := fx.post(t, fx.pullPath()+"/review-threads/"+itoa(root)+"/resolve", token, url.Values{
		"resolved": {"true"},
	})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("resolve status %d, want 303", resp.StatusCode)
	}
	if !fx.threadResolved(t, root) {
		t.Errorf("thread is not resolved after the resolve post")
	}

	token = fx.reviewCSRF(t, fx.pullPath()+"/files")
	resp = fx.post(t, fx.pullPath()+"/review-threads/"+itoa(root)+"/resolve", token, url.Values{
		"resolved": {"false"},
	})
	_ = resp.Body.Close()
	if fx.threadResolved(t, root) {
		t.Errorf("thread is still resolved after the unresolve post")
	}
}

// threadResolved reads the thread's resolved flag back through the service.
func (fx reviewWebFixture) threadResolved(t *testing.T, root int64) bool {
	t.Helper()
	threads, err := fx.reviews.ReviewThreads(context.Background(), fx.ownerPK, fx.owner, fx.repo, fx.prNum)
	if err != nil {
		t.Fatalf("review threads: %v", err)
	}
	for _, th := range threads {
		if th.ID == root {
			return th.IsResolved
		}
	}
	t.Fatalf("thread %d not found", root)
	return false
}

func TestSubmitApproveAsReviewer(t *testing.T) {
	fx := newReviewWebFixture(t)
	fx.loginAs(t, fx.reviewerPK)
	token := fx.reviewCSRF(t, fx.pullPath())

	resp := fx.post(t, fx.pullPath()+"/reviews", token, url.Values{
		"event": {"APPROVE"}, "body": {"looks good to me"},
	})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("approve status %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "#pullrequestreview-") {
		t.Errorf("approve redirected to %q, want a review anchor", loc)
	}
	// The decision derives APPROVED from the one approval.
	dec, err := fx.reviews.ReviewDecision(context.Background(), fx.ownerPK, fx.owner, fx.repo, fx.prNum)
	if err != nil {
		t.Fatalf("review decision: %v", err)
	}
	if dec == nil || *dec != domain.ReviewApproved {
		t.Fatalf("decision = %v, want APPROVED", dec)
	}
	// The submitted review and the decision both surface on the Conversation page.
	_, page := fx.getBody(t, fx.pullPath())
	if !strings.Contains(page, "approved these changes") {
		t.Errorf("conversation timeline is missing the approval:\n%s", page)
	}
	if !strings.Contains(page, "Changes approved") {
		t.Errorf("merge box is missing the approved rollup")
	}
}

func TestSubmitCommentVerdictInterleavesTimeline(t *testing.T) {
	fx := newReviewWebFixture(t)
	fx.loginAs(t, fx.reviewerPK)
	token := fx.reviewCSRF(t, fx.pullPath())

	resp := fx.post(t, fx.pullPath()+"/reviews", token, url.Values{
		"event": {"COMMENT"}, "body": {"a general note on the change"},
	})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("comment verdict status %d, want 303", resp.StatusCode)
	}
	_, page := fx.getBody(t, fx.pullPath())
	// Both the opening body and the submitted review render in one timeline.
	if !strings.Contains(page, "please review the new file") {
		t.Errorf("timeline lost the opening body")
	}
	if !strings.Contains(page, "a general note on the change") {
		t.Errorf("timeline is missing the submitted comment review:\n%s", page)
	}
}

func TestAuthorCannotApproveOwnPull(t *testing.T) {
	fx := newReviewWebFixture(t)
	fx.loginAs(t, fx.ownerPK) // octocat is the author
	token := fx.reviewCSRF(t, fx.pullPath())

	resp, err := fx.client.PostForm(fx.srv.URL+fx.pullPath()+"/reviews", url.Values{
		"_csrf": {token}, "event": {"APPROVE"}, "body": {"self approve"},
	})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("self-approve status %d, want 200 re-render", resp.StatusCode)
	}
	if !strings.Contains(string(b), "cannot approve your own pull request") {
		t.Errorf("self-approve re-render is missing the inline message:\n%s", b)
	}
	// No approval was recorded, so there is no decision.
	dec, err := fx.reviews.ReviewDecision(context.Background(), fx.ownerPK, fx.owner, fx.repo, fx.prNum)
	if err != nil {
		t.Fatalf("review decision: %v", err)
	}
	if dec != nil {
		t.Errorf("decision = %v, want none after a blocked self-approval", *dec)
	}
}

func TestRequestChangesNeedsBody(t *testing.T) {
	fx := newReviewWebFixture(t)
	fx.loginAs(t, fx.reviewerPK)
	token := fx.reviewCSRF(t, fx.pullPath())

	resp, err := fx.client.PostForm(fx.srv.URL+fx.pullPath()+"/reviews", url.Values{
		"_csrf": {token}, "event": {"REQUEST_CHANGES"}, "body": {"  "},
	})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bodyless request-changes status %d, want 200 re-render", resp.StatusCode)
	}
	if !strings.Contains(string(b), "needs a comment") {
		t.Errorf("bodyless request-changes is missing the inline message:\n%s", b)
	}
}

func TestSubmitReviewUnknownEventIsNotFound(t *testing.T) {
	fx := newReviewWebFixture(t)
	fx.loginAs(t, fx.reviewerPK)
	token := fx.reviewCSRF(t, fx.pullPath())

	resp := fx.post(t, fx.pullPath()+"/reviews", token, url.Values{
		"event": {"LGTM"}, "body": {"nope"},
	})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown-event status %d, want 404", resp.StatusCode)
	}
}

func TestReviewMutationWithoutCSRFIsForbidden(t *testing.T) {
	fx := newReviewWebFixture(t)
	fx.loginAs(t, fx.reviewerPK)
	// Post with no token: the guard rejects with the themed 403.
	resp, err := fx.client.PostForm(fx.srv.URL+fx.pullPath()+"/reviews", url.Values{
		"event": {"APPROVE"}, "body": {"sneaky"},
	})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("missing-CSRF status %d, want 403", resp.StatusCode)
	}
}

package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// reviewFixture is a REST server backed by the feature-branch repository the pull
// fixture uses, with the review and checks services mounted and a second user who
// can review the owner's pull request. The owner cannot approve their own pull, so
// the reviewer drives the approve, change-request, and comment paths while the
// owner drives dismiss and the checks reports.
type reviewFixture struct {
	srv         *httptest.Server
	ownerToken  string
	reviewToken string
	st          *store.Store
	ctx         context.Context
}

func reviewServer(t *testing.T) reviewFixture {
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
	ownerToken := seedToken(t, st, owner.PK)

	reviewer := &store.UserRow{Login: "hubot", Type: "User"}
	if err := st.InsertUser(ctx, reviewer); err != nil {
		t.Fatalf("insert reviewer: %v", err)
	}
	reviewToken := seedToken(t, st, reviewer.PK)

	repo := &store.RepoRow{OwnerPK: owner.PK, Name: "hello", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("insert repo: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	bareFeatureRepo(t, gitStore, repo.PK)

	authSvc := auth.NewService(st, "https://git.test.internal")
	t.Cleanup(authSvc.Close)
	cfg := authConfig(t)
	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	pullSvc := domain.NewPRService(st, repoSvc, issueSvc, gitStore)
	reviewSvc := domain.NewReviewService(st, repoSvc, pullSvc, issueSvc, gitStore)
	checksSvc := domain.NewChecksService(st, repoSvc, issueSvc, gitStore)

	root := mizu.NewRouter()
	Mount(root, Deps{
		Config:     cfg,
		Ready:      st,
		Auth:       authSvc,
		Users:      domain.NewUserService(st),
		Repos:      repoSvc,
		Issues:     issueSvc,
		Pulls:      pullSvc,
		Reviews:    reviewSvc,
		Checks:     checksSvc,
		URLs:       presenter.NewURLBuilder(cfg.URLs),
		NodeFormat: nodeid.FormatNew,
	})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	fx := reviewFixture{srv: srv, ownerToken: ownerToken, reviewToken: reviewToken, st: st, ctx: ctx}
	fx.openPull(t)
	return fx
}

// seedToken inserts a repo-scoped classic PAT for a user and returns its
// plaintext.
func seedToken(t *testing.T, st *store.Store, userPK int64) string {
	t.Helper()
	return seedScopedToken(t, st, userPK, "repo")
}

// seedScopedToken inserts a classic PAT carrying the given scope string.
func seedScopedToken(t *testing.T, st *store.Store, userPK int64, scopes string) string {
	t.Helper()
	g, err := auth.GenerateToken(auth.PrefixClassicPAT)
	if err != nil {
		t.Fatal(err)
	}
	hash := g.Hash
	if err := st.InsertToken(context.Background(), &store.TokenRow{
		UserPK: &userPK, TokenHash: hash[:], TokenPrefix: auth.PrefixClassicPAT,
		LastEight: g.Last8, Kind: "pat", Scopes: scopes,
	}); err != nil {
		t.Fatalf("insert token: %v", err)
	}
	return g.Plaintext
}

// openPull seeds the canonical feature->main pull request.
func (fx reviewFixture) openPull(t *testing.T) {
	t.Helper()
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls", fx.ownerToken,
		`{"title":"Add a feature","body":"It adds a feature.","head":"feature","base":"main"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed pull status %d, body %s", resp.StatusCode, body)
	}
}

func TestReviewApproveContract(t *testing.T) {
	fx := reviewServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls/1/reviews", fx.reviewToken,
		`{"event":"APPROVE","body":"Looks good."}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"state":"APPROVED"`) {
		t.Errorf("review not approved: %s", body)
	}
	assertWriteGolden(t, "review_approve.golden.json", body)
}

func TestReviewSelfApproveRejected(t *testing.T) {
	fx := reviewServer(t)
	// The pull author is the owner; the owner cannot approve their own pull.
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls/1/reviews", fx.ownerToken,
		`{"event":"APPROVE","body":"Self approve."}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422, body %s", resp.StatusCode, body)
	}
}

func TestReviewChangesRequestedNeedsBody(t *testing.T) {
	fx := reviewServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls/1/reviews", fx.reviewToken,
		`{"event":"REQUEST_CHANGES"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422, body %s", resp.StatusCode, body)
	}
}

func TestReviewListContract(t *testing.T) {
	fx := reviewServer(t)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls/1/reviews", fx.reviewToken,
		`{"event":"APPROVE","body":"Looks good."}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("seed review status %d, body %s", resp.StatusCode, body)
	}
	resp, body := get(t, fx.srv, "/repos/octocat/hello/pulls/1/reviews")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "review_list.golden.json", body)
}

func TestReviewCommentOnDiffContract(t *testing.T) {
	fx := reviewServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls/1/comments", fx.reviewToken,
		`{"path":"feature.txt","line":1,"side":"RIGHT","body":"Nice line."}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"path":"feature.txt"`) {
		t.Errorf("comment path missing: %s", body)
	}
	assertWriteGolden(t, "review_comment.golden.json", body)
}

func TestReviewCommentOffDiffRejected(t *testing.T) {
	fx := reviewServer(t)
	// README.md is not part of the feature pull's diff; anchoring there is a 422.
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls/1/comments", fx.reviewToken,
		`{"path":"README.md","line":1,"side":"RIGHT","body":"Off diff."}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422, body %s", resp.StatusCode, body)
	}
}

func TestReviewCommentReplyContract(t *testing.T) {
	fx := reviewServer(t)
	_, root := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls/1/comments", fx.reviewToken,
		`{"path":"feature.txt","line":1,"side":"RIGHT","body":"Root comment."}`)
	id := jsonInt(t, root, "id")
	resp, body := authedSend(t, fx.srv, http.MethodPost,
		"/repos/octocat/hello/pulls/comments/"+itoa(id)+"/replies", fx.ownerToken, `{"body":"A reply."}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"in_reply_to_id"`) {
		t.Errorf("reply missing in_reply_to_id: %s", body)
	}
}

func TestReviewDismissContract(t *testing.T) {
	fx := reviewServer(t)
	_, rev := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls/1/reviews", fx.reviewToken,
		`{"event":"APPROVE","body":"Looks good."}`)
	id := jsonInt(t, rev, "id")
	// The owner has write access and may dismiss a reviewer's verdict.
	resp, body := authedSend(t, fx.srv, http.MethodPut,
		"/repos/octocat/hello/pulls/1/reviews/"+itoa(id)+"/dismissals", fx.ownerToken,
		`{"message":"Stale after a rebase.","event":"DISMISS"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"state":"DISMISSED"`) {
		t.Errorf("review not dismissed: %s", body)
	}
}

// jsonInt decodes a JSON object body and returns an integer field, the path a
// test takes to thread a freshly created id into the next request's url.
func jsonInt(t *testing.T, body []byte, key string) int64 {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("decode %s: %v\n%s", key, err, body)
	}
	num, ok := m[key].(json.Number)
	if !ok {
		t.Fatalf("field %q not a number: %v\n%s", key, m[key], body)
	}
	n, err := num.Int64()
	if err != nil {
		t.Fatalf("field %q not an int: %v\n%s", key, err, body)
	}
	return n
}

// itoa renders an int64 for a url path segment.
func itoa(n int64) string { return strconv.FormatInt(n, 10) }

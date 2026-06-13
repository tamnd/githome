package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

// pullFixture is a REST server backed by a bare repository with a feature branch
// one commit ahead of main, the git state a pull request rests on. It carries the
// owner's token and the pull request service so a test can run the mergeability
// recompute the worker would run, then read the resolved detail.
type pullFixture struct {
	srv   *httptest.Server
	token string
	pulls *domain.PRService
	st    *store.Store
	ctx   context.Context
}

func pullServer(t *testing.T) pullFixture {
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

	u := &store.UserRow{Login: "octocat", Type: "User"}
	if err := st.InsertUser(ctx, u); err != nil {
		t.Fatalf("insert user: %v", err)
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
		t.Fatalf("insert token: %v", err)
	}
	repo := &store.RepoRow{OwnerPK: u.PK, Name: "hello", DefaultBranch: "main"}
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
	root := mizu.NewRouter()
	Mount(root, Deps{
		Config:     cfg,
		Ready:      st,
		Auth:       authSvc,
		Users:      domain.NewUserService(st),
		Repos:      repoSvc,
		Issues:     issueSvc,
		Pulls:      pullSvc,
		URLs:       presenter.NewURLBuilder(cfg.URLs),
		NodeFormat: nodeid.FormatNew,
	})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	return pullFixture{srv: srv, token: g.Plaintext, pulls: pullSvc, st: st, ctx: ctx}
}

// bareFeatureRepo builds a bare repository at gitStore.Dir(pk) with a main branch
// and a feature branch one commit ahead, a clean merge. Commit times are pinned
// so the head and base shas are stable and the recorded goldens stay valid.
func bareFeatureRepo(t *testing.T, gitStore *git.Store, pk int64) {
	t.Helper()
	src := t.TempDir()
	gitExec(t, src, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, src, "add", "README.md")
	gitExec(t, src, "commit", "-q", "-m", "initial commit")
	gitExec(t, src, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(src, "feature.txt"), []byte("a feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, src, "add", "feature.txt")
	gitExec(t, src, "commit", "-q", "-m", "add a feature")
	gitExec(t, src, "checkout", "-q", "main")

	bare := gitStore.Dir(pk)
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	gitExec(t, "", "clone", "-q", "--bare", src, bare)
}

// openPull creates the canonical feature->main pull request and resolves its
// mergeability, the resolved state the detail and list goldens record.
func (fx pullFixture) openPull(t *testing.T) {
	t.Helper()
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls", fx.token,
		`{"title":"Add a feature","body":"It adds a feature.","head":"feature","base":"main"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed pull status %d, body %s", resp.StatusCode, body)
	}
	iss, err := fx.st.GetIssueByNumber(fx.ctx, 1, 1)
	if err != nil {
		t.Fatalf("GetIssueByNumber: %v", err)
	}
	if err := fx.pulls.RecomputeMergeability(fx.ctx, iss.PK); err != nil {
		t.Fatalf("RecomputeMergeability: %v", err)
	}
}

func TestCreatePullContract(t *testing.T) {
	fx := pullServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls", fx.token,
		`{"title":"Add a feature","body":"It adds a feature.","head":"feature","base":"main"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "pull_create.golden.json", body)
}

// TestPullMaintainerCanModify confirms the full view echoes the
// maintainer_can_modify flag the create request set, which gh pr edit reads.
func TestPullMaintainerCanModify(t *testing.T) {
	fx := pullServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls", fx.token,
		`{"title":"Add a feature","head":"feature","base":"main","maintainer_can_modify":true}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"maintainer_can_modify":true`) {
		t.Errorf("create response missing maintainer_can_modify:true:\n%s", body)
	}
	resp, body = get(t, fx.srv, "/repos/octocat/hello/pulls/1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"maintainer_can_modify":true`) {
		t.Errorf("full view missing maintainer_can_modify:true:\n%s", body)
	}
}

func TestCreatePullValidation(t *testing.T) {
	fx := pullServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/pulls", fx.token,
		`{"title":"No head","base":"main"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"field":"head"`) {
		t.Errorf("missing head field error: %s", body)
	}
}

func TestGetPullContract(t *testing.T) {
	fx := pullServer(t)
	fx.openPull(t)
	resp, body := get(t, fx.srv, "/repos/octocat/hello/pulls/1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "pull_get.golden.json", body)
}

func TestListPullsContract(t *testing.T) {
	fx := pullServer(t)
	fx.openPull(t)
	resp, body := get(t, fx.srv, "/repos/octocat/hello/pulls?state=open")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "pull_list.golden.json", body)
}

func TestPullFilesContract(t *testing.T) {
	fx := pullServer(t)
	fx.openPull(t)
	resp, body := get(t, fx.srv, "/repos/octocat/hello/pulls/1/files")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "pull_files.golden.json", body)
}

func TestPullDiffMediaType(t *testing.T) {
	fx := pullServer(t)
	fx.openPull(t)
	req, _ := http.NewRequest(http.MethodGet, fx.srv.URL+"/repos/octocat/hello/pulls/1", nil)
	req.Header.Set("Accept", "application/vnd.github.diff")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("diff GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "diff") {
		t.Errorf("content-type %q, want a diff media type", ct)
	}
	if mt := resp.Header.Get("X-GitHub-Media-Type"); mt != "github.v3; format=diff" {
		t.Errorf("X-GitHub-Media-Type %q, want github.v3; format=diff", mt)
	}
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	if got := string(buf[:n]); !strings.Contains(got, "diff --git") || !strings.Contains(got, "feature.txt") {
		t.Errorf("diff body unexpected:\n%s", got)
	}

	// The default JSON body keeps the json format marker.
	resp2, _ := get(t, fx.srv, "/repos/octocat/hello/pulls/1")
	if mt := resp2.Header.Get("X-GitHub-Media-Type"); mt != "github.v3; format=json" {
		t.Errorf("JSON X-GitHub-Media-Type %q, want github.v3; format=json", mt)
	}
}

func TestMergePullContract(t *testing.T) {
	fx := pullServer(t)
	fx.openPull(t)
	resp, body := authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/pulls/1/merge", fx.token,
		`{"merge_method":"merge"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"merged":true`) {
		t.Errorf("merge result not merged: %s", body)
	}
	// A second merge is refused: the pull request is already merged.
	resp, body = authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/pulls/1/merge", fx.token,
		`{"merge_method":"merge"}`)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("second merge status %d, want 405, body %s", resp.StatusCode, body)
	}
}

// TestPullMergeCheck covers GET /pulls/{number}/merge: 404 with the standard
// envelope while the pull request is open, 204 with an empty body once merged.
func TestPullMergeCheck(t *testing.T) {
	fx := pullServer(t)
	fx.openPull(t)

	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/pulls/1/merge", "token "+fx.token)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unmerged check status %d, want 404, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"message":"Not Found"`) {
		t.Errorf("unmerged check body missing envelope: %s", body)
	}

	resp, body = authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/pulls/1/merge", fx.token,
		`{"merge_method":"merge"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("merge status %d, body %s", resp.StatusCode, body)
	}

	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/pulls/1/merge", "token "+fx.token)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("merged check status %d, want 204, body %s", resp.StatusCode, body)
	}
	if len(body) != 0 {
		t.Errorf("merged check carries a body: %s", body)
	}

	// An unknown number is the same 404 as an unmerged pull request.
	resp, _ = authedGet(t, fx.srv, "/repos/octocat/hello/pulls/99/merge", "token "+fx.token)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown pull check status %d, want 404", resp.StatusCode)
	}
}

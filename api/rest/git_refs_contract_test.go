package rest

import (
	"bytes"
	"context"
	"io"
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
	"github.com/tamnd/githome/jsondiff"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// writeFixture is a server backed by a bare repository, the shape the git write
// path operates on. It carries the owner's token and the two commit shas the
// ref-write tests point at.
type writeFixture struct {
	srv      *httptest.Server
	token    string
	headSHA  string
	firstSHA string
}

// writeServer builds a full REST server over a store seeded with owner octocat
// and repo hello, plus a bare git repository with two commits on master. It
// skips when git is unavailable, since the write path shells out to git.
func writeServer(t *testing.T) writeFixture {
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
	pushed := fixedWhen
	repo := &store.RepoRow{OwnerPK: u.PK, Name: "hello", DefaultBranch: "master", PushedAt: &pushed}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("insert repo: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	first, head := bareTwoCommits(t, gitStore, repo.PK)

	authSvc := auth.NewService(st, "https://git.test.internal")
	t.Cleanup(authSvc.Close)
	cfg := authConfig(t)
	root := mizu.NewRouter()
	Mount(root, Deps{
		Config:     cfg,
		Ready:      st,
		Auth:       authSvc,
		Users:      domain.NewUserService(st),
		Repos:      domain.NewRepoService(st, gitStore),
		URLs:       presenter.NewURLBuilder(cfg.URLs),
		NodeFormat: nodeid.FormatNew,
	})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	return writeFixture{srv: srv, token: g.Plaintext, headSHA: head, firstSHA: first}
}

// bareTwoCommits builds a bare repository at gitStore.Dir(pk) with two commits on
// master and returns the (first, head) shas. Commit times are pinned so the shas
// are stable across runs and the recorded goldens stay valid.
func bareTwoCommits(t *testing.T, gitStore *git.Store, pk int64) (first, head string) {
	t.Helper()
	src := t.TempDir()
	gitExec(t, src, "init", "-q", "-b", "master")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, src, "add", "README.md")
	gitExec(t, src, "commit", "-q", "-m", "initial commit")
	first = gitExec(t, src, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(src, "next.txt"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, src, "add", "next.txt")
	gitExec(t, src, "commit", "-q", "-m", "second commit")
	head = gitExec(t, src, "rev-parse", "HEAD")

	bare := gitStore.Dir(pk)
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	gitExec(t, "", "clone", "-q", "--bare", src, bare)
	return first, head
}

func gitExec(t *testing.T, dir string, args ...string) string {
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

// authedSend issues method with a JSON body and bearer token, returning the
// response and its body.
func authedSend(t testing.TB, srv *httptest.Server, method, path, token, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

// assertGolden compares body against testdata/<name>, recording it under RECORD=1.
func assertWriteGolden(t *testing.T, name string, body []byte) {
	t.Helper()
	file := filepath.Join("testdata", name)
	if os.Getenv("RECORD") == "1" {
		norm := strings.ReplaceAll(string(body), "git.test.internal", "HOST")
		if err := os.WriteFile(file, append([]byte(norm), '\n'), 0o644); err != nil {
			t.Fatalf("record %s: %v", file, err)
		}
		return
	}
	jsondiff.AssertCompatible(t, golden(t, name), body, jsondiff.Default("git.test.internal"))
}

func TestCreateRefContract(t *testing.T) {
	fx := writeServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/git/refs", fx.token,
		`{"ref":"refs/heads/featureA","sha":"`+fx.firstSHA+`"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "git_ref_create.golden.json", body)
}

func TestUpdateRefContract(t *testing.T) {
	fx := writeServer(t)
	// Seed the branch at the first commit, then fast-forward it to head.
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/git/refs", fx.token,
		`{"ref":"refs/heads/featureA","sha":"`+fx.firstSHA+`"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed create status %d, body %s", resp.StatusCode, body)
	}
	resp, body := authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello/git/refs/heads/featureA", fx.token,
		`{"sha":"`+fx.headSHA+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "git_ref_update.golden.json", body)
}

func TestMatchingRefsContract(t *testing.T) {
	fx := writeServer(t)
	// Seed two branches sharing the feature prefix alongside master.
	for _, ref := range []string{"refs/heads/feature-a", "refs/heads/feature-b"} {
		if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/git/refs", fx.token,
			`{"ref":"`+ref+`","sha":"`+fx.firstSHA+`"}`); resp.StatusCode != http.StatusCreated {
			t.Fatalf("seed %s status %d, body %s", ref, resp.StatusCode, body)
		}
	}
	resp, body := authedSend(t, fx.srv, http.MethodGet, "/repos/octocat/hello/git/matching-refs/heads/feature", fx.token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	if got := strings.Count(string(body), `"ref":"refs/heads/feature`); got != 2 {
		t.Errorf("matched %d feature refs, want 2: %s", got, body)
	}
	if strings.Contains(string(body), "master") {
		t.Errorf("prefix match leaked master: %s", body)
	}
	// No match is an empty array, never a 404.
	resp, body = authedSend(t, fx.srv, http.MethodGet, "/repos/octocat/hello/git/matching-refs/heads/nope", fx.token, "")
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("no-match status %d body %q, want 200 []", resp.StatusCode, body)
	}
}

func TestRefWriteErrors(t *testing.T) {
	fx := writeServer(t)

	// Anonymous (no token) cannot reach a write: the public repo is visible, so
	// the create is authorized and refused with 403.
	if resp, _ := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/git/refs", "",
		`{"ref":"refs/heads/x","sha":"`+fx.firstSHA+`"}`); resp.StatusCode != http.StatusForbidden {
		t.Errorf("anon create status %d, want 403", resp.StatusCode)
	}

	// A malformed ref name is 422.
	if resp, _ := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/git/refs", fx.token,
		`{"ref":"heads/x","sha":"`+fx.firstSHA+`"}`); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("bad-ref create status %d, want 422", resp.StatusCode)
	}

	// A target object that is not in the repository is 422.
	if resp, _ := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/git/refs", fx.token,
		`{"ref":"refs/heads/y","sha":"0123456789012345678901234567890123456789"}`); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("missing-object create status %d, want 422", resp.StatusCode)
	}

	// Creating a ref that already exists is 422.
	if resp, _ := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/git/refs", fx.token,
		`{"ref":"refs/heads/master","sha":"`+fx.firstSHA+`"}`); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("duplicate create status %d, want 422", resp.StatusCode)
	}

	// A missing sha field is 422 before any git work.
	if resp, _ := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/git/refs", fx.token,
		`{"ref":"refs/heads/z"}`); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("missing-sha create status %d, want 422", resp.StatusCode)
	}

	// A non-fast-forward update without force is 422.
	if resp, _ := authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello/git/refs/heads/master", fx.token,
		`{"sha":"`+fx.firstSHA+`"}`); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("non-ff update status %d, want 422", resp.StatusCode)
	}
	// ...but accepted with force.
	if resp, body := authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello/git/refs/heads/master", fx.token,
		`{"sha":"`+fx.firstSHA+`","force":true}`); resp.StatusCode != http.StatusOK {
		t.Errorf("forced update status %d, want 200, body %s", resp.StatusCode, body)
	}
}

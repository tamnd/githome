package gittransport_test

import (
	"bytes"
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
	"github.com/tamnd/githome/gittransport"
	"github.com/tamnd/githome/store"
)

// runGit runs git in dir with a clean, deterministic environment and fails the
// test on a nonzero exit, returning trimmed stdout.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
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

// servedRepo builds a bare repository with one commit at gitStore.Dir(pk) and
// returns the head sha. It first builds a source worktree, then clones it bare
// into the served path, which is the same shape a pushed repository would have.
func servedRepo(t *testing.T, gitStore *git.Store, pk int64) string {
	t.Helper()
	src := t.TempDir()
	runGit(t, src, "init", "-q", "-b", "master")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "add", "README.md")
	runGit(t, src, "commit", "-q", "-m", "initial commit")
	head := runGit(t, src, "rev-parse", "HEAD")

	bare := gitStore.Dir(pk)
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, "", "clone", "-q", "--bare", src, bare)
	return head
}

func TestCloneRoundTrip(t *testing.T) {
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
	repo := &store.RepoRow{OwnerPK: u.PK, Name: "hello", DefaultBranch: "master"}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("insert repo: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	wantHead := servedRepo(t, gitStore, repo.PK)

	root := mizu.NewRouter()
	gittransport.Mount(root, &gittransport.Service{
		Repos: domain.NewRepoService(st, gitStore),
		Git:   gitStore,
	})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	// Clone the served repository over Smart HTTP and verify the object graph.
	dst := filepath.Join(t.TempDir(), "clone")
	runGit(t, "", "clone", "-q", srv.URL+"/octocat/hello.git", dst)
	runGit(t, dst, "fsck", "--full", "--strict")
	if got := runGit(t, dst, "rev-parse", "HEAD"); got != wantHead {
		t.Fatalf("cloned HEAD = %s, want %s", got, wantHead)
	}
	if got := runGit(t, dst, "log", "-1", "--format=%s"); got != "initial commit" {
		t.Fatalf("subject = %q, want %q", got, "initial commit")
	}
}

// TestInfoRefsAnonymousInvisibleRepoGets401 covers the read-side auth
// challenge: an anonymous probe of a repository it cannot see, whether private
// or missing, gets the same 401 with a Basic challenge so git retries with
// credentials and a private repo's existence never leaks.
func TestInfoRefsAnonymousInvisibleRepoGets401(t *testing.T) {
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
	repo := &store.RepoRow{OwnerPK: owner.PK, Name: "secret", Private: true, DefaultBranch: "master"}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("insert repo: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	root := mizu.NewRouter()
	gittransport.Mount(root, &gittransport.Service{
		Repos: domain.NewRepoService(st, gitStore),
		Git:   gitStore,
	})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	// A private and a nonexistent repository answer identically.
	for _, path := range []string{
		"/octocat/secret.git/info/refs?service=git-upload-pack",
		"/octocat/nope.git/info/refs?service=git-upload-pack",
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", path, resp.StatusCode)
		}
		if got := resp.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Basic ") {
			t.Errorf("%s: WWW-Authenticate = %q, want a Basic challenge", path, got)
		}
	}
}

// TestInfoRefsAuthedMissingRepoIs404 covers the post-auth case: once a real
// credential resolved, a repository the actor still cannot see is a plain 404
// with no further challenge.
func TestInfoRefsAuthedMissingRepoIs404(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "githome.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	u := &store.UserRow{Login: "hubber", Type: "User"}
	if err := st.InsertUser(ctx, u); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	other := &store.UserRow{Login: "octocat", Type: "User"}
	if err := st.InsertUser(ctx, other); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	repo := &store.RepoRow{OwnerPK: other.PK, Name: "secret", Private: true, DefaultBranch: "master"}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("insert repo: %v", err)
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

	authSvc := auth.NewService(st, "https://git.test.internal")
	t.Cleanup(authSvc.Close)

	gitStore := git.NewStore(t.TempDir())
	root := mizu.NewRouter()
	gittransport.Mount(root, &gittransport.Service{
		Repos: domain.NewRepoService(st, gitStore),
		Git:   gitStore,
		Auth:  authSvc,
	})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	// hubber authenticates fine but cannot see octocat's private repo, and a
	// missing repo answers the same way: 404, no challenge.
	for _, path := range []string{
		"/octocat/secret.git/info/refs?service=git-upload-pack",
		"/octocat/nope.git/info/refs?service=git-upload-pack",
	} {
		req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth("hubber", g.Plaintext)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, resp.StatusCode)
		}
	}
}

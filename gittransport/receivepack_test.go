package gittransport_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/gittransport"
	"github.com/tamnd/githome/store"
)

// asActor wraps the router so every request carries the given authenticated
// actor, standing in for the auth middleware the real server mounts ahead of the
// transport. mizu exposes no context setter, so it updates the request in place.
func asActor(a *auth.Actor) mizu.Middleware {
	return func(next mizu.Handler) mizu.Handler {
		return func(c *mizu.Ctx) error {
			r := c.Request()
			*r = *r.WithContext(auth.WithActor(r.Context(), a))
			return next(c)
		}
	}
}

func TestPushRoundTrip(t *testing.T) {
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
		t.Fatalf("insert user: %v", err)
	}
	repo := &store.RepoRow{OwnerPK: owner.PK, Name: "hello", DefaultBranch: "master"}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("insert repo: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	servedRepo(t, gitStore, repo.PK)

	root := mizu.NewRouter()
	root.Use(asActor(&auth.Actor{Kind: auth.KindUser, UserID: owner.PK, UserLogin: "octocat"}))
	gittransport.Mount(root, &gittransport.Service{
		Repos: domain.NewRepoService(st, gitStore),
		Git:   gitStore,
	})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	// Clone, commit, push back over Smart HTTP.
	dst := filepath.Join(t.TempDir(), "clone")
	runGit(t, "", "clone", "-q", srv.URL+"/octocat/hello.git", dst)
	if err := os.WriteFile(filepath.Join(dst, "t.txt"), []byte("push test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dst, "add", "t.txt")
	runGit(t, dst, "commit", "-q", "-m", "push test")
	wantHead := runGit(t, dst, "rev-parse", "HEAD")
	runGit(t, dst, "push", "-q", "origin", "master")

	// The server's bare repository now points at the pushed commit.
	snap, err := gitStore.RefSnapshot(ctx, repo.PK)
	if err != nil {
		t.Fatalf("RefSnapshot: %v", err)
	}
	if snap["refs/heads/master"] != wantHead {
		t.Fatalf("server master = %q, want %q", snap["refs/heads/master"], wantHead)
	}

	// The post-receive sink touched pushed_at and enqueued a push event plus a
	// default-branch search reindex.
	got, err := st.RepoByPK(ctx, repo.PK)
	if err != nil {
		t.Fatalf("RepoByPK: %v", err)
	}
	if got.PushedAt == nil {
		t.Error("pushed_at not set after push")
	}
	jobs, err := st.ListJobs(ctx)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	var pushEvents, reindexes int
	for _, j := range jobs {
		switch j.Kind {
		case domain.JobPushEvent:
			pushEvents++
		case domain.JobReindexSearch:
			reindexes++
		}
	}
	if pushEvents != 1 {
		t.Errorf("push_event jobs = %d, want 1", pushEvents)
	}
	if reindexes != 1 {
		t.Errorf("reindex_search jobs = %d, want 1", reindexes)
	}
}

func TestPushRequiresAuth(t *testing.T) {
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
		t.Fatalf("insert user: %v", err)
	}
	repo := &store.RepoRow{OwnerPK: owner.PK, Name: "hello", DefaultBranch: "master"}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("insert repo: %v", err)
	}
	gitStore := git.NewStore(t.TempDir())
	servedRepo(t, gitStore, repo.PK)

	// No auth middleware: the actor is anonymous.
	root := mizu.NewRouter()
	gittransport.Mount(root, &gittransport.Service{
		Repos: domain.NewRepoService(st, gitStore),
		Git:   gitStore,
	})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	// The receive-pack advertisement is refused with 401 so a push never starts.
	resp, err := http.Get(srv.URL + "/octocat/hello.git/info/refs?service=git-receive-pack")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 401 {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
	if ch := resp.Header.Get("WWW-Authenticate"); ch == "" {
		t.Error("missing WWW-Authenticate challenge on 401")
	}
}

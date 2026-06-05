package gittransport_test

import (
	"context"
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

// featureRepo builds a bare repository at gitStore.Dir(pk) with main and a
// feature branch one commit ahead, the git state a pull request rests on, and
// returns the feature tip sha. The commit times are pinned so the sha is stable.
func featureRepo(t *testing.T, gitStore *git.Store, pk int64) string {
	t.Helper()
	src := t.TempDir()
	runGit(t, src, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "add", "README.md")
	runGit(t, src, "commit", "-q", "-m", "initial commit")
	runGit(t, src, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(src, "feature.txt"), []byte("a feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "add", "feature.txt")
	runGit(t, src, "commit", "-q", "-m", "add a feature")
	featureTip := runGit(t, src, "rev-parse", "HEAD")
	runGit(t, src, "checkout", "-q", "main")

	bare := gitStore.Dir(pk)
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, "", "clone", "-q", "--bare", src, bare)
	return featureTip
}

// pullTransport seeds a store with octocat, the hello repo, and a bare repository
// with a feature branch, opens one pull request so refs/pull/1/head is published,
// and returns the running transport server, the git store, the pull service, and
// the owner. It is the fixture both the fetch conformance and the head-sync push
// tests build on.
func pullTransport(t *testing.T) (*httptest.Server, *git.Store, *store.Store, *domain.PRService, *store.UserRow) {
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
	repo := &store.RepoRow{OwnerPK: owner.PK, Name: "hello", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("insert repo: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	featureRepo(t, gitStore, repo.PK)

	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	pullSvc := domain.NewPRService(st, repoSvc, issueSvc, gitStore)
	if _, err := pullSvc.CreatePR(ctx, owner.PK, "octocat", "hello", domain.PRInput{
		Title: "Add a feature", Base: "main", Head: "feature",
	}); err != nil {
		t.Fatalf("CreatePR: %v", err)
	}

	root := mizu.NewRouter()
	root.Use(asActor(&auth.Actor{Kind: auth.KindUser, UserID: owner.PK, UserLogin: "octocat"}))
	gittransport.Mount(root, &gittransport.Service{
		Repos: repoSvc,
		Git:   gitStore,
		Pulls: pullSvc,
	})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)
	return srv, gitStore, st, pullSvc, owner
}

// TestFetchPullHeadRef is the git conformance the spec's M5 test artifacts call
// for: a raw git fetch of the synthetic refs/pull/*/head refspec reaches the ref
// the pull open published, and the fetched object hash matches the head tip. This
// is the transport behind gh pr checkout.
func TestFetchPullHeadRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	ctx := context.Background()
	srv, gitStore, _, _, _ := pullTransport(t)

	// The open published refs/pull/1/head at the feature tip.
	wantHead, err := gitStore.RefSHA(ctx, 1, "refs/pull/1/head")
	if err != nil {
		t.Fatalf("refs/pull/1/head not published: %v", err)
	}

	// Clone main, then fetch the synthetic ref by its public refspec, exactly the
	// fetch gh pr checkout runs.
	dst := filepath.Join(t.TempDir(), "clone")
	runGit(t, "", "clone", "-q", srv.URL+"/octocat/hello.git", dst)
	runGit(t, dst, "fetch", "-q", "origin", "refs/pull/1/head:refs/remotes/origin/pull/1/head")
	got := runGit(t, dst, "rev-parse", "refs/remotes/origin/pull/1/head")
	if got != wantHead {
		t.Fatalf("fetched pull head = %s, want %s", got, wantHead)
	}
	// The fetched object is present and the graph is intact.
	runGit(t, dst, "cat-file", "-e", got)
	runGit(t, dst, "fsck", "--full", "--strict")
}

// TestPushAdvancesPullHead confirms a push to a pull request's head branch syncs
// the synthetic ref: receive-pack runs syncPulls, which advances refs/pull/1/head
// to the new tip and re-enqueues the mergeability recompute.
func TestPushAdvancesPullHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	ctx := context.Background()
	srv, gitStore, st, _, _ := pullTransport(t)

	// Clone, add a commit on feature, push the branch back.
	dst := filepath.Join(t.TempDir(), "clone")
	runGit(t, "", "clone", "-q", srv.URL+"/octocat/hello.git", dst)
	runGit(t, dst, "fetch", "-q", "origin", "feature:feature")
	runGit(t, dst, "checkout", "-q", "feature")
	if err := os.WriteFile(filepath.Join(dst, "more.txt"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dst, "add", "more.txt")
	runGit(t, dst, "commit", "-q", "-m", "more feature")
	newTip := runGit(t, dst, "rev-parse", "HEAD")
	runGit(t, dst, "push", "-q", "origin", "feature")

	// The post-receive pull sync advanced the synthetic head ref to the new tip.
	gotHead, err := gitStore.RefSHA(ctx, 1, "refs/pull/1/head")
	if err != nil {
		t.Fatalf("refs/pull/1/head after push: %v", err)
	}
	if gotHead != newTip {
		t.Fatalf("refs/pull/1/head = %s, want pushed tip %s", gotHead, newTip)
	}

	// The head move re-enqueued a mergeability recompute so the field reverts to
	// null until the worker reruns.
	jobs, err := st.ListJobs(ctx)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	var recomputes int
	for _, j := range jobs {
		if j.Kind == domain.JobRecomputeMergeability {
			recomputes++
		}
	}
	if recomputes == 0 {
		t.Fatalf("push to pull head did not enqueue a recompute_mergeability job: %+v", jobs)
	}
}

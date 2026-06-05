package domain

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
)

// prBareRepo builds a bare repository at gs.Dir(pk) with a main branch and a
// feature branch one commit ahead of it, a clean merge into main. It skips when
// git is unavailable.
func prBareRepo(t *testing.T, gs *git.Store, pk int64) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	src := t.TempDir()
	gitCmd(t, src, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(src, "a.txt"), "one\n")
	gitCmd(t, src, "add", "-A")
	gitCmd(t, src, "commit", "-q", "-m", "first")
	gitCmd(t, src, "checkout", "-q", "-b", "feature")
	writeFile(t, filepath.Join(src, "b.txt"), "two\n")
	gitCmd(t, src, "add", "-A")
	gitCmd(t, src, "commit", "-q", "-m", "add b")
	gitCmd(t, src, "checkout", "-q", "main")

	bare := gs.Dir(pk)
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, "", "clone", "-q", "--bare", src, bare)
}

type prFixture struct {
	svc     *PRService
	st      *store.Store
	gs      *git.Store
	repo    *store.RepoRow
	ownerPK int64
	ctx     context.Context
}

func newPRFixture(t *testing.T) *prFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "githome.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	owner := &store.UserRow{Login: "octocat", Type: "User"}
	if err := st.InsertUser(ctx, owner); err != nil {
		t.Fatalf("InsertUser: %v", err)
	}
	repo := &store.RepoRow{OwnerPK: owner.PK, Name: "hello", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("InsertRepo: %v", err)
	}
	gs := git.NewStore(t.TempDir())
	prBareRepo(t, gs, repo.PK)
	repos := NewRepoService(st, gs)
	issues := NewIssueService(st, repos)
	return &prFixture{
		svc:     NewPRService(st, repos, issues, gs),
		st:      st,
		gs:      gs,
		repo:    repo,
		ownerPK: owner.PK,
		ctx:     ctx,
	}
}

// issuePK resolves the internal issue pk for a pull request number, the key the
// mergeability recompute addresses a pull request by.
func (f *prFixture) issuePK(t *testing.T, number int64) int64 {
	t.Helper()
	iss, err := f.st.GetIssueByNumber(f.ctx, f.repo.PK, number)
	if err != nil {
		t.Fatalf("GetIssueByNumber(%d): %v", number, err)
	}
	return iss.PK
}

func TestCreatePROpensAndPublishesHead(t *testing.T) {
	f := newPRFixture(t)
	body := "please review"
	pr, err := f.svc.CreatePR(f.ctx, f.ownerPK, "octocat", "hello", PRInput{
		Title: "add b", Body: &body, Base: "main", Head: "feature",
	})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if pr.Number != 1 || pr.State != "open" || pr.User.Login != "octocat" {
		t.Fatalf("pr = %+v", pr)
	}
	if pr.Base.Ref != "main" || pr.Head.Ref != "feature" {
		t.Errorf("refs = base %q head %q", pr.Base.Ref, pr.Head.Ref)
	}
	// A fresh pull request has no computed merge state yet.
	if pr.Mergeable != nil || pr.MergeableState != "unknown" {
		t.Errorf("fresh merge state = %v / %q, want nil / unknown", pr.Mergeable, pr.MergeableState)
	}

	// The synthetic refs/pull/1/head points at the head tip a client can fetch.
	headSHA, err := f.gs.RefSHA(f.ctx, f.repo.PK, "refs/pull/1/head")
	if err != nil {
		t.Fatalf("refs/pull/1/head not published: %v", err)
	}
	if headSHA != pr.Head.SHA {
		t.Errorf("refs/pull/1/head = %s, want head %s", headSHA, pr.Head.SHA)
	}

	// The open enqueues a mergeability recompute so mergeable leaves null.
	jobs, err := f.st.ListJobs(f.ctx)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	var found bool
	for _, j := range jobs {
		if j.Kind == JobRecomputeMergeability {
			found = true
		}
	}
	if !found {
		t.Fatalf("create did not enqueue a recompute_mergeability job: %+v", jobs)
	}
}

func TestCreatePRValidationAndAuthorization(t *testing.T) {
	f := newPRFixture(t)
	cases := []struct {
		name string
		in   PRInput
	}{
		{"empty title", PRInput{Title: " ", Base: "main", Head: "feature"}},
		{"missing head", PRInput{Title: "x", Base: "main", Head: ""}},
		{"same branch", PRInput{Title: "x", Base: "main", Head: "main"}},
		{"unknown base", PRInput{Title: "x", Base: "ghost", Head: "feature"}},
		{"unknown head", PRInput{Title: "x", Base: "main", Head: "ghost"}},
	}
	for _, c := range cases {
		if _, err := f.svc.CreatePR(f.ctx, f.ownerPK, "octocat", "hello", c.in); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", c.name, err)
		}
	}
	// A non-owner who can see the public repo cannot open a pull request.
	if _, err := f.svc.CreatePR(f.ctx, 999, "octocat", "hello", PRInput{Title: "x", Base: "main", Head: "feature"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-owner create err = %v, want ErrForbidden", err)
	}
}

func TestRecomputeMergeabilityResolvesCleanMerge(t *testing.T) {
	f := newPRFixture(t)
	pr, err := f.svc.CreatePR(f.ctx, f.ownerPK, "octocat", "hello", PRInput{
		Title: "add b", Base: "main", Head: "feature",
	})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}

	// Running the recompute the worker would run resolves the null state to a
	// clean, mergeable result and publishes refs/pull/1/merge.
	if err := f.svc.RecomputeMergeability(f.ctx, f.issuePK(t, pr.Number)); err != nil {
		t.Fatalf("RecomputeMergeability: %v", err)
	}
	got, err := f.svc.GetPR(f.ctx, f.ownerPK, "octocat", "hello", pr.Number)
	if err != nil {
		t.Fatalf("GetPR: %v", err)
	}
	if got.Mergeable == nil || !*got.Mergeable {
		t.Fatalf("mergeable = %v, want true", got.Mergeable)
	}
	if got.MergeableState != "clean" {
		t.Errorf("mergeable_state = %q, want clean", got.MergeableState)
	}
	if got.ChangedFiles == 0 || got.CommitsCount == 0 {
		t.Errorf("diff stats not filled: %+v", got)
	}
	if _, err := f.gs.RefSHA(f.ctx, f.repo.PK, "refs/pull/1/merge"); err != nil {
		t.Errorf("clean recompute did not publish refs/pull/1/merge: %v", err)
	}
}

func TestMergeLandsAndClosesPull(t *testing.T) {
	f := newPRFixture(t)
	pr, err := f.svc.CreatePR(f.ctx, f.ownerPK, "octocat", "hello", PRInput{
		Title: "add b", Base: "main", Head: "feature",
	})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}

	// A stale expected head is refused before any merge happens.
	if _, err := f.svc.Merge(f.ctx, f.ownerPK, "octocat", "hello", pr.Number, MergeInput{
		Method: git.MergeCommit, ExpectedHead: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
	}); !errors.Is(err, ErrHeadMismatch) {
		t.Fatalf("stale head merge err = %v, want ErrHeadMismatch", err)
	}

	res, err := f.svc.Merge(f.ctx, f.ownerPK, "octocat", "hello", pr.Number, MergeInput{Method: git.MergeCommit})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !res.Merged || res.SHA == "" {
		t.Fatalf("merge result = %+v", res)
	}

	// The base branch now points at the merge commit.
	baseTip, err := f.gs.RefSHA(f.ctx, f.repo.PK, "refs/heads/main")
	if err != nil || baseTip != res.SHA {
		t.Fatalf("main = %s (%v), want merge commit %s", baseTip, err, res.SHA)
	}

	// A re-read reports the pull request merged and its issue closed.
	got, err := f.svc.GetPR(f.ctx, f.ownerPK, "octocat", "hello", pr.Number)
	if err != nil {
		t.Fatalf("GetPR after merge: %v", err)
	}
	if !got.Merged || got.State != "closed" || got.MergedBy == nil {
		t.Fatalf("post-merge pr = %+v", got)
	}

	// Merging again is refused: the pull request is already merged.
	if _, err := f.svc.Merge(f.ctx, f.ownerPK, "octocat", "hello", pr.Number, MergeInput{Method: git.MergeCommit}); !errors.Is(err, ErrNotMergeable) {
		t.Errorf("re-merge err = %v, want ErrNotMergeable", err)
	}
}

func TestGetPRNotFound(t *testing.T) {
	f := newPRFixture(t)
	if _, err := f.svc.GetPR(f.ctx, f.ownerPK, "octocat", "hello", 404); !errors.Is(err, ErrPullNotFound) {
		t.Errorf("missing pr err = %v, want ErrPullNotFound", err)
	}
}

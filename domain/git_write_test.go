package domain

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
)

func TestOnPush(t *testing.T) {
	ctx := context.Background()
	st := &fakeRepoStore{
		repos: map[string]*store.RepoRow{
			"octocat/hello": {PK: 5, DBID: 50, OwnerPK: 10, Name: "hello", DefaultBranch: "main"},
		},
	}
	svc := NewRepoService(st, git.NewStore(t.TempDir()))
	at := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)

	// A push that advances the default branch touches pushed_at and enqueues both
	// the push event and a default-branch search reindex.
	batch := PushBatch{
		RepoPK:     5,
		PusherPK:   10,
		Protocol:   "http",
		ReceivedAt: at,
		Updates: []RefUpdate{
			{Ref: "refs/heads/main", OldSHA: ZeroSHA, NewSHA: "1111111111111111111111111111111111111111"},
		},
	}
	if err := svc.OnPush(ctx, batch); err != nil {
		t.Fatalf("OnPush: %v", err)
	}
	if got := st.pushedAt[5]; !got.Equal(at) {
		t.Errorf("pushed_at = %v, want %v", got, at)
	}
	if len(st.jobs) != 2 {
		t.Fatalf("jobs = %d, want 2 (%+v)", len(st.jobs), st.jobs)
	}
	if st.jobs[0].Kind != JobDeliverEvent {
		t.Errorf("job[0].Kind = %q, want %q", st.jobs[0].Kind, JobDeliverEvent)
	}
	if st.jobs[1].Kind != JobReindexSearch || st.jobs[1].DedupeKey != "reindex:repo:5" {
		t.Errorf("job[1] = %+v, want reindex with dedupe reindex:repo:5", st.jobs[1])
	}
	if !strings.Contains(st.jobs[0].Payload, `"refs/heads/main"`) {
		t.Errorf("deliver_event payload missing ref: %s", st.jobs[0].Payload)
	}
	if len(st.events) != 1 || st.events[0].Event != EventPush {
		t.Errorf("events = %+v, want one push event", st.events)
	}

	// A push that touches only a side branch records the event but no reindex.
	st.jobs = nil
	side := PushBatch{
		RepoPK: 5, PusherPK: 10, Protocol: "http", ReceivedAt: at,
		Updates: []RefUpdate{{Ref: "refs/heads/feature", OldSHA: ZeroSHA, NewSHA: "2222222222222222222222222222222222222222"}},
	}
	if err := svc.OnPush(ctx, side); err != nil {
		t.Fatalf("OnPush side: %v", err)
	}
	if len(st.jobs) != 1 || st.jobs[0].Kind != JobDeliverEvent {
		t.Fatalf("side push jobs = %+v, want one deliver_event", st.jobs)
	}

	// An empty batch is a no-op.
	st.jobs = nil
	if err := svc.OnPush(ctx, PushBatch{RepoPK: 5}); err != nil {
		t.Fatalf("OnPush empty: %v", err)
	}
	if len(st.jobs) != 0 {
		t.Fatalf("empty batch enqueued %d jobs", len(st.jobs))
	}
}

// bareRepo builds a bare repository at gs.Dir(pk) with two commits on main and
// returns (first, head) shas. It skips when git is unavailable.
func bareRepo(t *testing.T, gs *git.Store, pk int64) (first, head string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	src := t.TempDir()
	gitCmd(t, src, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(src, "a.txt"), "one\n")
	gitCmd(t, src, "add", "-A")
	gitCmd(t, src, "commit", "-q", "-m", "first")
	first = gitCmd(t, src, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(src, "b.txt"), "two\n")
	gitCmd(t, src, "add", "-A")
	gitCmd(t, src, "commit", "-q", "-m", "second")
	head = gitCmd(t, src, "rev-parse", "HEAD")

	bare := gs.Dir(pk)
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, "", "clone", "-q", "--bare", src, bare)
	return first, head
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitCmd(t *testing.T, dir string, args ...string) string {
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

func TestRefServiceCreateAndUpdate(t *testing.T) {
	ctx := context.Background()
	gs := git.NewStore(t.TempDir())
	first, head := bareRepo(t, gs, 5)

	const ownerPK = int64(10)
	st := &fakeRepoStore{
		repos: map[string]*store.RepoRow{
			"octocat/hello": {PK: 5, DBID: 50, OwnerPK: ownerPK, Name: "hello", DefaultBranch: "main"},
		},
		users: map[int64]*store.UserRow{ownerPK: {PK: ownerPK, DBID: 100, Login: "octocat", Type: "User"}},
	}
	svc := NewRepoService(st, gs)

	// The owner creates a branch at first.
	ref, err := svc.CreateRef(ctx, ownerPK, "octocat", "hello", "refs/heads/feature", first)
	if err != nil {
		t.Fatalf("CreateRef: %v", err)
	}
	if ref.Name != "refs/heads/feature" || ref.Target != first || ref.Type != git.ObjectCommit {
		t.Fatalf("CreateRef ref = %+v", ref)
	}

	// Re-creating it is a conflict.
	if _, err := svc.CreateRef(ctx, ownerPK, "octocat", "hello", "refs/heads/feature", first); !errors.Is(err, ErrRefExists) {
		t.Errorf("re-create err = %v, want ErrRefExists", err)
	}

	// A fast-forward update succeeds.
	if _, err := svc.UpdateRef(ctx, ownerPK, "octocat", "hello", "refs/heads/feature", head, false); err != nil {
		t.Fatalf("fast-forward UpdateRef: %v", err)
	}
	// A rewind without force is refused.
	if _, err := svc.UpdateRef(ctx, ownerPK, "octocat", "hello", "refs/heads/feature", first, false); !errors.Is(err, ErrNotFastForward) {
		t.Errorf("rewind err = %v, want ErrNotFastForward", err)
	}
	// ...and accepted with force.
	if _, err := svc.UpdateRef(ctx, ownerPK, "octocat", "hello", "refs/heads/feature", first, true); err != nil {
		t.Errorf("forced rewind: %v", err)
	}
}

func TestRefServiceAuthorization(t *testing.T) {
	ctx := context.Background()
	gs := git.NewStore(t.TempDir())
	first, _ := bareRepo(t, gs, 5)
	_, _ = bareRepo(t, gs, 6)

	const ownerPK = int64(10)
	st := &fakeRepoStore{
		repos: map[string]*store.RepoRow{
			"octocat/hello":  {PK: 5, DBID: 50, OwnerPK: ownerPK, Name: "hello", DefaultBranch: "main"},
			"octocat/secret": {PK: 6, DBID: 60, OwnerPK: ownerPK, Name: "secret", Private: true, DefaultBranch: "main"},
		},
		users: map[int64]*store.UserRow{ownerPK: {PK: ownerPK, DBID: 100, Login: "octocat", Type: "User"}},
	}
	svc := NewRepoService(st, gs)

	// A non-owner who can see the public repo gets 403, not 404.
	if _, err := svc.CreateRef(ctx, 99, "octocat", "hello", "refs/heads/x", first); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-owner write err = %v, want ErrForbidden", err)
	}
	// Anonymous is likewise forbidden on the visible repo.
	if _, err := svc.CreateRef(ctx, 0, "octocat", "hello", "refs/heads/x", first); !errors.Is(err, ErrForbidden) {
		t.Errorf("anon write err = %v, want ErrForbidden", err)
	}
	// A private repo the actor cannot see stays 404 even on a write.
	if _, err := svc.CreateRef(ctx, 99, "octocat", "secret", "refs/heads/x", first); !errors.Is(err, ErrRepoNotFound) {
		t.Errorf("invisible write err = %v, want ErrRepoNotFound", err)
	}
}

func TestRefServiceValidation(t *testing.T) {
	ctx := context.Background()
	gs := git.NewStore(t.TempDir())
	_, head := bareRepo(t, gs, 5)

	const ownerPK = int64(10)
	st := &fakeRepoStore{
		repos: map[string]*store.RepoRow{
			"octocat/hello": {PK: 5, DBID: 50, OwnerPK: ownerPK, Name: "hello", DefaultBranch: "main"},
		},
		users: map[int64]*store.UserRow{ownerPK: {PK: ownerPK, DBID: 100, Login: "octocat", Type: "User"}},
	}
	svc := NewRepoService(st, gs)

	for _, bad := range []string{"feature", "refs/heads", "refs/heads/", "refs/heads/a..b", "refs/heads/a b", "refs/heads//x"} {
		if _, err := svc.CreateRef(ctx, ownerPK, "octocat", "hello", bad, head); !errors.Is(err, ErrInvalidRef) {
			t.Errorf("CreateRef(%q) err = %v, want ErrInvalidRef", bad, err)
		}
	}

	// A well-formed ref at a missing object is ErrObjectMissing.
	if _, err := svc.CreateRef(ctx, ownerPK, "octocat", "hello", "refs/heads/x", "0123456789012345678901234567890123456789"); !errors.Is(err, ErrObjectMissing) {
		t.Errorf("missing-object err = %v, want ErrObjectMissing", err)
	}
	// Updating a ref that does not exist is ErrRefNotFound.
	if _, err := svc.UpdateRef(ctx, ownerPK, "octocat", "hello", "refs/heads/ghost", head, false); !errors.Is(err, ErrRefNotFound) {
		t.Errorf("update-missing err = %v, want ErrRefNotFound", err)
	}
}

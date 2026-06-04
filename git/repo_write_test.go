package git_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/githome/git"
)

// writeRepo builds a bare repository at store.Dir(pk) with two commits on master
// and returns (first, head) shas. It clones a built worktree in bare so the
// served path is the same shape a pushed repository would have.
func writeRepo(t *testing.T, store *git.Store, pk int64) (first, head string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	src := t.TempDir()
	runGit(t, src, "init", "-q", "-b", "master")
	mustWrite(t, filepath.Join(src, "a.txt"), "one\n")
	runGit(t, src, "add", "-A")
	runGit(t, src, "commit", "-q", "-m", "first")
	first = runGit(t, src, "rev-parse", "HEAD")
	mustWrite(t, filepath.Join(src, "b.txt"), "two\n")
	runGit(t, src, "add", "-A")
	runGit(t, src, "commit", "-q", "-m", "second")
	head = runGit(t, src, "rev-parse", "HEAD")

	bare := store.Dir(pk)
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, "", "clone", "-q", "--bare", src, bare)
	return first, head
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
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

func TestRefWriteLifecycle(t *testing.T) {
	ctx := context.Background()
	store := git.NewStore(t.TempDir())
	first, head := writeRepo(t, store, 1)

	// Snapshot sees the one branch the clone created.
	snap, err := store.RefSnapshot(ctx, 1)
	if err != nil {
		t.Fatalf("RefSnapshot: %v", err)
	}
	if snap["refs/heads/master"] != head {
		t.Fatalf("snapshot master = %q, want %q", snap["refs/heads/master"], head)
	}

	// Create a new branch at first.
	if err := store.CreateRef(ctx, 1, "refs/heads/feature", first); err != nil {
		t.Fatalf("CreateRef: %v", err)
	}
	if sha, _ := store.RefSHA(ctx, 1, "refs/heads/feature"); sha != first {
		t.Fatalf("feature = %q, want %q", sha, first)
	}

	// Creating it again is ErrRefExists.
	if err := store.CreateRef(ctx, 1, "refs/heads/feature", first); err != git.ErrRefExists {
		t.Fatalf("re-create: got %v, want ErrRefExists", err)
	}

	// A fast-forward update (first -> head) succeeds.
	if err := store.UpdateRef(ctx, 1, "refs/heads/feature", head, false); err != nil {
		t.Fatalf("fast-forward UpdateRef: %v", err)
	}

	// A non-fast-forward update (head -> first, a rewind) is refused.
	if err := store.UpdateRef(ctx, 1, "refs/heads/feature", first, false); err != git.ErrNotFastForward {
		t.Fatalf("rewind: got %v, want ErrNotFastForward", err)
	}
	// ...unless forced.
	if err := store.UpdateRef(ctx, 1, "refs/heads/feature", first, true); err != nil {
		t.Fatalf("forced rewind: %v", err)
	}

	// Delete removes it.
	if err := store.DeleteRef(ctx, 1, "refs/heads/feature"); err != nil {
		t.Fatalf("DeleteRef: %v", err)
	}
	if _, err := store.RefSHA(ctx, 1, "refs/heads/feature"); err != git.ErrRefNotFound {
		t.Fatalf("after delete: got %v, want ErrRefNotFound", err)
	}
}

func TestRefWriteValidation(t *testing.T) {
	ctx := context.Background()
	store := git.NewStore(t.TempDir())
	_, head := writeRepo(t, store, 2)

	if err := store.CreateRef(ctx, 2, "refs/heads/bad", zeros); err != git.ErrObjectNotFound {
		t.Fatalf("create at missing object: got %v, want ErrObjectNotFound", err)
	}
	if _, err := store.RefSHA(ctx, 2, "refs/heads/nope"); err != git.ErrRefNotFound {
		t.Fatalf("RefSHA missing: got %v, want ErrRefNotFound", err)
	}
	if err := store.UpdateRef(ctx, 2, "refs/heads/nope", head, false); err != git.ErrRefNotFound {
		t.Fatalf("update missing ref: got %v, want ErrRefNotFound", err)
	}

	typ, err := store.ObjectType(ctx, 2, head)
	if err != nil {
		t.Fatalf("ObjectType: %v", err)
	}
	if typ != "commit" {
		t.Fatalf("HEAD type = %q, want commit", typ)
	}
}

const zeros = "0123456789012345678901234567890123456789"

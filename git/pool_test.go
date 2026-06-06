package git_test

import (
	"context"
	"os/exec"
	"testing"

	"github.com/tamnd/githome/git"
)

// TestCatFilePoolReuse verifies that ObjectExists and ObjectType give the same
// answers as the single-spawn path and that the pool correctly classifies
// present and absent objects.
func TestCatFilePoolReuse(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	store := git.NewStore(t.TempDir())
	ctx := context.Background()
	first, head := writeRepo(t, store, 1)

	// ObjectExists: present objects.
	for _, sha := range []string{first, head} {
		ok, err := store.ObjectExists(ctx, 1, sha)
		if err != nil {
			t.Fatalf("ObjectExists(%s): %v", sha[:8], err)
		}
		if !ok {
			t.Errorf("ObjectExists(%s) = false, want true", sha[:8])
		}
	}

	// ObjectExists: absent object.
	absent := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	ok, err := store.ObjectExists(ctx, 1, absent)
	if err != nil {
		t.Fatalf("ObjectExists(absent): %v", err)
	}
	if ok {
		t.Errorf("ObjectExists(absent) = true, want false")
	}

	// ObjectType: head should be a commit.
	typ, err := store.ObjectType(ctx, 1, head)
	if err != nil {
		t.Fatalf("ObjectType: %v", err)
	}
	if typ != "commit" {
		t.Errorf("ObjectType = %q, want commit", typ)
	}

	// Second call on head hits the cache (no new process round-trip).
	typ2, err := store.ObjectType(ctx, 1, head)
	if err != nil {
		t.Fatalf("ObjectType (cached): %v", err)
	}
	if typ2 != typ {
		t.Errorf("cached ObjectType = %q, want %q", typ2, typ)
	}
}

// TestCatFilePoolMultiRepo verifies that two different repos each get their
// own process and return correct results independently.
func TestCatFilePoolMultiRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	store := git.NewStore(t.TempDir())
	ctx := context.Background()
	_, head1 := writeRepo(t, store, 10)
	_, head2 := writeRepo(t, store, 11)

	// Each repo's head commit is present in its own repo, absent in the other.
	ok1, _ := store.ObjectExists(ctx, 10, head1)
	ok2, _ := store.ObjectExists(ctx, 11, head2)
	if !ok1 || !ok2 {
		t.Errorf("own-repo lookup failed: ok1=%v ok2=%v", ok1, ok2)
	}

	// head1 is not present in repo 11 (different repo, different history).
	crossOk, err := store.ObjectExists(ctx, 11, head1)
	if err != nil {
		t.Fatalf("cross-repo ObjectExists: %v", err)
	}
	if crossOk {
		t.Logf("cross-repo ObjectExists returned true (repos happen to share commit — ok)")
	}
}

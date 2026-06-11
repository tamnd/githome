package git_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/tamnd/githome/git"
)

func TestBatchedBlame(t *testing.T) {
	store := git.NewStore(t.TempDir())
	first, second := refsRepo(t, store, 32)
	repo, err := store.Open(32)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer repo.Release()

	lines, err := repo.Blame("master", "a.txt")
	if err != nil {
		t.Fatalf("Blame: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("blame lines = %+v, want 2", lines)
	}
	// "one" landed in the first commit, "two" in the second.
	if lines[0].Text != "one" || lines[0].SHA != first || lines[0].LineNum != 1 {
		t.Errorf("line 1 = %+v, want \"one\" from %s", lines[0], first)
	}
	if lines[1].Text != "two" || lines[1].SHA != second || lines[1].LineNum != 2 {
		t.Errorf("line 2 = %+v, want \"two\" from %s", lines[1], second)
	}
	if lines[0].AuthorName != "Octo Cat" || lines[0].AuthorEmail != "octo@example.com" {
		t.Errorf("line 1 author = %+v", lines[0])
	}
	if lines[0].When.IsZero() {
		t.Error("author time did not parse")
	}

	// A missing path still maps to the not-found error, via the fallback.
	if _, err := repo.Blame("master", "no-such-file"); !errors.Is(err, git.ErrObjectNotFound) {
		t.Fatalf("Blame missing path error = %v, want ErrObjectNotFound", err)
	}

	// The same annotation again comes from the content-addressed cache.
	again, err := repo.Blame("master", "a.txt")
	if err != nil || len(again) != 2 {
		t.Fatalf("Blame again = %+v, %v", again, err)
	}
}

func TestBlameSizeCap(t *testing.T) {
	store := git.NewStore(t.TempDir())
	refsRepo(t, store, 33)
	store.SetMaxBlobBytes(4) // a.txt is "one\ntwo\n", 8 bytes
	repo, err := store.Open(33)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer repo.Release()
	if _, err := repo.Blame("master", "a.txt"); !errors.Is(err, git.ErrBlobTooLarge) {
		t.Fatalf("Blame over cap error = %v, want ErrBlobTooLarge", err)
	}
}

func TestBatchedCommitPatch(t *testing.T) {
	store := git.NewStore(t.TempDir())
	first, second := refsRepo(t, store, 34)
	repo, err := store.Open(34)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer repo.Release()

	patch, err := repo.CommitPatch(second)
	if err != nil {
		t.Fatalf("CommitPatch: %v", err)
	}
	if !strings.Contains(patch, "+two") || !strings.Contains(patch, "a.txt") {
		t.Fatalf("patch = %q, want a.txt hunk adding \"two\"", patch)
	}

	// The root commit has no parent and an empty patch, as before.
	root, err := repo.CommitPatch(first)
	if err != nil || root != "" {
		t.Fatalf("root patch = %q, %v; want empty", root, err)
	}

	// And again, from the cache.
	again, err := repo.CommitPatch(second)
	if err != nil || again != patch {
		t.Fatalf("cached patch differs: %q vs %q (%v)", again, patch, err)
	}
}

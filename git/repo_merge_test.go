package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamnd/githome/git"
)

// mergeRepo builds a bare repository whose master and feature branches diverge
// from a common first commit. feature adds files master never touches, so it
// merges clean; conflict is a second feature tip that edits the same line master
// did, so it does not. It returns the branch tips the merge tests resolve.
func mergeRepo(t *testing.T, store *git.Store, pk int64) (base, feature, conflict string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	src := t.TempDir()
	runGit(t, src, "init", "-q", "-b", "master")
	mustWrite(t, filepath.Join(src, "a.txt"), "one\n")
	runGit(t, src, "add", "-A")
	runGit(t, src, "commit", "-q", "-m", "first")

	// feature branches here, before master moves.
	runGit(t, src, "branch", "feature")
	runGit(t, src, "branch", "conflict")

	// master edits a.txt and adds m.txt.
	mustWrite(t, filepath.Join(src, "a.txt"), "one\nmaster\n")
	mustWrite(t, filepath.Join(src, "m.txt"), "from master\n")
	runGit(t, src, "add", "-A")
	runGit(t, src, "commit", "-q", "-m", "master work")
	base = runGit(t, src, "rev-parse", "master")

	// feature adds files master never touches: a clean merge.
	runGit(t, src, "switch", "-q", "feature")
	mustWrite(t, filepath.Join(src, "b.txt"), "two\n")
	mustWrite(t, filepath.Join(src, "c.txt"), "three\n")
	runGit(t, src, "add", "-A")
	runGit(t, src, "commit", "-q", "-m", "feature one")
	mustWrite(t, filepath.Join(src, "c.txt"), "three\nfour\n")
	runGit(t, src, "add", "-A")
	runGit(t, src, "commit", "-q", "-m", "feature two")
	feature = runGit(t, src, "rev-parse", "feature")

	// conflict edits the same a.txt line master did.
	runGit(t, src, "switch", "-q", "conflict")
	mustWrite(t, filepath.Join(src, "a.txt"), "one\nfeature\n")
	runGit(t, src, "add", "-A")
	runGit(t, src, "commit", "-q", "-m", "conflicting work")
	conflict = runGit(t, src, "rev-parse", "conflict")

	bare := store.Dir(pk)
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, "", "clone", "-q", "--bare", src, bare)
	return base, feature, conflict
}

func TestMergeSurfaceClean(t *testing.T) {
	ctx := context.Background()
	store := git.NewStore(t.TempDir())
	base, feature, _ := mergeRepo(t, store, 10)

	// MergeBase is the shared first commit, not either tip.
	mb, ok, err := store.MergeBase(ctx, 10, base, feature)
	if err != nil || !ok {
		t.Fatalf("MergeBase: ok=%v err=%v", ok, err)
	}
	if mb == base || mb == feature {
		t.Fatalf("merge base %q should predate both tips", mb)
	}

	// feature is two commits ahead, one behind (master's single commit).
	ahead, behind, err := store.AheadBehind(ctx, 10, base, feature)
	if err != nil {
		t.Fatalf("AheadBehind: %v", err)
	}
	if ahead != 2 || behind != 1 {
		t.Fatalf("ahead/behind = %d/%d, want 2/1", ahead, behind)
	}

	// CommitsBetween lists feature's own two commits, oldest first.
	commits, err := store.CommitsBetween(ctx, 10, base, feature)
	if err != nil {
		t.Fatalf("CommitsBetween: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("commits = %d, want 2", len(commits))
	}
	if commits[0].Message != "feature one" || commits[1].Message != "feature two" {
		t.Fatalf("commit order wrong: %q then %q", commits[0].Message, commits[1].Message)
	}

	// The three-dot diff sees b.txt added and c.txt added, never master's m.txt.
	files, err := store.ChangedFiles(ctx, 10, base, feature)
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = f.Status
	}
	if got["b.txt"] != "added" || got["c.txt"] != "added" {
		t.Fatalf("changed files = %v, want b.txt and c.txt added", got)
	}
	if _, ok := got["m.txt"]; ok {
		t.Fatalf("three-dot diff must not include master-only m.txt: %v", got)
	}

	add, del, changed, err := store.DiffStat(ctx, 10, base, feature)
	if err != nil {
		t.Fatalf("DiffStat: %v", err)
	}
	if changed != 2 || add != 3 || del != 0 {
		t.Fatalf("diffstat = +%d -%d across %d files, want +3 -0 across 2", add, del, changed)
	}

	// A clean test merge yields a tree and no ref moves.
	tree, clean, err := store.TestMerge(ctx, 10, base, feature)
	if err != nil || !clean || tree == "" {
		t.Fatalf("TestMerge clean: tree=%q clean=%v err=%v", tree, clean, err)
	}
}

func TestMergeStrategies(t *testing.T) {
	ctx := context.Background()
	store := git.NewStore(t.TempDir())
	base, feature, conflict := mergeRepo(t, store, 11)
	repo, err := store.Open(11)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	when, err := time.Parse(time.RFC3339, "2026-01-02T03:04:05Z")
	if err != nil {
		t.Fatal(err)
	}
	who := git.Signature{Name: "Octo Cat", Email: "octo@example.com", When: when}

	// merge: a two-parent commit whose parents are base and feature.
	mc, ok, err := store.Merge(ctx, 11, git.MergeCommit, base, feature, "Merge pull request #1", who, who)
	if err != nil || !ok {
		t.Fatalf("merge: ok=%v err=%v", ok, err)
	}
	c, err := repo.Commit(mc)
	if err != nil {
		t.Fatalf("read merge commit: %v", err)
	}
	if len(c.Parents) != 2 || c.Parents[0] != base || c.Parents[1] != feature {
		t.Fatalf("merge parents = %v, want [%s %s]", c.Parents, base, feature)
	}

	// squash: one commit on top of base alone.
	sq, ok, err := store.Merge(ctx, 11, git.MergeSquash, base, feature, "Squash #1", who, who)
	if err != nil || !ok {
		t.Fatalf("squash: ok=%v err=%v", ok, err)
	}
	c, err = repo.Commit(sq)
	if err != nil {
		t.Fatalf("read squash commit: %v", err)
	}
	if len(c.Parents) != 1 || c.Parents[0] != base {
		t.Fatalf("squash parents = %v, want [%s]", c.Parents, base)
	}

	// rebase: feature's two commits replayed onto base, preserving their order.
	rb, ok, err := store.Merge(ctx, 11, git.MergeRebase, base, feature, "", who, who)
	if err != nil || !ok {
		t.Fatalf("rebase: ok=%v err=%v", ok, err)
	}
	replayed, err := store.CommitsBetween(ctx, 11, base, rb)
	if err != nil {
		t.Fatalf("CommitsBetween after rebase: %v", err)
	}
	if len(replayed) != 2 || replayed[0].Message != "feature one" || replayed[1].Message != "feature two" {
		t.Fatalf("rebased commits wrong: %+v", messagesOf(replayed))
	}

	// A conflicting head merges to ok=false under every strategy, writing no tip.
	for _, m := range []git.MergeMethod{git.MergeCommit, git.MergeSquash} {
		if sha, ok, err := store.Merge(ctx, 11, m, base, conflict, "x", who, who); err != nil || ok || sha != "" {
			t.Fatalf("%s of conflict: sha=%q ok=%v err=%v, want clean failure", m, sha, ok, err)
		}
	}
	if _, clean, err := store.TestMerge(ctx, 11, base, conflict); err != nil || clean {
		t.Fatalf("TestMerge conflict: clean=%v err=%v, want not clean", clean, err)
	}
}

func messagesOf(commits []git.Commit) []string {
	out := make([]string, len(commits))
	for i, c := range commits {
		out[i] = c.Message
	}
	return out
}

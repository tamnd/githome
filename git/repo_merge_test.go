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

	// The direct two-dot diff sees the trees themselves: feature's additions
	// plus master-only m.txt, reversed into a removal.
	direct, err := store.ChangedFilesDirect(ctx, 10, base, feature)
	if err != nil {
		t.Fatalf("ChangedFilesDirect: %v", err)
	}
	gotDirect := map[string]string{}
	for _, f := range direct {
		gotDirect[f.Path] = f.Status
	}
	if gotDirect["b.txt"] != "added" || gotDirect["c.txt"] != "added" {
		t.Fatalf("direct changed files = %v, want b.txt and c.txt added", gotDirect)
	}
	if gotDirect["m.txt"] != "removed" {
		t.Fatalf("direct diff must show master-only m.txt as removed: %v", gotDirect)
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

// TestChangedFilesOptsIgnoreWhitespace proves the ?w=1 path: a file whose only
// change between the ends is indentation drops out of the diff under -w, while a
// file with a real content change stays. The two whitespace modes also cache
// under distinct keys, so the second ask for one form never serves the other.
func TestChangedFilesOptsIgnoreWhitespace(t *testing.T) {
	ctx := context.Background()
	store := git.NewStore(t.TempDir())

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	src := t.TempDir()
	runGit(t, src, "init", "-q", "-b", "master")
	mustWrite(t, filepath.Join(src, "ws.txt"), "alpha\nbeta\n")
	mustWrite(t, filepath.Join(src, "real.txt"), "x\n")
	runGit(t, src, "add", "-A")
	runGit(t, src, "commit", "-q", "-m", "base")
	base := runGit(t, src, "rev-parse", "HEAD")

	// head only re-indents ws.txt (a whitespace-only change) but adds a line to
	// real.txt (a real change).
	mustWrite(t, filepath.Join(src, "ws.txt"), "    alpha\n    beta\n")
	mustWrite(t, filepath.Join(src, "real.txt"), "x\ny\n")
	runGit(t, src, "add", "-A")
	runGit(t, src, "commit", "-q", "-m", "head")
	head := runGit(t, src, "rev-parse", "HEAD")

	bare := store.Dir(20)
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, "", "clone", "-q", "--bare", src, bare)

	// Canonical diff: both files changed.
	full, err := store.ChangedFilesOpts(ctx, 20, base, head, true, false)
	if err != nil {
		t.Fatalf("ChangedFilesOpts canonical: %v", err)
	}
	gotFull := map[string]bool{}
	for _, f := range full {
		gotFull[f.Path] = true
	}
	if !gotFull["ws.txt"] || !gotFull["real.txt"] {
		t.Fatalf("canonical diff = %v, want both ws.txt and real.txt", gotFull)
	}

	// Whitespace-ignored diff: the indentation-only file drops out.
	ws, err := store.ChangedFilesOpts(ctx, 20, base, head, true, true)
	if err != nil {
		t.Fatalf("ChangedFilesOpts ignore-ws: %v", err)
	}
	gotWS := map[string]bool{}
	for _, f := range ws {
		gotWS[f.Path] = true
	}
	if gotWS["ws.txt"] {
		t.Fatalf("ignore-whitespace diff must drop the indentation-only ws.txt: %v", gotWS)
	}
	if !gotWS["real.txt"] {
		t.Fatalf("ignore-whitespace diff must keep the real change real.txt: %v", gotWS)
	}

	// The canonical view is still two files on re-read: the two modes did not
	// collide in the diff cache.
	again, err := store.ChangedFilesOpts(ctx, 20, base, head, true, false)
	if err != nil {
		t.Fatalf("ChangedFilesOpts canonical re-read: %v", err)
	}
	if len(again) != len(full) {
		t.Fatalf("canonical re-read = %d files, want %d (whitespace mode leaked through the cache)", len(again), len(full))
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

// CommitsBetweenN keeps the newest commits of the range when the cap bites,
// still oldest first, and is unbounded at zero.
func TestCommitsBetweenN(t *testing.T) {
	ctx := context.Background()
	store := git.NewStore(t.TempDir())
	base, feature, _ := mergeRepo(t, store, 14)

	all, err := store.CommitsBetweenN(ctx, 14, base, feature, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("unbounded: got %d commits, want 2", len(all))
	}

	one, err := store.CommitsBetweenN(ctx, 14, base, feature, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 {
		t.Fatalf("capped: got %d commits, want 1", len(one))
	}
	if one[0].SHA != all[1].SHA {
		t.Fatalf("capped list should keep the newest commit: got %s want %s", one[0].SHA, all[1].SHA)
	}
}

func TestLastCommitForPath(t *testing.T) {
	ctx := context.Background()
	store := git.NewStore(t.TempDir())
	_, feature, _ := mergeRepo(t, store, 15)

	// Empty path is the branch tip: feature's newest commit is "feature two".
	tip, ok, err := store.LastCommitForPath(ctx, 15, "feature", "")
	if err != nil || !ok {
		t.Fatalf("tip: ok=%v err=%v", ok, err)
	}
	if tip.SHA != feature {
		t.Fatalf("tip = %s, want feature head %s", tip.SHA, feature)
	}
	if tip.Message != "feature two" {
		t.Fatalf("tip message = %q, want %q", tip.Message, "feature two")
	}

	// a.txt was last touched on feature by the shared first commit, two commits
	// behind the tip.
	c, ok, err := store.LastCommitForPath(ctx, 15, "feature", "a.txt")
	if err != nil || !ok {
		t.Fatalf("a.txt: ok=%v err=%v", ok, err)
	}
	if c.Message != "first" {
		t.Fatalf("a.txt message = %q, want %q", c.Message, "first")
	}

	// b.txt landed in "feature one", one behind the tip.
	c, ok, err = store.LastCommitForPath(ctx, 15, "feature", "b.txt")
	if err != nil || !ok {
		t.Fatalf("b.txt: ok=%v err=%v", ok, err)
	}
	if c.Message != "feature one" {
		t.Fatalf("b.txt message = %q, want %q", c.Message, "feature one")
	}

	// A path with no history and a bad revision are absent answers, not errors.
	if _, ok, err := store.LastCommitForPath(ctx, 15, "feature", "no-such-file.txt"); err != nil || ok {
		t.Fatalf("missing path: ok=%v err=%v, want absent and no error", ok, err)
	}
	if _, ok, err := store.LastCommitForPath(ctx, 15, "no-such-branch", ""); err != nil || ok {
		t.Fatalf("bad rev: ok=%v err=%v, want absent and no error", ok, err)
	}
}

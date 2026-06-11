package git_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/githome/git"
)

// refsRepo builds a bare store-backed repository whose ref namespace exercises
// every shape the batched for-each-ref listing parses: two branches, a
// lightweight tag, and two annotated tags, one with a multi-line message so
// the record separator inside contents cannot be confused with a field break.
func refsRepo(t *testing.T, store *git.Store, pk int64) (first, second string) {
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

	mustWrite(t, filepath.Join(src, "a.txt"), "one\ntwo\n")
	runGit(t, src, "add", "-A")
	runGit(t, src, "commit", "-q", "-m", "second")
	second = runGit(t, src, "rev-parse", "HEAD")

	runGit(t, src, "branch", "feature", first)
	runGit(t, src, "tag", "v0.1.0", first)
	runGit(t, src, "tag", "-a", "v1.0.0", "-m", "release one\n\nwith a body line", second)
	runGit(t, src, "tag", "-a", "v1.1.0", "-m", "release two", second)

	bare := store.Dir(pk)
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, "", "clone", "-q", "--bare", src, bare)
	return first, second
}

func TestBatchedRefListings(t *testing.T) {
	store := git.NewStore(t.TempDir())
	first, second := refsRepo(t, store, 30)
	repo, err := store.Open(30)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer repo.Release()

	branches, err := repo.Branches()
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	if len(branches) != 2 {
		t.Fatalf("branches = %+v, want feature and master", branches)
	}
	if branches[0].Name != "feature" || branches[0].Commit != first {
		t.Errorf("branches[0] = %+v, want feature at %s", branches[0], first)
	}
	if branches[1].Name != "master" || branches[1].Commit != second {
		t.Errorf("branches[1] = %+v, want master at %s", branches[1], second)
	}

	tags, err := repo.Tags()
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if len(tags) != 3 {
		t.Fatalf("tags = %+v, want v0.1.0 v1.0.0 v1.1.0", tags)
	}

	light := tags[0]
	if light.Name != "v0.1.0" || light.Commit != first || light.Annotated != nil {
		t.Errorf("lightweight tag = %+v, want plain v0.1.0 at %s", light, first)
	}

	multi := tags[1]
	if multi.Name != "v1.0.0" || multi.Commit != second {
		t.Fatalf("annotated tag = %+v, want v1.0.0 peeling to %s", multi, second)
	}
	if multi.Annotated == nil {
		t.Fatal("v1.0.0 lost its annotation")
	}
	if got := strings.TrimRight(multi.Annotated.Message, "\n"); got != "release one\n\nwith a body line" {
		t.Errorf("multi-line message = %q", got)
	}
	if multi.Annotated.Target != second || multi.Annotated.TargetType != git.ObjectCommit {
		t.Errorf("annotation target = %s (%s), want commit %s",
			multi.Annotated.Target, multi.Annotated.TargetType, second)
	}
	if multi.Annotated.Tagger.Name != "Octo Cat" || multi.Annotated.Tagger.Email != "octo@example.com" {
		t.Errorf("tagger = %+v", multi.Annotated.Tagger)
	}
	if multi.Annotated.Tagger.When.IsZero() {
		t.Error("tagger date did not parse")
	}

	// The record after the multi-line message is the parse's hardest case:
	// its fields must not absorb the previous record's body.
	single := tags[2]
	if single.Name != "v1.1.0" || single.Commit != second || single.Annotated == nil {
		t.Fatalf("tag after multi-line message = %+v", single)
	}
	if got := strings.TrimRight(single.Annotated.Message, "\n"); got != "release two" {
		t.Errorf("message = %q, want release two", got)
	}

	refs, err := repo.Refs()
	if err != nil {
		t.Fatalf("Refs: %v", err)
	}
	want := map[string]git.ObjectType{
		"refs/heads/feature": git.ObjectCommit,
		"refs/heads/master":  git.ObjectCommit,
		"refs/tags/v0.1.0":   git.ObjectCommit,
		"refs/tags/v1.0.0":   git.ObjectTag,
		"refs/tags/v1.1.0":   git.ObjectTag,
	}
	if len(refs) != len(want) {
		t.Fatalf("refs = %+v, want %d entries", refs, len(want))
	}
	for _, ref := range refs {
		typ, ok := want[ref.Name]
		if !ok {
			t.Errorf("unexpected ref %s", ref.Name)
			continue
		}
		if ref.Type != typ {
			t.Errorf("%s type = %s, want %s", ref.Name, ref.Type, typ)
		}
		if ref.Target == "" {
			t.Errorf("%s has no target", ref.Name)
		}
	}
}

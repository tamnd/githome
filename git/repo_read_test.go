package git

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/go-git/go-billy/v5/util"
)

// fixture is the shape of the repository buildFixture creates.
type fixture struct {
	repo      *Repo
	first     SHA // root commit (README.md only)
	second    SHA // adds docs/guide.md
	branch    string
	annotated string // annotated tag name
	light     string // lightweight tag name
}

var fixedWhen = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func sig() *object.Signature {
	return &object.Signature{Name: "Octo Cat", Email: "octo@example.com", When: fixedWhen}
}

// buildFixture initializes a worktree repository in a temp dir with two commits
// and two tags, then returns it wrapped as a read-only Repo. The read methods
// behave identically on a worktree or bare repository, so this exercises them
// without standing up a bare repo and a clone.
func buildFixture(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	r, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	fs := wt.Filesystem

	if err := util.WriteFile(fs, "README.md", []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	first, err := wt.Commit("initial commit", &gogit.CommitOptions{Author: sig(), Committer: sig()})
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}

	if err := util.WriteFile(fs, "docs/guide.md", []byte("guide body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("docs/guide.md"); err != nil {
		t.Fatal(err)
	}
	second, err := wt.Commit("add the guide", &gogit.CommitOptions{Author: sig(), Committer: sig()})
	if err != nil {
		t.Fatalf("second commit: %v", err)
	}

	// A lightweight tag and an annotated tag, so Tags and Refs cover both.
	if _, err := r.CreateTag("v0.1.0", first, nil); err != nil {
		t.Fatalf("lightweight tag: %v", err)
	}
	if _, err := r.CreateTag("v1.0.0", second, &gogit.CreateTagOptions{Tagger: sig(), Message: "release one"}); err != nil {
		t.Fatalf("annotated tag: %v", err)
	}

	head, err := r.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	return fixture{
		repo:      &Repo{repo: r},
		first:     first.String(),
		second:    second.String(),
		branch:    head.Name().Short(),
		annotated: "v1.0.0",
		light:     "v0.1.0",
	}
}

func TestHEADAndBranches(t *testing.T) {
	fx := buildFixture(t)
	head, err := fx.repo.HEAD()
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	if head.Name != fx.branch || head.Commit != fx.second {
		t.Fatalf("HEAD = %+v, want %s at %s", head, fx.branch, fx.second)
	}
	branches, err := fx.repo.Branches()
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	if len(branches) != 1 || branches[0].Name != fx.branch || branches[0].Commit != fx.second {
		t.Fatalf("Branches = %+v, want one %s at %s", branches, fx.branch, fx.second)
	}
}

func TestCommitAndLog(t *testing.T) {
	fx := buildFixture(t)
	c, err := fx.repo.Commit("HEAD")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if c.SHA != fx.second {
		t.Errorf("Commit SHA = %s, want %s", c.SHA, fx.second)
	}
	if len(c.Parents) != 1 || c.Parents[0] != fx.first {
		t.Errorf("Parents = %v, want [%s]", c.Parents, fx.first)
	}
	if c.Message != "add the guide" {
		t.Errorf("Message = %q", c.Message)
	}
	if c.Author.Name != "Octo Cat" || c.Author.Email != "octo@example.com" || !c.Author.When.Equal(fixedWhen) {
		t.Errorf("Author = %+v", c.Author)
	}

	log, err := fx.repo.Log(LogOpts{From: "HEAD"})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(log) != 2 || log[0].SHA != fx.second || log[1].SHA != fx.first {
		t.Fatalf("Log = %+v, want [second, first]", log)
	}

	// Path-filtered log: docs/guide.md only appears in the second commit.
	docs, err := fx.repo.Log(LogOpts{From: "HEAD", Path: "docs/guide.md"})
	if err != nil {
		t.Fatalf("Log(path): %v", err)
	}
	if len(docs) != 1 || docs[0].SHA != fx.second {
		t.Fatalf("path log = %+v, want [second]", docs)
	}
}

func TestTreeRecursiveAndShallow(t *testing.T) {
	fx := buildFixture(t)
	shallow, err := fx.repo.Tree("HEAD", false)
	if err != nil {
		t.Fatalf("Tree shallow: %v", err)
	}
	names := map[string]TreeEntry{}
	for _, e := range shallow.Entries {
		names[e.Path] = e
	}
	if _, ok := names["README.md"]; !ok {
		t.Errorf("shallow tree missing README.md: %+v", shallow.Entries)
	}
	if docs, ok := names["docs"]; !ok || docs.Type != ObjectTree || docs.Mode != "040000" {
		t.Errorf("shallow tree docs entry = %+v", docs)
	}
	if readme := names["README.md"]; readme.Type != ObjectBlob || readme.Mode != "100644" || readme.Size != int64(len("# Hello\n")) {
		t.Errorf("README entry = %+v", names["README.md"])
	}

	rec, err := fx.repo.Tree("HEAD", true)
	if err != nil {
		t.Fatalf("Tree recursive: %v", err)
	}
	var foundNested bool
	for _, e := range rec.Entries {
		if e.Path == "docs/guide.md" && e.Type == ObjectBlob {
			foundNested = true
		}
	}
	if !foundNested {
		t.Errorf("recursive tree missing docs/guide.md: %+v", rec.Entries)
	}
}

func TestPathAt(t *testing.T) {
	fx := buildFixture(t)

	file, err := fx.repo.PathAt("HEAD", "README.md")
	if err != nil {
		t.Fatalf("PathAt file: %v", err)
	}
	if file.IsDir || file.File == nil || string(file.File.Content) != "# Hello\n" {
		t.Fatalf("file result = %+v", file)
	}
	if file.Entry.Type != ObjectBlob || file.Entry.Path != "README.md" {
		t.Errorf("file entry = %+v", file.Entry)
	}

	dir, err := fx.repo.PathAt("HEAD", "docs")
	if err != nil {
		t.Fatalf("PathAt dir: %v", err)
	}
	if !dir.IsDir || len(dir.Dir) != 1 || dir.Dir[0].Path != "docs/guide.md" {
		t.Fatalf("dir result = %+v", dir)
	}

	root, err := fx.repo.PathAt("HEAD", "")
	if err != nil {
		t.Fatalf("PathAt root: %v", err)
	}
	if !root.IsDir || len(root.Dir) != 2 {
		t.Fatalf("root listing = %+v", root.Dir)
	}

	if _, err := fx.repo.PathAt("HEAD", "nope.txt"); !errors.Is(err, ErrPathNotFound) {
		t.Errorf("PathAt(missing): err = %v, want ErrPathNotFound", err)
	}
}

func TestTagsAndRefs(t *testing.T) {
	fx := buildFixture(t)
	tags, err := fx.repo.Tags()
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	byName := map[string]Tag{}
	for _, tg := range tags {
		byName[tg.Name] = tg
	}
	light, ok := byName[fx.light]
	if !ok || light.Annotated != nil || light.Commit != fx.first {
		t.Errorf("lightweight tag = %+v", light)
	}
	ann, ok := byName[fx.annotated]
	if !ok || ann.Annotated == nil || ann.Commit != fx.second {
		t.Fatalf("annotated tag = %+v", ann)
	}
	if strings.TrimSpace(ann.Annotated.Message) != "release one" || ann.Annotated.Tagger.Name != "Octo Cat" {
		t.Errorf("annotated metadata = %+v", ann.Annotated)
	}

	refs, err := fx.repo.Refs()
	if err != nil {
		t.Fatalf("Refs: %v", err)
	}
	var sawBranch, sawTag bool
	for _, ref := range refs {
		if ref.Name == "refs/heads/"+fx.branch {
			sawBranch = true
		}
		if ref.Name == "refs/tags/"+fx.annotated && ref.Type == ObjectTag {
			sawTag = true
		}
	}
	if !sawBranch || !sawTag {
		t.Errorf("Refs missing branch or annotated tag: %+v", refs)
	}

	single, err := fx.repo.RefByName("heads/" + fx.branch)
	if err != nil {
		t.Fatalf("RefByName: %v", err)
	}
	if single.Name != "refs/heads/"+fx.branch || single.Target != fx.second {
		t.Errorf("RefByName = %+v", single)
	}
}

func TestBlob(t *testing.T) {
	fx := buildFixture(t)
	// Resolve the README blob sha through the tree, then read it back.
	res, err := fx.repo.PathAt("HEAD", "README.md")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := fx.repo.Blob(res.Entry.SHA)
	if err != nil {
		t.Fatalf("Blob: %v", err)
	}
	if string(blob.Content) != "# Hello\n" || blob.Size != int64(len("# Hello\n")) {
		t.Errorf("blob = %+v", blob)
	}
}

func TestStoreLayoutAndOpen(t *testing.T) {
	root := t.TempDir()
	st := NewStore(root)

	const pk = int64(258) // 258 % 256 == 2
	wantDir := filepath.Join(root, "2", "258.git")
	if got := st.Dir(pk); got != wantDir {
		t.Fatalf("Dir(%d) = %q, want %q", pk, got, wantDir)
	}

	if _, err := st.Open(pk); !errors.Is(err, ErrRepoNotFound) {
		t.Errorf("Open(missing) err = %v, want ErrRepoNotFound", err)
	}

	if _, err := st.Init(pk); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := st.Open(pk); err != nil {
		t.Fatalf("Open after Init: %v", err)
	}
	// The shard directory should now hold the bare repo.
	if _, err := st.Open(int64(2)); !errors.Is(err, ErrRepoNotFound) {
		t.Errorf("Open(other pk) err = %v, want ErrRepoNotFound", err)
	}
}

func TestEmptyRepository(t *testing.T) {
	st := NewStore(t.TempDir())
	repo, err := st.Init(7)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := repo.HEAD(); !errors.Is(err, ErrEmptyRepository) {
		t.Errorf("HEAD on empty repo err = %v, want ErrEmptyRepository", err)
	}
	if _, err := repo.Tree("HEAD", false); !errors.Is(err, ErrEmptyRepository) {
		t.Errorf("Tree on empty repo err = %v, want ErrEmptyRepository", err)
	}
	branches, err := repo.Branches()
	if err != nil || len(branches) != 0 {
		t.Errorf("Branches on empty repo = %+v, %v", branches, err)
	}
}

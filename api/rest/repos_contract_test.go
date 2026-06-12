package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/go-git/go-billy/v5/util"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/jsondiff"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// repoFixture is the deterministic repository the repo contract tests run
// against, plus the object ids they need to build sha-addressed URLs. The
// store and git store handles let a test seed extra rows (an org, a fork)
// beyond the octocat/hello baseline.
type repoFixture struct {
	srv      *httptest.Server
	token    string
	branch   string
	headSHA  string
	treeSHA  string
	blobSHA  string // README.md blob at HEAD
	firstSHA string

	st       *store.Store
	gitStore *git.Store
	ownerPK  int64
	repoPK   int64
}

// fixedWhen pins every commit and tag time so the git object ids are stable
// across runs; recorded goldens then stay valid.
var fixedWhen = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func repoServer(t *testing.T) repoFixture {
	t.Helper()
	return repoServerCap(t, 0)
}

// repoServerCap is repoServer with an optional blob size cap on the git store.
// A zero cap leaves the store's built-in default; a positive cap lets a test
// exercise the 403 too_large path on an otherwise small fixture.
func repoServerCap(t *testing.T, blobCap int64) repoFixture {
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

	u := &store.UserRow{Login: "octocat", Type: "User"}
	if err := st.InsertUser(ctx, u); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	g, err := auth.GenerateToken(auth.PrefixClassicPAT)
	if err != nil {
		t.Fatal(err)
	}
	hash := g.Hash
	if err := st.InsertToken(ctx, &store.TokenRow{
		UserPK: &u.PK, TokenHash: hash[:], TokenPrefix: auth.PrefixClassicPAT,
		LastEight: g.Last8, Kind: "pat", Scopes: "repo",
	}); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	desc := "the hello repo"
	pushed := fixedWhen
	repo := &store.RepoRow{OwnerPK: u.PK, Name: "hello", Description: &desc, DefaultBranch: "master", PushedAt: &pushed}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("insert repo: %v", err)
	}

	gitRoot := t.TempDir()
	gitStore := git.NewStore(gitRoot)
	fx := buildRepoFixture(t, gitStore.Dir(repo.PK))

	gr, err := gitStore.Open(repo.PK)
	if err != nil {
		t.Fatalf("open git repo: %v", err)
	}
	head, _ := gr.HEAD()
	commit, _ := gr.Commit("HEAD")
	readme, _ := gr.PathAt("HEAD", "README.md")
	fx.branch = head.Name
	fx.headSHA = head.Commit
	fx.treeSHA = commit.Tree
	fx.blobSHA = readme.Entry.SHA

	// Apply the cap only after the fixture metadata is resolved, so request-time
	// reads enforce it while the setup reads above stay unaffected.
	if blobCap != 0 {
		gitStore.SetMaxBlobBytes(blobCap)
	}

	authSvc := auth.NewService(st, "https://git.test.internal")
	t.Cleanup(authSvc.Close)
	cfg := authConfig(t)
	root := mizu.NewRouter()
	Mount(root, Deps{
		Config:     cfg,
		Ready:      st,
		Auth:       authSvc,
		Users:      domain.NewUserService(st),
		Repos:      domain.NewRepoService(st, gitStore),
		Keys:       domain.NewKeyService(st),
		Teams:      domain.NewTeamService(st),
		URLs:       presenter.NewURLBuilder(cfg.URLs),
		NodeFormat: nodeid.FormatNew,
	})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	fx.srv = srv
	fx.token = g.Plaintext
	fx.st = st
	fx.gitStore = gitStore
	fx.ownerPK = u.PK
	fx.repoPK = repo.PK
	return fx
}

// buildRepoFixture writes two commits and two tags into a fresh repository at
// dir. The read methods behave the same on this worktree repo as on a bare one.
func buildRepoFixture(t *testing.T, dir string) repoFixture {
	t.Helper()
	r, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	fs := wt.Filesystem
	sig := &object.Signature{Name: "Octo Cat", Email: "octo@example.com", When: fixedWhen}

	if err := util.WriteFile(fs, "README.md", []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	first, err := wt.Commit("initial commit", &gogit.CommitOptions{Author: sig, Committer: sig})
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}
	if err := util.WriteFile(fs, "docs/guide.md", []byte("guide body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("docs/guide.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("add the guide", &gogit.CommitOptions{Author: sig, Committer: sig}); err != nil {
		t.Fatalf("second commit: %v", err)
	}
	if _, err := r.CreateTag("v0.1.0", first, nil); err != nil {
		t.Fatalf("lightweight tag: %v", err)
	}
	if _, err := r.CreateTag("v1.0.0", first, &gogit.CreateTagOptions{Tagger: sig, Message: "release one"}); err != nil {
		t.Fatalf("annotated tag: %v", err)
	}
	return repoFixture{firstSHA: first.String()}
}

// assertGolden compares an authenticated GET against testdata/<name>. With
// RECORD=1 it (re)writes the golden from the response, normalizing the test
// host to the HOST sentinel so the files match the rest of testdata.
func (fx repoFixture) assertGolden(t *testing.T, name, path string) {
	t.Helper()
	resp, body := authedGet(t, fx.srv, path, "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status %d, body %s", path, resp.StatusCode, body)
	}
	file := filepath.Join("testdata", name)
	if os.Getenv("RECORD") == "1" {
		norm := strings.ReplaceAll(string(body), "git.test.internal", "HOST")
		if err := os.WriteFile(file, append([]byte(norm), '\n'), 0o644); err != nil {
			t.Fatalf("record %s: %v", file, err)
		}
		return
	}
	jsondiff.AssertCompatible(t, golden(t, name), body, jsondiff.Default("git.test.internal"))
}

func TestRepositoryContract(t *testing.T) {
	fx := repoServer(t)
	fx.assertGolden(t, "repository.golden.json", "/repos/octocat/hello")
}

func TestBranchesContract(t *testing.T) {
	fx := repoServer(t)
	fx.assertGolden(t, "branches.golden.json", "/repos/octocat/hello/branches")
	fx.assertGolden(t, "branch.golden.json", "/repos/octocat/hello/branches/"+fx.branch)
}

func TestTagsContract(t *testing.T) {
	fx := repoServer(t)
	fx.assertGolden(t, "tags.golden.json", "/repos/octocat/hello/tags")
}

func TestRefsContract(t *testing.T) {
	fx := repoServer(t)
	fx.assertGolden(t, "refs.golden.json", "/repos/octocat/hello/git/refs")
	fx.assertGolden(t, "ref.golden.json", "/repos/octocat/hello/git/ref/heads/"+fx.branch)
}

func TestCommitsContract(t *testing.T) {
	fx := repoServer(t)
	fx.assertGolden(t, "commits.golden.json", "/repos/octocat/hello/commits")
}

func TestContentsContract(t *testing.T) {
	fx := repoServer(t)
	fx.assertGolden(t, "contents_file.golden.json", "/repos/octocat/hello/contents/README.md")
	fx.assertGolden(t, "contents_dir.golden.json", "/repos/octocat/hello/contents")
}

func TestGitDataContract(t *testing.T) {
	fx := repoServer(t)
	fx.assertGolden(t, "blob.golden.json", "/repos/octocat/hello/git/blobs/"+fx.blobSHA)
	fx.assertGolden(t, "tree.golden.json", "/repos/octocat/hello/git/trees/"+fx.treeSHA+"?recursive=1")
	fx.assertGolden(t, "git_commit.golden.json", "/repos/octocat/hello/git/commits/"+fx.headSHA)
}

// TestBlobTooLarge confirms a blob past the server's size ceiling comes back as
// a 403 on both the contents and git blob endpoints, rather than buffering the
// whole object. The fixture's README is eight bytes, so a one-byte cap trips it.
func TestBlobTooLarge(t *testing.T) {
	fx := repoServerCap(t, 1)

	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/contents/README.md", fx.token)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("oversized contents status %d, want 403, body %s", resp.StatusCode, body)
	}
	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/git/blobs/"+fx.blobSHA, fx.token)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("oversized blob status %d, want 403, body %s", resp.StatusCode, body)
	}
}

// TestPrivateRepoHidden confirms a private repo the actor cannot see is a 404,
// not a 403, so its existence does not leak.
func TestPrivateRepoHidden(t *testing.T) {
	fx := repoServer(t)
	resp, _ := authedGet(t, fx.srv, "/repos/octocat/nope", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing repo status %d, want 404", resp.StatusCode)
	}
}

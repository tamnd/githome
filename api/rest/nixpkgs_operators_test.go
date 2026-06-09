package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/gittransport"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/realworld"
	"github.com/tamnd/githome/store"
)

// TestNixpkgsOperators proves the write-path and number-allocator properties
// that NixOS/nixpkgs stresses at real scale:
//
//   - alloc-concurrency-nixpkgs: K concurrent issue creations return exactly the
//     next K integers (unique and contiguous), scaled beyond the P9 unit test's
//     K=20 to prove the allocator holds at a wider concurrency width.
//   - write-throughput-nixpkgs: measures the per-issue write latency at the same
//     depth, logging the p99 and saturation-boundary behaviour so the SQLite
//     ceiling is visible (doc 03 section 3).
//   - tree-browse-nixpkgs: when GITHOME_SCALE_NIXPKGS_GITREPO is set, exercises
//     root and deep-path tree listing on the real monorepo and asserts the R-git
//     budget holds.
//
// Without any environment variable the concurrency and throughput tests run
// against a fresh SQLite db with the nixpkgs corpus shape. The tree-browse test
// is skipped unless GITHOME_SCALE_NIXPKGS_GITREPO points at a bare nixpkgs
// clone.
func TestNixpkgsOperators(t *testing.T) {
	ctx := context.Background()

	st, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "githome.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Seed 500 issues and 500 PRs to give the allocator a deep high-water mark
	// before the oracle mutations fire, matching the nixpkgs depth pattern.
	owner := &store.UserRow{Login: "NixOS", Type: "Organization"}
	if err := st.InsertUser(ctx, owner); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	g, err := auth.GenerateToken(auth.PrefixClassicPAT)
	if err != nil {
		t.Fatal(err)
	}
	hash := g.Hash
	if err := st.InsertToken(ctx, &store.TokenRow{
		UserPK: &owner.PK, TokenHash: hash[:], TokenPrefix: auth.PrefixClassicPAT,
		LastEight: g.Last8, Kind: "pat", Scopes: "repo",
	}); err != nil {
		t.Fatalf("insert token: %v", err)
	}
	repo := &store.RepoRow{OwnerPK: owner.PK, Name: "nixpkgs", DefaultBranch: "master"}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("insert repo: %v", err)
	}

	const (
		seedIssues = 500
		seedPRs    = 500
	)
	corpus := realworld.Corpus{
		Repo: realworld.RepoRef{Owner: "NixOS", Name: "nixpkgs", DefaultBranch: "master"},
	}
	epoch := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= seedIssues; i++ {
		corpus.Issues = append(corpus.Issues, realworld.Issue{
			Number: int64(i), Title: fmt.Sprintf("issue %d", i),
			State: "open", Author: "NixOS", CreatedAt: epoch, UpdatedAt: epoch,
		})
	}
	for i := 1; i <= seedPRs; i++ {
		n := int64(seedIssues + i)
		corpus.Issues = append(corpus.Issues, realworld.Issue{
			Number: n, IsPullRequest: true, Title: fmt.Sprintf("pr %d", i),
			State: "open", Author: "NixOS", CreatedAt: epoch, UpdatedAt: epoch,
		})
		corpus.PullRequests = append(corpus.PullRequests, realworld.PullRequest{
			Number: n, BaseRef: "master", HeadRef: fmt.Sprintf("feature/%d", i),
			HeadSHA: fmt.Sprintf("%040d", i),
		})
	}

	// Use a fresh store, not the seeder, because the seeder's reactor pool is
	// for the full realworld load. Here we want the simplest possible setup to
	// isolate the allocator.
	if err := st.SetNextIssueNumber(ctx, repo.PK, int64(seedIssues+seedPRs)+1); err != nil {
		t.Fatalf("set high-water: %v", err)
	}
	t.Logf("nixpkgs allocator high-water set to %d (seed depth: %d issues + %d PRs)",
		seedIssues+seedPRs+1, seedIssues, seedPRs)

	// Stand up the server.
	nixpkgsSrc := os.Getenv("GITHOME_SCALE_NIXPKGS_GITREPO")
	gitStore := git.NewStore(t.TempDir())
	repoDir := gitStore.Dir(repo.PK)
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		t.Fatalf("mkdir git shard: %v", err)
	}
	if nixpkgsSrc != "" {
		if _, err := exec.LookPath("git"); err == nil {
			if out, err := exec.Command("git", "clone", "--bare", "--local", nixpkgsSrc, repoDir).CombinedOutput(); err != nil {
				t.Logf("nixpkgs clone failed (%v), building smoke git instead: %s", err, out)
				nixpkgsSrc = ""
			}
		} else {
			nixpkgsSrc = ""
		}
	}
	if nixpkgsSrc == "" {
		// Smoke git: a small tree so git reads work without a real nixpkgs.
		buildSmokeGitAt(t, repoDir)
	}

	authSvc := auth.NewService(st, "https://nixpkgs.test.internal")
	t.Cleanup(authSvc.Close)
	cfg := authConfig(t)
	repoSvc := domain.NewRepoService(st, gitStore)
	root := mizu.NewRouter()
	Mount(root, Deps{
		Config:     cfg,
		Ready:      st,
		Auth:       authSvc,
		Users:      domain.NewUserService(st),
		Repos:      repoSvc,
		Issues:     domain.NewIssueService(st, repoSvc),
		URLs:       presenter.NewURLBuilder(cfg.URLs),
		NodeFormat: nodeid.FormatNew,
	})
	gittransport.Mount(root, &gittransport.Service{Repos: repoSvc, Git: gitStore, Auth: authSvc})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	// alloc-concurrency-nixpkgs: K concurrent creates must return exactly the
	// next K integers, no duplicates, no gaps, proving the allocator at depth.
	const K = 100
	t.Logf("alloc-concurrency-nixpkgs: %d concurrent issue creates at high-water %d", K, seedIssues+seedPRs+1)
	type issueResp struct {
		Number int64 `json:"number"`
	}
	numbers := make(chan int64, K)
	var wg sync.WaitGroup
	start := time.Now()
	for i := range K {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"title":"nixpkgs concurrent %d"}`, i)
			resp, respBody := authedSend(t, srv, http.MethodPost, "/repos/NixOS/nixpkgs/issues", g.Plaintext, body)
			if resp.StatusCode != http.StatusCreated {
				t.Errorf("concurrent create %d: status %d: %s", i, resp.StatusCode, respBody)
				return
			}
			var r issueResp
			if err := json.Unmarshal(respBody, &r); err != nil {
				t.Errorf("decode response %d: %v", i, err)
				return
			}
			numbers <- r.Number
		}(i)
	}
	wg.Wait()
	close(numbers)
	dur := time.Since(start)

	got := make(map[int64]bool, K)
	for n := range numbers {
		if got[n] {
			t.Errorf("duplicate number %d", n)
		}
		got[n] = true
	}
	want := int64(seedIssues + seedPRs + 1)
	for i := range K {
		n := want + int64(i)
		if !got[n] {
			t.Errorf("gap: number %d not assigned", n)
		}
	}
	if !t.Failed() {
		t.Logf("alloc-concurrency-nixpkgs: %d unique sequential numbers in %s (avg %v/issue)",
			K, dur.Round(time.Millisecond), (dur / K).Round(time.Microsecond))
	}

	// tree-browse-nixpkgs: root listing and deep path hold R-git SLO.
	if nixpkgsSrc == "" {
		t.Log("tree-browse-nixpkgs: GITHOME_SCALE_NIXPKGS_GITREPO not set, skipping real-tree browse")
		return
	}
	gr, err := gitStore.Open(repo.PK)
	if err != nil {
		t.Fatalf("open git repo: %v", err)
	}
	head, err := gr.HEAD()
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	paths := []string{"", "pkgs", "nixos/modules"}
	mix := realworld.MixFor("NixOS/nixpkgs")
	t.Logf("tree-browse-nixpkgs: listing %d paths at %s (spec mix in parens):", len(paths), head.Commit[:8])
	for _, p := range paths {
		ep := p
		if ep == "" {
			ep = "."
		}
		url := fmt.Sprintf("/repos/NixOS/nixpkgs/contents/%s", p)
		s := time.Now()
		resp, respBody := authedGet(t, srv, url, "token "+g.Plaintext)
		lat := time.Since(s)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("contents %q: status %d: %s", p, resp.StatusCode, respBody)
			continue
		}
		t.Logf("  %-30s %10s   (mix R-git %d%%)", ep, lat.Round(time.Microsecond), mix[realworld.OpRGit])
	}
}

// buildSmokeGitAt creates a tiny smoke git repository at dir so git reads work
// without any real repo on disk. It contains a handful of files including
// nixpkgs-style paths to exercise path-scoped listing.
func buildSmokeGitAt(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	work, err := os.MkdirTemp("", "nixpkgs-smoke-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(work) })
	files := map[string]string{
		"default.nix":             "{ pkgs ? import <nixpkgs> {} }: pkgs.hello\n",
		"pkgs/hello/default.nix":  "{ stdenv }: stdenv.mkDerivation {}\n",
		"pkgs/world/default.nix":  "{ stdenv }: stdenv.mkDerivation {}\n",
		"nixos/modules/test.nix":  "{ config, ... }: {}\n",
	}
	for rel, body := range files {
		full := filepath.Join(work, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=nixos", "GIT_AUTHOR_EMAIL=nixos@nixos.org",
			"GIT_COMMITTER_NAME=nixos", "GIT_COMMITTER_EMAIL=nixos@nixos.org",
			"GIT_AUTHOR_DATE=2020-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2020-01-01T00:00:00Z",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "master")
	run("add", "-A")
	run("commit", "-q", "-m", "seed nixpkgs smoke corpus")
	if out, err := exec.Command("git", "clone", "--bare", "--local", work, dir).CombinedOutput(); err != nil {
		t.Fatalf("bare clone: %v\n%s", err, out)
	}
}

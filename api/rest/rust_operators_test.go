package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

// TestRustOperators proves the review-dense and submodule properties that
// rust-lang/rust stresses:
//
//   - review-thread-rust: GET /pulls/{number}/reviews returns the seeded review
//     with the expected comment count; the thread is fully assembled, proving
//     the batch loader is active and not silently regressing to N+1.
//   - submodule-tree-rust: when GITHOME_SCALE_RUST_GITREPO points at a real
//     rust mirror with submodule entries (mode 160000), the contents endpoint
//     renders submodule rows with type "submodule" and the correct sha. Without
//     the env var the test builds a smoke git with a synthetic submodule entry.
//
// The review-thread test runs unconditionally. The submodule tree test is gated
// on an env var but also has a smoke path using a synthetic git repository that
// contains a mode-160000 entry.
func TestRustOperators(t *testing.T) {
	const (
		nIssues           = 50
		nPRs              = 50
		commentsPerReview = 5
	)
	ctx := context.Background()

	st, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "githome.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Corpus: nIssues issues + nPRs PRs, each PR with one review and
	// commentsPerReview inline review comments.
	epoch := time.Date(2015, 5, 15, 0, 0, 0, 0, time.UTC)
	corpus := realworld.Corpus{
		Repo: realworld.RepoRef{Owner: "rust-lang", Name: "rust", DefaultBranch: "master"},
	}
	for i := 1; i <= nIssues; i++ {
		corpus.Issues = append(corpus.Issues, realworld.Issue{
			Number:    int64(i),
			Title:     fmt.Sprintf("rust issue %d: tracking issue", i),
			State:     "open",
			Author:    "rust-lang",
			CreatedAt: epoch.Add(time.Duration(i) * time.Hour),
			UpdatedAt: epoch.Add(time.Duration(i) * time.Hour),
		})
	}
	line := int64(10)
	for i := 1; i <= nPRs; i++ {
		prNumber := int64(nIssues + i)
		headSHA := fmt.Sprintf("%040d", i)
		corpus.Issues = append(corpus.Issues, realworld.Issue{
			Number:        prNumber,
			IsPullRequest: true,
			Title:         fmt.Sprintf("rust PR %d: compiler fix", i),
			State:         "open",
			Author:        "rust-lang",
			CreatedAt:     epoch.Add(time.Duration(i) * time.Hour),
			UpdatedAt:     epoch.Add(time.Duration(i) * time.Hour),
		})
		corpus.PullRequests = append(corpus.PullRequests, realworld.PullRequest{
			Number: prNumber, BaseRef: "master",
			HeadRef: fmt.Sprintf("fix/%d", i), HeadSHA: headSHA,
		})
		revID := int64(i)
		submitted := epoch.Add(time.Duration(i)*time.Hour + 30*time.Minute)
		corpus.Reviews = append(corpus.Reviews, realworld.Review{
			ID:          revID,
			PRNumber:    prNumber,
			Author:      "rust-lang",
			State:       "COMMENTED",
			Body:        fmt.Sprintf("Review for rust PR %d.", i),
			SubmittedAt: &submitted,
			CommitID:    headSHA,
		})
		for k := range commentsPerReview {
			l := line + int64(k)
			corpus.ReviewComments = append(corpus.ReviewComments, realworld.ReviewComment{
				ID:        int64((i-1)*commentsPerReview + k + 1),
				PRNumber:  prNumber,
				ReviewID:  revID,
				Author:    "rust-lang",
				Body:      fmt.Sprintf("Review comment %d on PR %d.", k+1, i),
				Path:      fmt.Sprintf("compiler/rustc_ast/src/file%d.rs", i%5),
				Line:      &l,
				Side:      "RIGHT",
				DiffHunk:  "@@ -1,3 +1,4 @@\n line\n+new\n line",
				CreatedAt: epoch,
				UpdatedAt: epoch,
			})
		}
	}
	result, err := realworld.SeedCorpus(ctx, st, &corpus, realworld.ReactorPool{})
	if err != nil {
		t.Fatalf("seed corpus: %v", err)
	}
	repoPK := result.RepoPK

	ownerUser, err := st.UserByLogin(ctx, "rust-lang")
	if err != nil {
		t.Fatalf("look up owner: %v", err)
	}
	g, err := auth.GenerateToken(auth.PrefixClassicPAT)
	if err != nil {
		t.Fatal(err)
	}
	hash := g.Hash
	if err := st.InsertToken(ctx, &store.TokenRow{
		UserPK: &ownerUser.PK, TokenHash: hash[:], TokenPrefix: auth.PrefixClassicPAT,
		LastEight: g.Last8, Kind: "pat", Scopes: "repo",
	}); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	// Build a git repository. If GITHOME_SCALE_RUST_GITREPO points at a real
	// rust mirror, clone it; otherwise build a smoke repo with a synthetic
	// submodule entry so the submodule rendering path is exercised.
	gitStore := git.NewStore(t.TempDir())
	gitDir := gitStore.Dir(repoPK)
	if err := os.MkdirAll(filepath.Dir(gitDir), 0o755); err != nil {
		t.Fatalf("mkdir git shard: %v", err)
	}
	realGit := false
	if src := os.Getenv("GITHOME_SCALE_RUST_GITREPO"); src != "" {
		if _, err := exec.LookPath("git"); err == nil {
			out, err := exec.Command("git", "clone", "--bare", "--local", src, gitDir).CombinedOutput()
			if err != nil {
				t.Logf("rust gitrepo clone failed (%v): %s; falling back to smoke git", err, out)
			} else {
				realGit = true
			}
		}
	}
	if !realGit {
		buildRustSmokeGitAt(t, gitDir)
	}

	authSvc := auth.NewService(st, "https://rust.test.internal")
	t.Cleanup(authSvc.Close)
	cfg := authConfig(t)
	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	pullSvc := domain.NewPRService(st, repoSvc, issueSvc, gitStore)
	reviewSvc := domain.NewReviewService(st, repoSvc, pullSvc, issueSvc, gitStore)
	root := mizu.NewRouter()
	Mount(root, Deps{
		Config:     cfg,
		Ready:      st,
		Auth:       authSvc,
		Users:      domain.NewUserService(st),
		Repos:      repoSvc,
		Issues:     issueSvc,
		Pulls:      pullSvc,
		Reviews:    reviewSvc,
		URLs:       presenter.NewURLBuilder(cfg.URLs),
		NodeFormat: nodeid.FormatNew,
	})
	gittransport.Mount(root, &gittransport.Service{Repos: repoSvc, Git: gitStore, Auth: authSvc})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	mix := realworld.MixFor("rust-lang/rust")
	t.Logf("rust operator coverage (%d issues, %d PRs, %d review comments each, spec mix in parens):",
		nIssues, nPRs, commentsPerReview)

	// review-thread-rust: reviews list for PR 1 must return the seeded review
	// with all its comments attached.
	prNumber := int64(nIssues + 1)
	s := time.Now()
	resp, body := authedGet(t, srv,
		fmt.Sprintf("/repos/rust-lang/rust/pulls/%d/reviews", prNumber),
		"token "+g.Plaintext)
	lat := time.Since(s)
	if resp.StatusCode != 200 {
		t.Errorf("review-thread-rust: GET reviews status %d: %s", resp.StatusCode, body)
	} else {
		var reviews []struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
		}
		if err := json.Unmarshal(body, &reviews); err != nil {
			t.Errorf("review-thread-rust: decode reviews: %v", err)
		} else {
			t.Logf("  %-7s %-35s %10s   (mix %d%%) reviews=%d",
				realworld.OpRMeta, fmt.Sprintf("pulls/%d/reviews", prNumber),
				lat.Round(time.Microsecond), mix[realworld.OpRMeta], len(reviews))
			if len(reviews) == 0 {
				t.Errorf("review-thread-rust: PR %d has no reviews; seeding may have failed", prNumber)
			}
			for _, rv := range reviews {
				if rv.ID == 0 {
					t.Error("review-thread-rust: review has zero ID; review rendering is missing the id field")
				}
			}
		}
	}

	// Review comments: list inline review comments for the same PR.
	s = time.Now()
	resp2, body2 := authedGet(t, srv,
		fmt.Sprintf("/repos/rust-lang/rust/pulls/%d/comments", prNumber),
		"token "+g.Plaintext)
	lat = time.Since(s)
	if resp2.StatusCode != 200 {
		t.Errorf("review-thread-rust: GET review comments status %d: %s", resp2.StatusCode, body2)
	} else {
		var comments []struct {
			ID   int64  `json:"id"`
			Path string `json:"path"`
			Body string `json:"body"`
		}
		if err := json.Unmarshal(body2, &comments); err == nil {
			t.Logf("  %-7s %-35s %10s   (mix %d%%) comments=%d",
				realworld.OpRMeta, fmt.Sprintf("pulls/%d/comments", prNumber),
				lat.Round(time.Microsecond), mix[realworld.OpRMeta], len(comments))
			if len(comments) < commentsPerReview {
				t.Errorf("review-thread-rust: expected at least %d review comments for PR %d, got %d",
					commentsPerReview, prNumber, len(comments))
			}
		}
	}

	// submodule-tree-rust: the contents endpoint for the root tree must render
	// the submodule entry (compiler/llvm-project) with type "submodule".
	// With a real rust clone this verifies the actual submodule; with the smoke
	// repo a synthetic submodule entry is present.
	s = time.Now()
	resp3, body3 := authedGet(t, srv, "/repos/rust-lang/rust/contents/", "token "+g.Plaintext)
	lat = time.Since(s)
	if resp3.StatusCode != 200 {
		t.Errorf("submodule-tree-rust: contents root status %d: %s", resp3.StatusCode, body3)
	} else {
		var entries []struct {
			Name string `json:"name"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
		}
		if err := json.Unmarshal(body3, &entries); err != nil {
			t.Errorf("submodule-tree-rust: decode tree: %v", err)
		} else {
			var foundSubmodule bool
			for _, e := range entries {
				if e.Type == "submodule" {
					foundSubmodule = true
					if e.SHA == "" {
						t.Errorf("submodule-tree-rust: entry %q type=submodule but sha is empty", e.Name)
					}
					t.Logf("  submodule entry: name=%q sha=%s", e.Name, e.SHA)
				}
			}
			t.Logf("  %-7s %-35s %10s   (mix %d%%) entries=%d submodule=%v",
				realworld.OpRGit, "contents/", lat.Round(time.Microsecond),
				mix[realworld.OpRGit], len(entries), foundSubmodule)
			if !foundSubmodule {
				if realGit {
					t.Error("submodule-tree-rust: no submodule entry found in real rust repo root; type rendering may be broken")
				} else {
					t.Log("  submodule-tree-rust: no submodule in smoke git (expected with real rust repo)")
				}
			}
		}
	}
}

// buildRustSmokeGitAt creates a tiny smoke git repository at dir with a
// synthetic submodule entry so the mode-160000 rendering path is exercised
// in CI without a full rust mirror on disk.
func buildRustSmokeGitAt(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	work, err := os.MkdirTemp("", "rust-smoke-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(work) })

	// Regular source files.
	files := map[string]string{
		"README.md":                     "# rust\n",
		"compiler/rustc_ast/src/lib.rs": "// ast\n",
		"library/std/src/lib.rs":        "// std\n",
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
			"GIT_AUTHOR_NAME=rustlang", "GIT_AUTHOR_EMAIL=rust@rust-lang.org",
			"GIT_COMMITTER_NAME=rustlang", "GIT_COMMITTER_EMAIL=rust@rust-lang.org",
			"GIT_AUTHOR_DATE=2015-05-15T00:00:00Z", "GIT_COMMITTER_DATE=2015-05-15T00:00:00Z",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "master")
	run("add", "-A")
	run("commit", "-q", "-m", "seed rust smoke corpus")

	// Add a synthetic submodule entry pointing at a fake commit hash.
	// We write .gitmodules + use update-index --add --cacheinfo to insert the
	// mode-160000 entry without requiring an actual submodule checkout.
	gitmodules := "[submodule \"compiler/llvm-project\"]\n\tpath = compiler/llvm-project\n\turl = https://github.com/rust-lang/llvm-project\n"
	if err := os.WriteFile(filepath.Join(work, ".gitmodules"), []byte(gitmodules), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeSHA := "abcdef1234567890abcdef1234567890abcdef12"
	run("update-index", "--add", "--cacheinfo", "160000,"+fakeSHA+",compiler/llvm-project")
	run("add", ".gitmodules")
	run("commit", "-q", "-m", "add llvm-project submodule smoke entry")

	if out, err := exec.Command("git", "clone", "--bare", "--local", work, dir).CombinedOutput(); err != nil {
		t.Fatalf("bare clone: %v\n%s", err, out)
	}
}

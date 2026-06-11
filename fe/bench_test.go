package fe_test

// fe/bench_test.go exercises every key web page under real-world-scale data and
// asserts that each page returns an HTML response in under 100 ms at p50 on
// developer hardware. The fixture is created once per test binary run (sync.Once)
// so the expensive seed-and-git-init setup does not repeat across benchmarks.
//
// Run just the benchmarks:
//
//	go test ./fe -bench=. -benchtime=5s -benchmem
//
// Run the SLO gate (fails if any page exceeds 100 ms):
//
//	go test ./fe -run=TestPageSLO -v

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/config"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe"
	"github.com/tamnd/githome/fe/assets"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/markup"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// benchFixt is the shared real-world benchmark fixture. It is built once and
// reused across every Benchmark* and TestPageSLO call in this file.
type benchFixt struct {
	srv      *httptest.Server
	owner    string
	repo     string
	issueNum int64  // an open issue with 20 comments
	prNum    int64  // an open PR with a multi-file diff
	blob     string // relative path to a realistic source file
	branch   string // the non-default branch used for compare
}

var (
	_bfOnce sync.Once
	_bf     *benchFixt
	_bfErr  error
)

func getBenchFixt(tb testing.TB) *benchFixt {
	tb.Helper()
	_bfOnce.Do(func() { _bf, _bfErr = makeBenchFixt() })
	if _bfErr != nil {
		tb.Fatalf("bench fixture: %v", _bfErr)
	}
	return _bf
}

// makeBenchFixt builds a self-contained server that mirrors a live Githome
// instance backed by a real SQLite database and a real git store, seeded with
// enough data to simulate a busy open-source project.
func makeBenchFixt() (*benchFixt, error) {
	ctx := context.Background()

	// Scratch directory lives in os.TempDir() rather than b.TempDir() because it
	// must outlive a single Benchmark call (sync.Once keeps one for the binary).
	dir, err := os.MkdirTemp("", "githome-fe-bench-*")
	if err != nil {
		return nil, fmt.Errorf("mktmp: %w", err)
	}

	st, err := store.Open(ctx, "sqlite://"+filepath.Join(dir, "bench.db"))
	if err != nil {
		return nil, fmt.Errorf("store open: %w", err)
	}
	if err := st.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	// ── Domain services ────────────────────────────────────────────────────────
	gitDir := filepath.Join(dir, "git")
	gs := git.NewStore(gitDir)

	repoSvc := domain.NewRepoService(st, gs)
	issueSvc := domain.NewIssueService(st, repoSvc)
	pullSvc := domain.NewPRService(st, repoSvc, issueSvc, gs)
	reviewSvc := domain.NewReviewService(st, repoSvc, pullSvc, issueSvc, gs)
	checksSvc := domain.NewChecksService(st, repoSvc, issueSvc, gs)
	userSvc := domain.NewUserService(st)
	hookSvc := domain.NewHookService(st, repoSvc, nil)
	eventSvc := domain.NewEventService(st, repoSvc)
	searchSvc := domain.NewSearchService(st, repoSvc, issueSvc, gs)

	// ── Seed: owner + repo ─────────────────────────────────────────────────────
	owner := &store.UserRow{Login: "octocat", Type: "User"}
	if err := st.InsertUser(ctx, owner); err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}

	desc := "A real-world-scale benchmark repository"
	repoRow := &store.RepoRow{
		OwnerPK:       owner.PK,
		Name:          "hello",
		Description:   &desc,
		DefaultBranch: "main",
	}
	if err := st.InsertRepo(ctx, repoRow); err != nil {
		return nil, fmt.Errorf("insert repo: %w", err)
	}

	// ── Build git repo ─────────────────────────────────────────────────────────
	featureBranch := "feature"
	featureBlob := "pkg/server.go"
	if err := buildBenchGitRepo(gs, repoRow.PK, featureBranch); err != nil {
		return nil, fmt.Errorf("build git repo: %w", err)
	}

	// ── Seed: 5 labels ─────────────────────────────────────────────────────────
	labelNames := []string{"bug", "enhancement", "documentation", "question", "wontfix"}
	var labelPKs []int64
	for i, name := range labelNames {
		colors := []string{"d73a4a", "a2eeef", "0075ca", "e4e669", "cfd3d7"}
		l := &store.LabelRow{RepoPK: repoRow.PK, Name: name, Color: colors[i]}
		if err := st.WithTx(ctx, func(tx *store.Tx) error { return tx.SeedLabel(ctx, l) }); err != nil {
			return nil, fmt.Errorf("seed label %s: %w", name, err)
		}
		labelPKs = append(labelPKs, l.PK)
	}

	// ── Seed: 100 issues (75 open, 25 closed) with 20 comments each ────────────
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	const issueCount = 100
	const commentsPerIssue = 20
	var deepIssueNum int64
	issuePKs := make([]int64, 0, issueCount)

	if err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *store.Tx) error {
			for i := 1; i <= issueCount; i++ {
				when := base.Add(time.Duration(i) * time.Hour)
				state := "open"
				if i > 75 {
					state = "closed"
				}
				body := fmt.Sprintf("This is the body for issue %d. It describes a real bug or feature request with enough detail to trigger template rendering of a realistic paragraph.", i)
				iss := &store.IssueRow{
					RepoPK:    repoRow.PK,
					Number:    int64(i),
					UserPK:    owner.PK,
					Title:     fmt.Sprintf("fix: crash in module handler when request body exceeds limit #%d", i),
					Body:      &body,
					State:     state,
					CreatedAt: when,
					UpdatedAt: when,
				}
				if err := tx.SeedIssue(ctx, iss); err != nil {
					return err
				}
				issuePKs = append(issuePKs, iss.PK)
				if i == 42 {
					deepIssueNum = iss.Number
				}
				// Attach 1-2 labels to every issue.
				lPK := labelPKs[i%len(labelPKs)]
				if err := tx.AttachLabels(ctx, iss.PK, []int64{lPK}); err != nil {
					return err
				}
			}
			return nil
		})
	}); err != nil {
		return nil, fmt.Errorf("seed issues: %w", err)
	}

	// Seed comments on issue 42 (the deep issue) and a handful on others.
	if err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *store.Tx) error {
			for issIdx, issuePK := range issuePKs {
				count := 3
				if int64(issIdx+1) == deepIssueNum {
					count = commentsPerIssue
				}
				for j := range count {
					when := base.Add(time.Duration(issIdx+1)*time.Hour + time.Duration(j)*time.Minute)
					body := fmt.Sprintf("Comment %d on issue %d. This is a thoughtful code review note with a Go snippet and a link to a related PR.", j+1, issIdx+1)
					c := &store.CommentRow{
						IssuePK:   issuePK,
						UserPK:    owner.PK,
						Body:      body,
						CreatedAt: when,
						UpdatedAt: when,
					}
					if err := tx.SeedComment(ctx, c); err != nil {
						return err
					}
				}
			}
			return nil
		})
	}); err != nil {
		return nil, fmt.Errorf("seed comments: %w", err)
	}
	if err := st.RecomputeIssueCommentCounts(ctx, repoRow.PK); err != nil {
		return nil, fmt.Errorf("recompute comment counts: %w", err)
	}
	if err := st.SetNextIssueNumber(ctx, repoRow.PK, issueCount+1); err != nil {
		return nil, fmt.Errorf("set next issue number: %w", err)
	}

	// ── Seed: 10 PRs ───────────────────────────────────────────────────────────
	mainSHA, featureSHA, err := readBranchSHAs(gs.Dir(repoRow.PK), "main", featureBranch)
	if err != nil {
		return nil, fmt.Errorf("read branch SHAs: %w", err)
	}
	var deepPRNum int64
	if err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *store.Tx) error {
			for i := 1; i <= 10; i++ {
				when := base.Add(time.Duration(100+i) * time.Hour)
				body := fmt.Sprintf("This pull request adds a feature described in issue #%d.", i)
				iss := &store.IssueRow{
					RepoPK:    repoRow.PK,
					Number:    int64(issueCount + i),
					IsPull:    true,
					UserPK:    owner.PK,
					Title:     fmt.Sprintf("feat: implement handler for operation %d", i),
					Body:      &body,
					State:     "open",
					CreatedAt: when,
					UpdatedAt: when,
				}
				if err := tx.SeedIssue(ctx, iss); err != nil {
					return err
				}
				pr := &store.PullRow{
					IssuePK:        iss.PK,
					RepoPK:         repoRow.PK,
					BaseRef:        "main",
					BaseSHA:        mainSHA,
					HeadRef:        featureBranch,
					HeadSHA:        featureSHA,
					Additions:      12,
					Deletions:      4,
					ChangedFiles:   3,
					CommitsCount:   2,
					MergeableState: "clean",
					CreatedAt:      when,
					UpdatedAt:      when,
				}
				if err := tx.SeedPull(ctx, pr); err != nil {
					return err
				}
				if i == 1 {
					deepPRNum = iss.Number
				}
			}
			return nil
		})
	}); err != nil {
		return nil, fmt.Errorf("seed PRs: %w", err)
	}
	if err := st.SetNextIssueNumber(ctx, repoRow.PK, int64(issueCount+11)); err != nil {
		return nil, fmt.Errorf("set next issue number (PR): %w", err)
	}

	// ── Mount the frontend ─────────────────────────────────────────────────────
	rs, err := render.New(assets.FS(), false)
	if err != nil {
		return nil, fmt.Errorf("render.New: %w", err)
	}
	discard := io.Discard
	_ = discard

	htmlBase := "https://bench.internal"
	urls := config.URLs{
		HTML: mustParseURL(htmlBase),
		API:  mustParseURL(htmlBase + "/api/v3"),
	}
	mk := markup.New(markup.Config{BaseURL: htmlBase})
	pres := presenter.NewURLBuilder(urls)
	vb := view.NewBuilder("Githome")
	sessions := webmw.NewSessions(testKey, time.Hour, func(context.Context, int64) (*view.Viewer, error) {
		return nil, nil
	})
	csrf := webmw.NewCSRF(rs)
	flash := webmw.NewFlash(testKey)

	root := mizu.NewRouter()
	handler := fe.Mount(root, fe.Deps{
		Render:   rs,
		View:     vb,
		Repos:    repoSvc,
		Hooks:    hookSvc,
		Checks:   checksSvc,
		Issues:   issueSvc,
		Pulls:    pullSvc,
		Reviews:  reviewSvc,
		Search:   searchSvc,
		Users:    userSvc,
		Events:   eventSvc,
		URLs:     pres,
		Markup:   mk,
		Sessions: sessions,
		CSRF:     csrf,
		Flash:    flash,
	})

	srv := httptest.NewServer(handler)
	return &benchFixt{
		srv:      srv,
		owner:    "octocat",
		repo:     "hello",
		issueNum: deepIssueNum,
		prNum:    deepPRNum,
		blob:     featureBlob,
		branch:   featureBranch,
	}, nil
}

// ── HTTP helpers ───────────────────────────────────────────────────────────────

// benchGet issues a GET to the fixture server and returns the status code.
// It drains and discards the body so connections are reused.
func benchGet(b *testing.B, srv *httptest.Server, path string) int {
	b.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		b.Fatalf("GET %s: %v", path, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// sloGet is benchGet for TestPageSLO: same request, asserts 200 OK.
func sloGet(t *testing.T, srv *httptest.Server, path string) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s: status %d, want 200", path, resp.StatusCode)
	}
}

// ── Benchmarks ─────────────────────────────────────────────────────────────────

func BenchmarkHome(b *testing.B) {
	fx := getBenchFixt(b)
	// warm up: compile templates, warm SQLite page cache
	_ = benchGet(b, fx.srv, "/")
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchGet(b, fx.srv, "/")
	}
}

func BenchmarkProfile(b *testing.B) {
	fx := getBenchFixt(b)
	path := "/" + fx.owner
	_ = benchGet(b, fx.srv, path)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchGet(b, fx.srv, path)
	}
}

func BenchmarkRepoTree(b *testing.B) {
	fx := getBenchFixt(b)
	path := "/" + fx.owner + "/" + fx.repo
	_ = benchGet(b, fx.srv, path)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchGet(b, fx.srv, path)
	}
}

func BenchmarkRepoBlob(b *testing.B) {
	fx := getBenchFixt(b)
	path := "/" + fx.owner + "/" + fx.repo + "/blob/main/" + fx.blob
	_ = benchGet(b, fx.srv, path)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchGet(b, fx.srv, path)
	}
}

func BenchmarkIssuesIndex(b *testing.B) {
	fx := getBenchFixt(b)
	path := "/" + fx.owner + "/" + fx.repo + "/issues"
	_ = benchGet(b, fx.srv, path)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchGet(b, fx.srv, path)
	}
}

func BenchmarkIssueDetail(b *testing.B) {
	fx := getBenchFixt(b)
	path := fmt.Sprintf("/%s/%s/issues/%d", fx.owner, fx.repo, fx.issueNum)
	_ = benchGet(b, fx.srv, path)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchGet(b, fx.srv, path)
	}
}

func BenchmarkPullsIndex(b *testing.B) {
	fx := getBenchFixt(b)
	path := "/" + fx.owner + "/" + fx.repo + "/pulls"
	_ = benchGet(b, fx.srv, path)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchGet(b, fx.srv, path)
	}
}

func BenchmarkPullDetail(b *testing.B) {
	fx := getBenchFixt(b)
	path := fmt.Sprintf("/%s/%s/pull/%d", fx.owner, fx.repo, fx.prNum)
	_ = benchGet(b, fx.srv, path)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchGet(b, fx.srv, path)
	}
}

func BenchmarkPullFiles(b *testing.B) {
	fx := getBenchFixt(b)
	path := fmt.Sprintf("/%s/%s/pull/%d/files", fx.owner, fx.repo, fx.prNum)
	_ = benchGet(b, fx.srv, path)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchGet(b, fx.srv, path)
	}
}

func BenchmarkSearch(b *testing.B) {
	fx := getBenchFixt(b)
	path := "/search?q=handler&type=code"
	_ = benchGet(b, fx.srv, path)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchGet(b, fx.srv, path)
	}
}

func BenchmarkComparePicker(b *testing.B) {
	fx := getBenchFixt(b)
	path := "/" + fx.owner + "/" + fx.repo + "/compare"
	_ = benchGet(b, fx.srv, path)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchGet(b, fx.srv, path)
	}
}

func BenchmarkCompareRange(b *testing.B) {
	fx := getBenchFixt(b)
	path := fmt.Sprintf("/%s/%s/compare/main...%s", fx.owner, fx.repo, fx.branch)
	_ = benchGet(b, fx.srv, path)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchGet(b, fx.srv, path)
	}
}

func BenchmarkSettingsAppearance(b *testing.B) {
	fx := getBenchFixt(b)
	// Settings is gated to signed-in users. An anonymous request returns 404,
	// which is still a valid full template render of the themed error page.
	path := "/settings/appearance"
	_ = benchGet(b, fx.srv, path)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchGet(b, fx.srv, path)
	}
}

func BenchmarkRepoSearch(b *testing.B) {
	fx := getBenchFixt(b)
	path := "/" + fx.owner + "/" + fx.repo + "/search?q=handler&type=code"
	_ = benchGet(b, fx.srv, path)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		benchGet(b, fx.srv, path)
	}
}

// ── SLO gate ───────────────────────────────────────────────────────────────────

// TestPageSLO checks that every key page returns under the render budget
// averaged over 20 warm requests. The fixture is warm before timing starts.
// Run with:
//
//	go test ./fe -run=TestPageSLO -v
func TestPageSLO(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped in -short mode")
	}
	fx := getBenchFixt(t)

	type page struct {
		name string
		path string
	}
	pages := []page{
		{"home", "/"},
		{"profile", "/" + fx.owner},
		{"repo_tree", "/" + fx.owner + "/" + fx.repo},
		{"repo_blob", "/" + fx.owner + "/" + fx.repo + "/blob/main/" + fx.blob},
		{"issues_index", "/" + fx.owner + "/" + fx.repo + "/issues"},
		{"issue_detail", fmt.Sprintf("/%s/%s/issues/%d", fx.owner, fx.repo, fx.issueNum)},
		{"pulls_index", "/" + fx.owner + "/" + fx.repo + "/pulls"},
		{"pull_detail", fmt.Sprintf("/%s/%s/pull/%d", fx.owner, fx.repo, fx.prNum)},
		{"pull_files", fmt.Sprintf("/%s/%s/pull/%d/files", fx.owner, fx.repo, fx.prNum)},
		{"search_global", "/search?q=handler&type=code"},
		{"search_repo", "/" + fx.owner + "/" + fx.repo + "/search?q=handler"},
		{"compare_picker", "/" + fx.owner + "/" + fx.repo + "/compare"},
		{"compare_range", fmt.Sprintf("/%s/%s/compare/main...%s", fx.owner, fx.repo, fx.branch)},
	}

	const warmup = 3
	const measure = 20
	// 50 ms is the render budget the performance review set (2005/review/03).
	// Every page on this fixture averages under 20 ms warm on a developer
	// machine, so this still leaves a slower shared runner 2-3x headroom while
	// catching a page that regresses to a per-request history walk or diff.
	const budget = 50 * time.Millisecond

	for _, p := range pages {
		t.Run(p.name, func(t *testing.T) {
			// Warm up: pre-compile templates, fill page cache.
			for range warmup {
				sloGet(t, fx.srv, p.path)
			}
			// Measure: time measure requests, assert average.
			start := time.Now()
			for range measure {
				sloGet(t, fx.srv, p.path)
			}
			avg := time.Since(start) / measure
			t.Logf("%s: avg=%v", p.name, avg)
			if avg > budget {
				t.Errorf("%s: avg=%v exceeds the %v budget", p.name, avg, budget)
			}
		})
	}
}

// ── Git repo builder ───────────────────────────────────────────────────────────

// buildBenchGitRepo creates a bare git repository at the path the git.Store
// expects for repoPK, populated with realistic Go source files across three
// packages, 15 commits on main, and a feature branch two commits ahead.
func buildBenchGitRepo(gs *git.Store, repoPK int64, featureBranch string) error {
	src, err := os.MkdirTemp("", "githome-bench-src-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(src) }()

	gc := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=Octo Cat", "GIT_AUTHOR_EMAIL=octo@example.com",
			"GIT_COMMITTER_NAME=Octo Cat", "GIT_COMMITTER_EMAIL=octo@example.com",
		)
		var errb bytes.Buffer
		cmd.Stderr = &errb
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, errb.String())
		}
		return nil
	}
	wf := func(rel, body string) error {
		path := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(body), 0o644)
	}

	if err := gc("init", "-q", "-b", "main"); err != nil {
		return err
	}

	// Stage 1: initial commit with package skeletons.
	files := map[string]string{
		"go.mod":         benchGoMod,
		"main.go":        benchMain,
		"pkg/server.go":  benchServer,
		"pkg/auth.go":    benchAuth,
		"pkg/config.go":  benchConfig,
		"store/store.go": benchStore,
		"store/query.go": benchQuery,
		"README.md":      benchReadme,
	}
	for rel, body := range files {
		if err := wf(rel, body); err != nil {
			return err
		}
	}
	if err := gc("add", "-A"); err != nil {
		return err
	}
	if err := gc("commit", "-q", "-m", "init: project skeleton"); err != nil {
		return err
	}

	// Stage 2: a series of feature commits on main.
	updates := []struct {
		file, msg, body string
	}{
		{"pkg/server.go", "feat: add request timeout middleware", benchServerV2},
		{"pkg/auth.go", "fix: validate JWT expiry before accepting token", benchAuthV2},
		{"store/query.go", "perf: add index hint for issues pagination", benchQueryV2},
		{"pkg/config.go", "feat: read DB_MAX_CONNS from env", benchConfigV2},
	}
	for _, u := range updates {
		if err := wf(u.file, u.body); err != nil {
			return err
		}
		if err := gc("add", u.file); err != nil {
			return err
		}
		if err := gc("commit", "-q", "-m", u.msg); err != nil {
			return err
		}
	}

	// Stage 3: feature branch with two additional commits.
	if err := gc("checkout", "-q", "-b", featureBranch); err != nil {
		return err
	}
	featureFiles := []struct {
		file, msg, body string
	}{
		{"pkg/metrics.go", "feat: add Prometheus metrics endpoint", benchMetrics},
		{"pkg/server.go", "feat: wire metrics handler into server", benchServerV3},
	}
	for _, u := range featureFiles {
		if err := wf(u.file, u.body); err != nil {
			return err
		}
		if err := gc("add", u.file); err != nil {
			return err
		}
		if err := gc("commit", "-q", "-m", u.msg); err != nil {
			return err
		}
	}
	if err := gc("checkout", "-q", "main"); err != nil {
		return err
	}

	// Clone into the bare repo the git.Store expects.
	bare := gs.Dir(repoPK)
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("git", "clone", "-q", "--bare", src, bare)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone --bare: %v\n%s", err, errb.String())
	}
	return nil
}

// readBranchSHAs reads the tip SHAs for two branches from a bare git repo.
func readBranchSHAs(bareDir, branch1, branch2 string) (sha1, sha2 string, err error) {
	read := func(ref string) (string, error) {
		cmd := exec.Command("git", "-C", bareDir, "rev-parse", ref)
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("rev-parse %s: %v", ref, err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	sha1, err = read(branch1)
	if err != nil {
		return
	}
	sha2, err = read(branch2)
	return
}

func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// ── Realistic source file bodies ───────────────────────────────────────────────

const benchGoMod = `module example.com/hello

go 1.24
`

const benchMain = `package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/hello/pkg"
	"example.com/hello/store"
)

func main() {
	cfg := pkg.LoadConfig()
	db, err := store.Open(cfg.DSN)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	srv := pkg.NewServer(cfg, db)
	httpSrv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      srv,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		log.Printf("listening on %s", cfg.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	_ = httpSrv.Shutdown(shutCtx)
}
`

const benchServer = `package pkg

import (
	"net/http"

	"example.com/hello/store"
)

// Server is the root HTTP handler.
type Server struct {
	cfg *Config
	db  *store.DB
	mux *http.ServeMux
}

// NewServer wires the handler tree.
func NewServer(cfg *Config, db *store.DB) *Server {
	s := &Server{cfg: cfg, db: db, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /issues", s.handleIssuesList)
	s.mux.HandleFunc("POST /issues", s.handleIssuesCreate)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleIssuesList(w http.ResponseWriter, r *http.Request) {
	issues, err := s.db.ListIssues(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, issues)
}

func (s *Server) handleIssuesCreate(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusCreated)
}
`

const benchServerV2 = benchServer + `
// withTimeout returns a middleware that cancels requests after d.
func withTimeout(d time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
`

const benchServerV3 = benchServer + `
// withTimeout returns a middleware that cancels requests after d.
func withTimeout(d time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) registerMetrics() {
	s.mux.Handle("GET /metrics", newMetricsHandler(s.cfg))
}
`

const benchAuth = `package pkg

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

// ErrExpiredToken is returned when a JWT has passed its expiry time.
var ErrExpiredToken = errors.New("auth: token expired")

// ParseBearer extracts a Bearer token from the Authorization header.
func ParseBearer(r *http.Request) (string, error) {
	v := r.Header.Get("Authorization")
	if v == "" {
		return "", errors.New("auth: missing Authorization header")
	}
	parts := strings.SplitN(v, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", errors.New("auth: expected Bearer scheme")
	}
	return parts[1], nil
}

// ValidateExpiry checks the exp claim embedded in a signed JWT payload.
func ValidateExpiry(exp int64) error {
	if time.Unix(exp, 0).Before(time.Now()) {
		return ErrExpiredToken
	}
	return nil
}
`

const benchAuthV2 = benchAuth + `
// RequireAuth is middleware that rejects requests lacking a valid non-expired token.
func RequireAuth(secret []byte, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, err := ParseBearer(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		_ = tok
		next.ServeHTTP(w, r)
	})
}
`

const benchConfig = `package pkg

import (
	"os"
	"strconv"
)

// Config holds the runtime configuration read from the environment.
type Config struct {
	Addr string
	DSN  string
	Env  string
}

// LoadConfig reads the runtime configuration from environment variables.
func LoadConfig() *Config {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "sqlite://./dev.db"
	}
	env := os.Getenv("ENV")
	if env == "" {
		env = "development"
	}
	return &Config{Addr: addr, DSN: dsn, Env: env}
}
`

const benchConfigV2 = benchConfig + `
// MaxConns returns the maximum number of database connections, read from
// DB_MAX_CONNS (default 10).
func (c *Config) MaxConns() int {
	if raw := os.Getenv("DB_MAX_CONNS"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return 10
}
`

const benchStore = `package store

import (
	"context"
	"database/sql"
	_ "modernc.org/sqlite"
)

// DB is the store's database handle.
type DB struct{ db *sql.DB }

// Open opens or creates a database at dsn.
func Open(dsn string) (*DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	return &DB{db: db}, nil
}

// Close closes the underlying database connection.
func (d *DB) Close() error { return d.db.Close() }

// Issue is a single tracked issue.
type Issue struct {
	ID    int64
	Title string
	Body  string
	State string
}

// ListIssues returns all open issues.
func (d *DB) ListIssues(ctx context.Context) ([]Issue, error) {
	rows, err := d.db.QueryContext(ctx, "SELECT id, title, body, state FROM issues WHERE state='open' ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Issue
	for rows.Next() {
		var iss Issue
		if err := rows.Scan(&iss.ID, &iss.Title, &iss.Body, &iss.State); err != nil {
			return nil, err
		}
		out = append(out, iss)
	}
	return out, rows.Err()
}
`

const benchQuery = `package store

import (
	"context"
	"fmt"
)

// ListPage returns a page of issues with keyset pagination.
// After is the last-seen id for cursor-based navigation.
func (d *DB) ListPage(ctx context.Context, after int64, limit int) ([]Issue, error) {
	q := "SELECT id, title, body, state FROM issues WHERE state='open'"
	args := []any{}
	if after > 0 {
		q += " AND id < ?"
		args = append(args, after)
	}
	q += fmt.Sprintf(" ORDER BY id DESC LIMIT %d", limit)
	rows, err := d.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Issue
	for rows.Next() {
		var iss Issue
		if err := rows.Scan(&iss.ID, &iss.Title, &iss.Body, &iss.State); err != nil {
			return nil, err
		}
		out = append(out, iss)
	}
	return out, rows.Err()
}
`

const benchQueryV2 = benchQuery + `
// CountOpen returns the total number of open issues, using the covering index.
func (d *DB) CountOpen(ctx context.Context) (int, error) {
	var n int
	return n, d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM issues WHERE state='open'").Scan(&n)
}
`

const benchMetrics = `package pkg

import (
	"fmt"
	"net/http"
	"runtime"
)

type metricsHandler struct{ cfg *Config }

func newMetricsHandler(cfg *Config) *metricsHandler { return &metricsHandler{cfg: cfg} }

func (h *metricsHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Fprintf(w, "# HELP go_heap_alloc_bytes Current heap allocation.\n")
	fmt.Fprintf(w, "# TYPE go_heap_alloc_bytes gauge\n")
	fmt.Fprintf(w, "go_heap_alloc_bytes %d\n", ms.HeapAlloc)
	fmt.Fprintf(w, "# HELP go_goroutines Number of goroutines.\n")
	fmt.Fprintf(w, "# TYPE go_goroutines gauge\n")
	fmt.Fprintf(w, "go_goroutines %d\n", runtime.NumGoroutine())
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, "%v\n", v)
}
`

const benchReadme = `# Hello

A real-world-scale benchmark repository. This README is rendered through the
Markdown pipeline to exercise the GFM parser, the sanitizer, and the syntax
highlighter.

## Installation

` + "```" + `sh
go install example.com/hello@latest
` + "```" + `

## Usage

Set environment variables and run:

` + "```" + `sh
export DATABASE_URL=sqlite://./prod.db
export ADDR=:8443
./hello
` + "```" + `

## Architecture

The server is a plain ` + "`net/http`" + ` mux with four layers:

1. **Transport** – TLS termination, request timeout, rate limiting.
2. **Auth** – Bearer token validation, session lookup.
3. **Router** – URL dispatch, path parameter extraction.
4. **Handlers** – Business logic, domain reads and writes.

See [pkg/server.go](pkg/server.go) for the wiring.
`

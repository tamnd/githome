package rest

// HTTP-layer stress benchmarks.
//
// Each benchmark starts a real in-process httptest.Server backed by a seeded
// SQLite store and fires HTTP requests against it in the hot loop.
//
// Scenarios:
//
//   - BenchmarkIssueList_HTTP              — GET /issues, 8 parallel readers
//   - BenchmarkIssueList_HTTP_withLabels   — 10 labels per issue (batch hydration)
//   - BenchmarkIssueList_HTTP_withMilestone — milestone on all issues (batch counts)
//   - BenchmarkIssueList_HTTP_deepPage     — page 50 (OFFSET 1470)
//   - BenchmarkIssueGet_HTTP               — GET /issues/1, 8 parallel readers
//   - BenchmarkIssueGet_304_vs_200         — conditional vs fresh GET latency
//   - BenchmarkIssueCreate_HTTP_sequential — write throughput, no contention
//   - BenchmarkIssueCreate_HTTP_concurrent_8 — 8-way concurrent writes
//   - BenchmarkSearch_HTTP                 — /search/issues?q=user:octocat
//   - BenchmarkSearch_HTTP_compoundQuery   — label+author+state compound qualifier
//   - BenchmarkIssueComments_HTTP          — /issues/1/comments with 100 comments
//   - BenchmarkMilestoneList_HTTP          — /milestones with 1 K-issue milestone

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// drainClose reads the response body to completion and closes it so the
// underlying TCP connection is returned to the transport's keep-alive pool.
// Calling resp.Body.Close() without reading leaves data in flight; Go's
// net/http then cannot reuse the connection, creating a new TCP connection
// per request and exhausting the ephemeral-port range under high parallelism.
func drainClose(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// newPooledClient returns one http.Client with a shared transport.
// All parallel goroutines must share this one client so keep-alive reuse
// prevents ephemeral-port exhaustion under high parallelism.
func newPooledClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		MaxIdleConnsPerHost: 128,
		DisableKeepAlives:   false,
	}}
}

// httpStressEnv builds a REST server and seeds it with the given corpus
// parameters by writing directly to the store (bypassing HTTP to keep setup
// fast).
//
//   nIssues        — open issues to create
//   commentsOnFirst — comments to add to issue #1
//   labelsPerIssue  — labels attached to every issue (0 = none)
//   withMilestone   — link all issues to a milestone named "v1.0"
func httpStressEnv(b *testing.B, nIssues, commentsOnFirst, labelsPerIssue int, withMilestone bool) issueFixture {
	b.Helper()
	fx := issueServer(b)
	st := fx.st
	tok := fx.token
	ctx := context.Background()

	// Labels via HTTP so the REST path (including FTS index) is populated.
	var labelPKs []int64
	for i := range labelsPerIssue {
		name := fmt.Sprintf("label-%02d", i)
		resp, body := authedSend(b, fx.srv, http.MethodPost, "/repos/octocat/hello/labels", tok,
			fmt.Sprintf(`{"name":%q,"color":"d73a4a"}`, name))
		if resp.StatusCode != http.StatusCreated {
			b.Fatalf("create label %s: %d: %s", name, resp.StatusCode, body)
		}
		l, err := st.GetLabel(ctx, resolveRepoPK(b, st), name)
		if err != nil {
			b.Fatalf("GetLabel %s: %v", name, err)
		}
		labelPKs = append(labelPKs, l.PK)
	}

	// Milestone via HTTP.
	var milestonePK *int64
	if withMilestone {
		resp, body := authedSend(b, fx.srv, http.MethodPost, "/repos/octocat/hello/milestones", tok,
			`{"title":"v1.0"}`)
		if resp.StatusCode != http.StatusCreated {
			b.Fatalf("create milestone: %d: %s", resp.StatusCode, body)
		}
		var ms struct {
			Number int64 `json:"number"`
		}
		_ = json.Unmarshal(body, &ms)
		repoPK := resolveRepoPK(b, st)
		mr, err := st.GetMilestoneByNumber(ctx, repoPK, ms.Number)
		if err != nil {
			b.Fatalf("GetMilestoneByNumber: %v", err)
		}
		milestonePK = &mr.PK
	}

	owner := resolveOwner(b, st)
	repoPK := resolveRepoPK(b, st)
	base := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)

	// Issues: seeded directly through the store for speed.
	if err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *store.Tx) error {
			for i := 1; i <= nIssues; i++ {
				when := base.Add(time.Duration(i) * time.Minute)
				iss := &store.IssueRow{
					RepoPK:      repoPK,
					Number:      int64(i),
					UserPK:      owner.PK,
					Title:       fmt.Sprintf("stress issue %d: fix the crash in module bar", i),
					State:       "open",
					MilestonePK: milestonePK,
					CreatedAt:   when,
					UpdatedAt:   when,
				}
				if err := tx.SeedIssue(ctx, iss); err != nil {
					return err
				}
				if len(labelPKs) > 0 {
					if err := tx.AttachLabels(ctx, iss.PK, labelPKs); err != nil {
						return err
					}
				}
			}
			return nil
		})
	}); err != nil {
		b.Fatalf("seed issues: %v", err)
	}
	if err := st.SetNextIssueNumber(ctx, repoPK, int64(nIssues+1)); err != nil {
		b.Fatalf("SetNextIssueNumber: %v", err)
	}

	// Comments on issue #1.
	if commentsOnFirst > 0 {
		iss1, err := st.GetIssueByNumber(ctx, repoPK, 1)
		if err != nil {
			b.Fatalf("GetIssueByNumber(1): %v", err)
		}
		if err := st.BulkLoad(ctx, func() error {
			return st.WithTx(ctx, func(tx *store.Tx) error {
				for i := 1; i <= commentsOnFirst; i++ {
					c := &store.CommentRow{
						IssuePK:   iss1.PK,
						UserPK:    owner.PK,
						Body:      fmt.Sprintf("comment %d with some discussion context", i),
						CreatedAt: base.Add(time.Duration(i) * time.Second),
						UpdatedAt: base.Add(time.Duration(i) * time.Second),
					}
					if err := tx.SeedComment(ctx, c); err != nil {
						return err
					}
				}
				return nil
			})
		}); err != nil {
			b.Fatalf("seed comments: %v", err)
		}
	}

	return fx
}

// searchStressEnv builds a full server with the Search service wired and seeds
// nIssues via HTTP POST so the FTS index is populated. Labels (if any) are
// created first, then each issue is posted with those labels attached.
func searchStressEnv(b *testing.B, nIssues, labelsPerIssue int) issueFixture {
	b.Helper()
	ctx := context.Background()

	st, err := store.Open(ctx, "sqlite://"+filepath.Join(b.TempDir(), "githome.db"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	b.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		b.Fatalf("migrate: %v", err)
	}

	u := &store.UserRow{Login: "octocat", Type: "User"}
	if err := st.InsertUser(ctx, u); err != nil {
		b.Fatalf("insert user: %v", err)
	}
	g, err := auth.GenerateToken(auth.PrefixClassicPAT)
	if err != nil {
		b.Fatal(err)
	}
	hash := g.Hash
	if err := st.InsertToken(ctx, &store.TokenRow{
		UserPK: &u.PK, TokenHash: hash[:], TokenPrefix: auth.PrefixClassicPAT,
		LastEight: g.Last8, Kind: "pat", Scopes: "repo",
	}); err != nil {
		b.Fatalf("insert token: %v", err)
	}
	repo := &store.RepoRow{OwnerPK: u.PK, Name: "hello", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, repo); err != nil {
		b.Fatalf("insert repo: %v", err)
	}

	authSvc := auth.NewService(st, "https://git.test.internal")
	b.Cleanup(authSvc.Close)
	cfg := authConfig(b)
	gitStore := git.NewStore(b.TempDir())
	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	root := mizu.NewRouter()
	Mount(root, Deps{
		Config:     cfg,
		Ready:      st,
		Auth:       authSvc,
		Users:      domain.NewUserService(st),
		Repos:      repoSvc,
		Issues:     issueSvc,
		Search:     domain.NewSearchService(st, repoSvc, issueSvc, gitStore),
		URLs:       presenter.NewURLBuilder(cfg.URLs),
		NodeFormat: nodeid.FormatNew,
	})
	srv := httptest.NewServer(root)
	b.Cleanup(srv.Close)

	fx := issueFixture{srv: srv, token: g.Plaintext, st: st}
	tok := g.Plaintext

	// Create labels first.
	var labelNames []string
	for i := range labelsPerIssue {
		name := fmt.Sprintf("label-%02d", i)
		resp, body := authedSend(b, srv, http.MethodPost, "/repos/octocat/hello/labels", tok,
			fmt.Sprintf(`{"name":%q,"color":"d73a4a"}`, name))
		if resp.StatusCode != http.StatusCreated {
			b.Fatalf("create label %s: %d: %s", name, resp.StatusCode, body)
		}
		labelNames = append(labelNames, name)
	}

	// Seed issues via HTTP so each INSERT fires the FTS trigger.
	labelsJSON := ""
	if len(labelNames) > 0 {
		parts := make([]string, len(labelNames))
		for i, n := range labelNames {
			parts[i] = `"` + n + `"`
		}
		labelsJSON = `,"labels":[` + strings.Join(parts, ",") + `]`
	}
	for i := 1; i <= nIssues; i++ {
		body := fmt.Sprintf(`{"title":"stress issue %d: fix crash in module bar"%s}`, i, labelsJSON)
		resp, out := authedSend(b, srv, http.MethodPost, "/repos/octocat/hello/issues", tok, body)
		if resp.StatusCode != http.StatusCreated {
			b.Fatalf("seed issue %d: %d: %s", i, resp.StatusCode, out)
		}
	}
	return fx
}

func resolveOwner(b *testing.B, st *store.Store) *store.UserRow {
	b.Helper()
	u, err := st.UserByLogin(context.Background(), "octocat")
	if err != nil {
		b.Fatalf("UserByLogin: %v", err)
	}
	return u
}

func resolveRepoPK(b *testing.B, st *store.Store) int64 {
	b.Helper()
	r, err := st.RepoByOwnerName(context.Background(), "octocat", "hello")
	if err != nil {
		b.Fatalf("RepoByOwnerName: %v", err)
	}
	return r.PK
}

// ── parallel reads ────────────────────────────────────────────────────────────

// BenchmarkIssueList_HTTP measures GET /repos/octocat/hello/issues under 8
// parallel readers. This is the dominant workload class for read-heavy repos.
func BenchmarkIssueList_HTTP(b *testing.B) {
	fx := httpStressEnv(b, 500, 0, 0, false)
	url := fx.srv.URL + "/repos/octocat/hello/issues?per_page=30&state=open"
	tok := "token " + fx.token

	client := newPooledClient()
	b.SetParallelism(8)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest(http.MethodGet, url, nil)
			req.Header.Set("Authorization", tok)
			resp, err := client.Do(req)
			if err != nil {
				b.Fatal(err)
			}
			drainClose(resp)
			if resp.StatusCode != http.StatusOK {
				b.Fatalf("want 200, got %d", resp.StatusCode)
			}
		}
	})
}

// BenchmarkIssueList_HTTP_withLabels measures the issue list when every issue
// carries 10 labels, exercising the LabelsByIssuePKs batch path end-to-end.
func BenchmarkIssueList_HTTP_withLabels(b *testing.B) {
	fx := httpStressEnv(b, 300, 0, 10, false)
	url := fx.srv.URL + "/repos/octocat/hello/issues?per_page=30&state=open"
	tok := "token " + fx.token

	client := newPooledClient()
	b.SetParallelism(4)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest(http.MethodGet, url, nil)
			req.Header.Set("Authorization", tok)
			resp, err := client.Do(req)
			if err != nil {
				b.Fatal(err)
			}
			drainClose(resp)
			if resp.StatusCode != http.StatusOK {
				b.Fatalf("want 200, got %d", resp.StatusCode)
			}
		}
	})
}

// BenchmarkIssueList_HTTP_withMilestone measures the issue list where all issues
// share one milestone, exercising the MilestoneIssueCountsByPKs batch path.
func BenchmarkIssueList_HTTP_withMilestone(b *testing.B) {
	fx := httpStressEnv(b, 300, 0, 0, true)
	url := fx.srv.URL + "/repos/octocat/hello/issues?per_page=30&state=open"
	tok := "token " + fx.token

	client := newPooledClient()
	b.SetParallelism(4)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest(http.MethodGet, url, nil)
			req.Header.Set("Authorization", tok)
			resp, err := client.Do(req)
			if err != nil {
				b.Fatal(err)
			}
			drainClose(resp)
			if resp.StatusCode != http.StatusOK {
				b.Fatalf("want 200, got %d", resp.StatusCode)
			}
		}
	})
}

// BenchmarkIssueList_HTTP_deepPage measures page 50 (OFFSET 1 470) of a
// 2 000-issue repo to surface the OFFSET pagination regression end-to-end.
func BenchmarkIssueList_HTTP_deepPage(b *testing.B) {
	fx := httpStressEnv(b, 2_000, 0, 0, false)
	url := fx.srv.URL + "/repos/octocat/hello/issues?per_page=30&state=open&page=50"
	tok := "token " + fx.token
	client := newPooledClient()

	b.ResetTimer()
	for b.Loop() {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("Authorization", tok)
		resp, err := client.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		drainClose(resp)
		if resp.StatusCode != http.StatusOK {
			b.Fatalf("want 200, got %d", resp.StatusCode)
		}
	}
}

// ── single-issue GET ──────────────────────────────────────────────────────────

// BenchmarkIssueGet_HTTP measures GET /issues/1 under 8 parallel readers.
func BenchmarkIssueGet_HTTP(b *testing.B) {
	fx := httpStressEnv(b, 100, 0, 0, false)
	url := fx.srv.URL + "/repos/octocat/hello/issues/1"
	tok := "token " + fx.token

	client := newPooledClient()
	b.SetParallelism(8)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest(http.MethodGet, url, nil)
			req.Header.Set("Authorization", tok)
			resp, err := client.Do(req)
			if err != nil {
				b.Fatal(err)
			}
			drainClose(resp)
			if resp.StatusCode != http.StatusOK {
				b.Fatalf("want 200, got %d", resp.StatusCode)
			}
		}
	})
}

// BenchmarkIssueGet_304_vs_200 compares conditional GET (304) against a fresh
// GET (200) to quantify the version-ETag savings.
func BenchmarkIssueGet_304_vs_200(b *testing.B) {
	fx := httpStressEnv(b, 10, 0, 0, false)
	url := fx.srv.URL + "/repos/octocat/hello/issues/1"
	tok := "token " + fx.token

	client := newPooledClient()
	req0, _ := http.NewRequest(http.MethodGet, url, nil)
	req0.Header.Set("Authorization", tok)
	resp0, err := client.Do(req0)
	if err != nil {
		b.Fatalf("first GET: %v", err)
	}
	drainClose(resp0)
	etag := resp0.Header.Get("ETag")
	if etag == "" {
		b.Fatal("no ETag on first GET")
	}

	b.Run("200-fresh", func(b *testing.B) {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				req, _ := http.NewRequest(http.MethodGet, url, nil)
				req.Header.Set("Authorization", tok)
				resp, err := client.Do(req)
				if err != nil {
					b.Fatal(err)
				}
				drainClose(resp)
			}
		})
	})

	b.Run("304-conditional", func(b *testing.B) {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				req, _ := http.NewRequest(http.MethodGet, url, nil)
				req.Header.Set("Authorization", tok)
				req.Header.Set("If-None-Match", etag)
				resp, err := client.Do(req)
				if err != nil {
					b.Fatal(err)
				}
				drainClose(resp)
				if resp.StatusCode != http.StatusNotModified {
					b.Fatalf("want 304, got %d", resp.StatusCode)
				}
			}
		})
	})
}

// ── write throughput ──────────────────────────────────────────────────────────

// BenchmarkIssueCreate_HTTP_sequential measures sequential POST throughput
// through the full handler stack.
func BenchmarkIssueCreate_HTTP_sequential(b *testing.B) {
	fx := issueServer(b)
	tok := fx.token
	var counter atomic.Int64

	b.ResetTimer()
	for b.Loop() {
		n := counter.Add(1)
		resp, body := authedSend(b, fx.srv, http.MethodPost, "/repos/octocat/hello/issues",
			tok, fmt.Sprintf(`{"title":"seq issue %d"}`, n))
		if resp.StatusCode != http.StatusCreated {
			b.Fatalf("want 201, got %d: %s", resp.StatusCode, body)
		}
	}
}

// BenchmarkIssueCreate_HTTP_concurrent_8 measures 8-way concurrent POST.
func BenchmarkIssueCreate_HTTP_concurrent_8(b *testing.B) {
	fx := issueServer(b)
	tok := fx.token
	var counter atomic.Int64

	b.SetParallelism(8)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := counter.Add(1)
			resp, body := authedSend(b, fx.srv, http.MethodPost, "/repos/octocat/hello/issues",
				tok, fmt.Sprintf(`{"title":"concurrent issue %d"}`, n))
			if resp.StatusCode != http.StatusCreated {
				b.Fatalf("want 201, got %d: %s", resp.StatusCode, body)
			}
		}
	})
}

// ── search ────────────────────────────────────────────────────────────────────

// BenchmarkSearch_HTTP measures /search/issues?q=user:octocat over 200 issues.
// Issues are seeded via HTTP so the FTS index is populated.
func BenchmarkSearch_HTTP(b *testing.B) {
	fx := searchStressEnv(b, 200, 0)
	url := fx.srv.URL + "/search/issues?q=user:octocat+state:open&per_page=30"
	tok := "token " + fx.token

	client := newPooledClient()
	b.SetParallelism(4)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest(http.MethodGet, url, nil)
			req.Header.Set("Authorization", tok)
			resp, err := client.Do(req)
			if err != nil {
				b.Fatal(err)
			}
			drainClose(resp)
			if resp.StatusCode != http.StatusOK {
				b.Fatalf("want 200, got %d", resp.StatusCode)
			}
		}
	})
}

// BenchmarkSearch_HTTP_compoundQuery measures label + author + state search.
// Issues carry 3 labels; all are seeded via HTTP for FTS population.
func BenchmarkSearch_HTTP_compoundQuery(b *testing.B) {
	fx := searchStressEnv(b, 100, 3)
	url := fx.srv.URL + "/search/issues?q=user:octocat+label:label-00+state:open+author:octocat&per_page=30"
	tok := "token " + fx.token
	client := newPooledClient()

	b.ResetTimer()
	for b.Loop() {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("Authorization", tok)
		resp, err := client.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		drainClose(resp)
		if resp.StatusCode != http.StatusOK {
			b.Fatalf("want 200, got %d", resp.StatusCode)
		}
	}
}

// ── comment list ──────────────────────────────────────────────────────────────

// BenchmarkIssueComments_HTTP measures /issues/1/comments with 100 comments.
func BenchmarkIssueComments_HTTP(b *testing.B) {
	fx := httpStressEnv(b, 10, 100, 0, false)
	url := fx.srv.URL + "/repos/octocat/hello/issues/1/comments?per_page=100"
	tok := "token " + fx.token

	client := newPooledClient()
	b.SetParallelism(4)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest(http.MethodGet, url, nil)
			req.Header.Set("Authorization", tok)
			resp, err := client.Do(req)
			if err != nil {
				b.Fatal(err)
			}
			drainClose(resp)
			if resp.StatusCode != http.StatusOK {
				b.Fatalf("want 200, got %d", resp.StatusCode)
			}
		}
	})
}

// ── milestone list ────────────────────────────────────────────────────────────

// BenchmarkMilestoneList_HTTP measures /milestones for a repo where one
// milestone owns 1 000 issues. MilestoneIssueCountsByPKs batch is the hot path.
func BenchmarkMilestoneList_HTTP(b *testing.B) {
	fx := issueServer(b)
	st := fx.st
	tok := fx.token
	ctx := context.Background()

	resp, body := authedSend(b, fx.srv, http.MethodPost, "/repos/octocat/hello/milestones", tok,
		`{"title":"v2.0"}`)
	if resp.StatusCode != http.StatusCreated {
		b.Fatalf("create milestone: %d: %s", resp.StatusCode, body)
	}
	var ms struct {
		Number int64 `json:"number"`
	}
	_ = json.Unmarshal(body, &ms)

	repoPK := resolveRepoPK(b, st)
	owner := resolveOwner(b, st)
	milestone, err := st.GetMilestoneByNumber(ctx, repoPK, ms.Number)
	if err != nil {
		b.Fatalf("GetMilestoneByNumber: %v", err)
	}

	base := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *store.Tx) error {
			for i := 1; i <= 1_000; i++ {
				when := base.Add(time.Duration(i) * time.Minute)
				iss := &store.IssueRow{
					RepoPK:      repoPK,
					Number:      int64(i),
					UserPK:      owner.PK,
					Title:       fmt.Sprintf("issue %d in milestone", i),
					State:       "open",
					MilestonePK: &milestone.PK,
					CreatedAt:   when,
					UpdatedAt:   when,
				}
				if err := tx.SeedIssue(ctx, iss); err != nil {
					return err
				}
			}
			return nil
		})
	}); err != nil {
		b.Fatalf("seed issues: %v", err)
	}
	_ = st.SetNextIssueNumber(ctx, repoPK, 1001)

	url := fx.srv.URL + "/repos/octocat/hello/milestones"
	authHdr := "token " + tok
	client := newPooledClient()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest(http.MethodGet, url, nil)
			req.Header.Set("Authorization", authHdr)
			r, err := client.Do(req)
			if err != nil {
				b.Fatal(err)
			}
			drainClose(r)
			if r.StatusCode != http.StatusOK {
				b.Fatalf("want 200, got %d", r.StatusCode)
			}
		}
	})
}

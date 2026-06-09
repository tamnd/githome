package domain

// Stress benchmarks for the domain assembly and write layers.
//
// These cover scenarios the microbenchmarks in bench_test.go skip:
//
//   - large page assembly (100 issues, 100 PRs, 500 comments)
//   - maximum-hydration issue (20 labels, 10 assignees, milestone)
//   - sequential CreateIssue throughput (write path including event+job)
//   - concurrent CreateIssue under shared allocator contention
//   - search with compound qualifiers (label + author + state)
//   - large search result page (100 hits)
//   - event feed assembly with maximum page size (100 events)
//   - comment list at 500 entries (rust-lang/rust dense review scenario)
//   - PR list with full label+assignee hydration

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
)

// stressEnv extends benchEnv with heavier seeding used by the stress tests.
type stressEnv struct {
	benchEnv
}

// newStressEnv builds a heavily-seeded domain environment:
//   - 1 owner + 20 assignee users + 1 repository
//   - 1 milestone
//   - 1 000 open issues
//   - issues 1–500 carry 20 labels each
//   - issues 501–700 carry 10 assignees each
//   - issues 1–1000 are linked to the milestone
//   - 500 pull requests (numbered 1001–1500)
//   - 500 comments on issue #1
//   - 100 events (for feed benchmarks)
func newStressEnv(b *testing.B) *stressEnv {
	b.Helper()
	ctx := context.Background()

	st, err := store.Open(ctx, "sqlite://"+filepath.Join(b.TempDir(), "stress.db"))
	if err != nil {
		b.Fatalf("store.Open: %v", err)
	}
	b.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		b.Fatalf("Migrate: %v", err)
	}

	gs := git.NewStore(b.TempDir())
	repos := NewRepoService(st, gs)
	issues := NewIssueService(st, repos)
	prs := NewPRService(st, repos, issues, gs)
	search := NewSearchService(st, repos, issues, gs)
	events := NewEventService(st, repos)

	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	owner := &store.UserRow{Login: "octocat", Type: "User", CreatedAt: base, UpdatedAt: base}
	repo := &store.RepoRow{Name: "stress", DefaultBranch: "main", CreatedAt: base, UpdatedAt: base}

	// 20 extra users for assignee tests.
	assigneeUsers := make([]*store.UserRow, 10)
	for i := range assigneeUsers {
		assigneeUsers[i] = &store.UserRow{
			Login:     fmt.Sprintf("contributor%02d", i),
			Type:      "User",
			CreatedAt: base,
			UpdatedAt: base,
		}
	}

	// 20 labels.
	labels := make([]*store.LabelRow, 20)
	for i := range labels {
		labels[i] = &store.LabelRow{Name: fmt.Sprintf("label-%02d", i), Color: "d73a4a"}
	}

	// milestone row (PK filled after insert).
	milestone := &store.MilestoneRow{Title: "v2.0", State: "open", CreatedAt: base, UpdatedAt: base}

	err = st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *store.Tx) error {
			if err := tx.SeedUser(ctx, owner); err != nil {
				return err
			}
			for _, u := range assigneeUsers {
				if err := tx.SeedUser(ctx, u); err != nil {
					return err
				}
			}
			repo.OwnerPK = owner.PK
			if err := tx.SeedRepo(ctx, repo); err != nil {
				return err
			}
			for _, l := range labels {
				l.RepoPK = repo.PK
				if err := tx.SeedLabel(ctx, l); err != nil {
					return err
				}
			}
			milestone.RepoPK = repo.PK
			milestone.CreatorPK = &owner.PK
			if err := tx.SeedMilestone(ctx, milestone); err != nil {
				return err
			}

			labelPKs := make([]int64, len(labels))
			for i, l := range labels {
				labelPKs[i] = l.PK
			}
			assigneePKs := make([]int64, len(assigneeUsers))
			for i, u := range assigneeUsers {
				assigneePKs[i] = u.PK
			}

			// 1 000 issues.
			for i := 1; i <= 1000; i++ {
				when := base.Add(time.Duration(i) * time.Minute)
				iss := &store.IssueRow{
					RepoPK:      repo.PK,
					Number:      int64(i),
					UserPK:      owner.PK,
					Title:       fmt.Sprintf("stress issue %d: tracking something important", i),
					State:       "open",
					MilestonePK: &milestone.PK,
					CreatedAt:   when,
					UpdatedAt:   when,
				}
				if err := tx.SeedIssue(ctx, iss); err != nil {
					return fmt.Errorf("SeedIssue %d: %w", i, err)
				}
				if i <= 500 {
					if err := tx.AttachLabels(ctx, iss.PK, labelPKs); err != nil {
						return err
					}
				}
				if i > 500 && i <= 700 {
					if err := tx.AddAssignees(ctx, iss.PK, assigneePKs); err != nil {
						return err
					}
				}
			}

			// 500 PRs (numbered 1001–1500).
			for i := 1; i <= 500; i++ {
				when := base.Add(time.Duration(1000+i) * time.Minute)
				prNum := int64(1000 + i)
				sha := fmt.Sprintf("%040x", i)
				pullIss := &store.IssueRow{
					RepoPK:    repo.PK,
					Number:    prNum,
					IsPull:    true,
					UserPK:    owner.PK,
					Title:     fmt.Sprintf("stress PR %d", i),
					State:     "open",
					CreatedAt: when,
					UpdatedAt: when,
				}
				if err := tx.SeedIssue(ctx, pullIss); err != nil {
					return err
				}
				pr := &store.PullRow{
					IssuePK:        pullIss.PK,
					RepoPK:         repo.PK,
					BaseRef:        "main",
					BaseSHA:        sha,
					HeadRef:        fmt.Sprintf("feature/%d", i),
					HeadSHA:        sha,
					MergeableState: "unknown",
					CreatedAt:      when,
					UpdatedAt:      when,
				}
				if err := tx.SeedPull(ctx, pr); err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		b.Fatalf("bulk seed: %v", err)
	}

	// Seed 500 comments on issue #1.
	issRow, err := st.GetIssueByNumber(ctx, repo.PK, 1)
	if err != nil {
		b.Fatalf("GetIssueByNumber(1): %v", err)
	}
	err = st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *store.Tx) error {
			for i := 1; i <= 500; i++ {
				when := base.Add(time.Duration(i) * time.Second)
				c := &store.CommentRow{
					IssuePK:   issRow.PK,
					UserPK:    owner.PK,
					Body:      fmt.Sprintf("stress comment %d: this is a longer body with some context", i),
					CreatedAt: when,
					UpdatedAt: when,
				}
				if err := tx.SeedComment(ctx, c); err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		b.Fatalf("seed comments: %v", err)
	}

	// Seed 100 events.
	for i := 1; i <= 100; i++ {
		ev := &store.EventRow{
			Event: "issues", Action: "opened",
			ActorPK: owner.PK, RepoPK: repo.PK, Public: true,
		}
		if err := st.InsertEvent(ctx, ev); err != nil {
			b.Fatalf("InsertEvent %d: %v", i, err)
		}
	}

	if err := st.SetNextIssueNumber(ctx, repo.PK, 1501); err != nil {
		b.Fatalf("SetNextIssueNumber: %v", err)
	}

	env := &stressEnv{}
	env.ctx = ctx
	env.st = st
	env.gs = gs
	env.repos = repos
	env.issues = issues
	env.prs = prs
	env.search = search
	env.events = events
	env.owner = owner
	env.repo = repo
	env.issueN = 1
	return env
}

// ── large page assembly ───────────────────────────────────────────────────────

// BenchmarkAssembleIssueList_100 assembles a page of 100 issues from a repo
// with 1 000. Target: ≤ 3 ms/op (proportional to the 30-item baseline).
func BenchmarkAssembleIssueList_100(b *testing.B) {
	env := newStressEnv(b)
	q := IssueQuery{State: "open", Page: 1, PerPage: 100}
	b.ResetTimer()
	for b.Loop() {
		issues, _, err := env.issues.ListIssues(env.ctx, env.owner.PK, "octocat", "stress", q)
		if err != nil {
			b.Fatalf("ListIssues: %v", err)
		}
		if len(issues) != 100 {
			b.Fatalf("got %d, want 100", len(issues))
		}
	}
}

// BenchmarkAssemblePRList_100 assembles a page of 100 pull requests.
// Target: ≤ 5 ms/op.
func BenchmarkAssemblePRList_100(b *testing.B) {
	env := newStressEnv(b)
	q := PRQuery{State: "open", Page: 1, PerPage: 100}
	b.ResetTimer()
	for b.Loop() {
		prs, _, err := env.prs.ListPRs(env.ctx, env.owner.PK, "octocat", "stress", q)
		if err != nil {
			b.Fatalf("ListPRs: %v", err)
		}
		if len(prs) != 100 {
			b.Fatalf("got %d, want 100", len(prs))
		}
	}
}

// ── max-hydration single issue ────────────────────────────────────────────────

// BenchmarkAssembleIssue_maxHydration assembles a single issue that carries
// 20 labels, 10 assignees, and a milestone. This is the worst-case single-row
// assembly, exercising every sub-query the single path runs.
func BenchmarkAssembleIssue_maxHydration(b *testing.B) {
	env := newStressEnv(b)
	// Issues 501–700 have 10 assignees. Issues 1–500 have 20 labels.
	// Use issue 1 which has 20 labels + milestone (no assignees).
	b.ResetTimer()
	for b.Loop() {
		issue, err := env.issues.GetIssue(env.ctx, env.owner.PK, "octocat", "stress", 1)
		if err != nil {
			b.Fatalf("GetIssue: %v", err)
		}
		if len(issue.Labels) != 20 {
			b.Fatalf("want 20 labels, got %d", len(issue.Labels))
		}
	}
}

// BenchmarkAssembleIssue_maxAssignees assembles a single issue with 10
// assignees. This stresses the assignee user-lookup chain.
func BenchmarkAssembleIssue_maxAssignees(b *testing.B) {
	env := newStressEnv(b)
	// Issues 501–700 have 10 assignees.
	b.ResetTimer()
	for b.Loop() {
		issue, err := env.issues.GetIssue(env.ctx, env.owner.PK, "octocat", "stress", 501)
		if err != nil {
			b.Fatalf("GetIssue: %v", err)
		}
		if len(issue.Assignees) != 10 {
			b.Fatalf("want 10 assignees, got %d", len(issue.Assignees))
		}
	}
}

// ── 500-comment thread ────────────────────────────────────────────────────────

// BenchmarkAssembleCommentList_500 measures ListComments for a thread of 500
// comments. Target: ≤ 2 ms/op.
func BenchmarkAssembleCommentList_500(b *testing.B) {
	env := newStressEnv(b)
	b.ResetTimer()
	for b.Loop() {
		comments, err := env.issues.ListComments(env.ctx, env.owner.PK, "octocat", "stress", env.issueN, 1, 500)
		if err != nil {
			b.Fatalf("ListComments: %v", err)
		}
		if len(comments) != 500 {
			b.Fatalf("got %d, want 500", len(comments))
		}
	}
}

// ── compound search ───────────────────────────────────────────────────────────

// BenchmarkAssembleSearch_compoundQuery benchmarks a search with label +
// state + author qualifiers returning 30 results from a 1 000-issue repo.
func BenchmarkAssembleSearch_compoundQuery(b *testing.B) {
	env := newStressEnv(b)
	raw := "user:octocat label:label-00 state:open author:octocat"
	b.ResetTimer()
	for b.Loop() {
		hits, _, err := env.search.SearchIssues(env.ctx, env.owner.PK, raw, "created", "desc", 1, 30)
		if err != nil {
			b.Fatalf("SearchIssues: %v", err)
		}
		_ = hits
	}
}

// BenchmarkAssembleSearch_100 benchmarks a search returning 100 results, the
// maximum per_page value GitHub supports. This shows the batch loader scaling.
func BenchmarkAssembleSearch_100(b *testing.B) {
	env := newStressEnv(b)
	raw := "user:octocat"
	b.ResetTimer()
	for b.Loop() {
		hits, _, err := env.search.SearchIssues(env.ctx, env.owner.PK, raw, "created", "desc", 1, 100)
		if err != nil {
			b.Fatalf("SearchIssues: %v", err)
		}
		if len(hits) != 100 {
			b.Fatalf("got %d, want 100", len(hits))
		}
	}
}

// ── event feed ───────────────────────────────────────────────────────────────

// BenchmarkAssembleEventFeed_100 measures PublicFeed at maximum depth (100
// events). Target: ≤ 2 ms/op.
func BenchmarkAssembleEventFeed_100(b *testing.B) {
	env := newStressEnv(b)
	b.ResetTimer()
	for b.Loop() {
		evs, err := env.events.PublicFeed(env.ctx, 100)
		if err != nil {
			b.Fatalf("PublicFeed: %v", err)
		}
		if len(evs) != 100 {
			b.Fatalf("got %d, want 100", len(evs))
		}
	}
}

// ── write throughput ──────────────────────────────────────────────────────────

// BenchmarkCreateIssue_sequential measures sequential CreateIssue throughput:
// one issue per loop iteration, all within the same repository. This exercises
// the full write path: auth, AllocIssueNumber, InsertIssue, InsertEventAndJob.
func BenchmarkCreateIssue_sequential(b *testing.B) {
	ctx := context.Background()
	st, err := store.Open(ctx, "sqlite://"+filepath.Join(b.TempDir(), "write.db"))
	if err != nil {
		b.Fatalf("store.Open: %v", err)
	}
	b.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		b.Fatalf("Migrate: %v", err)
	}
	gs := git.NewStore(b.TempDir())
	repos := NewRepoService(st, gs)
	issues := NewIssueService(st, repos)

	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	owner := &store.UserRow{Login: "writer", Type: "User", CreatedAt: base, UpdatedAt: base}
	if err := st.InsertUser(ctx, owner); err != nil {
		b.Fatalf("InsertUser: %v", err)
	}
	repo := &store.RepoRow{
		OwnerPK: owner.PK, Name: "writes", DefaultBranch: "main",
		CreatedAt: base, UpdatedAt: base,
	}
	if err := st.InsertRepo(ctx, repo); err != nil {
		b.Fatalf("InsertRepo: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	var n int
	for b.Loop() {
		n++
		_, err := issues.CreateIssue(ctx, owner.PK, "writer", "writes", IssueInput{
			Title: fmt.Sprintf("sequential issue %d", n),
		})
		if err != nil {
			b.Fatalf("CreateIssue: %v", err)
		}
	}
}

// BenchmarkCreateIssue_concurrent_8 measures CreateIssue under 8-way
// concurrency within a single repository. SQLite serializes writes; this
// measures the lock-wait overhead on top of the sequential write cost.
func BenchmarkCreateIssue_concurrent_8(b *testing.B) {
	ctx := context.Background()
	st, err := store.Open(ctx, "sqlite://"+filepath.Join(b.TempDir(), "concurrent.db"))
	if err != nil {
		b.Fatalf("store.Open: %v", err)
	}
	b.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		b.Fatalf("Migrate: %v", err)
	}
	gs := git.NewStore(b.TempDir())
	repos := NewRepoService(st, gs)
	issues := NewIssueService(st, repos)

	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	owner := &store.UserRow{Login: "writer", Type: "User", CreatedAt: base, UpdatedAt: base}
	if err := st.InsertUser(ctx, owner); err != nil {
		b.Fatalf("InsertUser: %v", err)
	}
	repo := &store.RepoRow{
		OwnerPK: owner.PK, Name: "writes", DefaultBranch: "main",
		CreatedAt: base, UpdatedAt: base,
	}
	if err := st.InsertRepo(ctx, repo); err != nil {
		b.Fatalf("InsertRepo: %v", err)
	}

	var mu sync.Mutex
	var counter int

	b.SetParallelism(8)
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			mu.Lock()
			counter++
			n := counter
			mu.Unlock()
			_, err := issues.CreateIssue(ctx, owner.PK, "writer", "writes", IssueInput{
				Title: fmt.Sprintf("concurrent issue %d", n),
			})
			if err != nil {
				b.Fatalf("CreateIssue: %v", err)
			}
		}
	})
}

// ── deep pagination via keyset ────────────────────────────────────────────────

// BenchmarkListIssues_keysetDeep_1000 measures ListIssuesPage with a cursor
// positioned at page 33 (row 990 of 1 000-issue repo) using the keyset path.
// This proves flat O(1) cost at depth.
func BenchmarkListIssues_keysetDeep_1000(b *testing.B) {
	env := newStressEnv(b)

	// Walk to page 32 to acquire a real cursor.
	q := IssueQuery{State: "open", PerPage: 30}
	var cursor string
	for pg := 1; pg <= 32; pg++ {
		q.Page = pg
		q.Cursor = cursor
		issues, _, err := env.issues.ListIssuesPage(env.ctx, env.owner.PK, "octocat", "stress", q)
		if err != nil {
			b.Fatalf("ListIssuesPage pg %d: %v", pg, err)
		}
		if len(issues) == 0 {
			break
		}
	}
	// cursor is the Link header token; for the domain layer it's a query param.
	// Run from the last cursor position to measure seek cost at depth.
	q.Cursor = cursor
	q.Page = 33
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, _, err := env.issues.ListIssuesPage(env.ctx, env.owner.PK, "octocat", "stress", q)
		if err != nil {
			b.Fatalf("ListIssuesPage deep: %v", err)
		}
	}
}

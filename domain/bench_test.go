package domain

// Domain-layer assembly microbenchmarks.
//
// Each benchmark spins up a real in-process SQLite store, seeds it with
// synthetic data, wires the domain services over it, then calls the service
// methods in the hot loop. b.ResetTimer() is called after the seed so only the
// assembly work is measured.
//
// Targets (from notes/Spec/2001/implementation/performance/03_microbenchmarks.md):
//   BenchmarkAssembleIssueList_30   <= 1.5 ms/op
//   BenchmarkAssemblePRList_30      <= 2.5 ms/op
//   BenchmarkAssembleSearch_30      <= 2.0 ms/op
//   BenchmarkAssembleCommentList_100 <= 1.0 ms/op
//   BenchmarkAssembleEventFeed_50   <= 1.0 ms/op

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
)

// benchEnv is the shared setup for all domain assembly benchmarks: a real
// SQLite store, migrated, seeded, and wired into the domain services.
type benchEnv struct {
	ctx     context.Context
	st      *store.Store
	gs      *git.Store
	repos   *RepoService
	issues  *IssueService
	prs     *PRService
	search  *SearchService
	events  *EventService
	owner   *store.UserRow
	repo    *store.RepoRow
	issueN  int64 // issue number of the first issue (for comment list)
}

// newBenchEnv builds a fully-seeded benchmark environment. It seeds:
//   - 1 owner + 1 repository
//   - 300 open issues (numbered 1–300)
//   - 300 pull requests (each backed by a paired IsPull issue row, numbered 301–600)
//   - 100 comments on issue #1
//   - 50 events in the events table (for the feed benchmarks)
//
// It does NOT call b.ResetTimer(); callers do that immediately before their
// measured loop.
func newBenchEnv(b *testing.B) *benchEnv {
	b.Helper()
	ctx := context.Background()

	st, err := store.Open(ctx, "sqlite://"+filepath.Join(b.TempDir(), "bench.db"))
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

	// Seed owner and repository.
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	owner := &store.UserRow{
		Login:     "octocat",
		Type:      "User",
		CreatedAt: base,
		UpdatedAt: base,
	}
	repo := &store.RepoRow{
		Name:          "hello",
		DefaultBranch: "main",
		CreatedAt:     base,
		UpdatedAt:     base,
	}

	err = st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *store.Tx) error {
			if err := tx.SeedUser(ctx, owner); err != nil {
				return fmt.Errorf("SeedUser: %w", err)
			}
			repo.OwnerPK = owner.PK
			if err := tx.SeedRepo(ctx, repo); err != nil {
				return fmt.Errorf("SeedRepo: %w", err)
			}

			// 300 plain issues.
			for i := 1; i <= 300; i++ {
				when := base.Add(time.Duration(i) * time.Minute)
				iss := &store.IssueRow{
					RepoPK:    repo.PK,
					Number:    int64(i),
					UserPK:    owner.PK,
					Title:     fmt.Sprintf("issue %d", i),
					State:     "open",
					CreatedAt: when,
					UpdatedAt: when,
				}
				if err := tx.SeedIssue(ctx, iss); err != nil {
					return fmt.Errorf("SeedIssue %d: %w", i, err)
				}
			}

			// 300 pull requests: each needs an IsPull issue row plus a pull_requests row.
			for i := 1; i <= 300; i++ {
				when := base.Add(time.Duration(300+i) * time.Minute)
				issNum := int64(300 + i)
				pullIss := &store.IssueRow{
					RepoPK:    repo.PK,
					Number:    issNum,
					IsPull:    true,
					UserPK:    owner.PK,
					Title:     fmt.Sprintf("pull request %d", i),
					State:     "open",
					CreatedAt: when,
					UpdatedAt: when,
				}
				if err := tx.SeedIssue(ctx, pullIss); err != nil {
					return fmt.Errorf("SeedIssue PR %d: %w", i, err)
				}
				sha := fmt.Sprintf("%040x", i) // deterministic fake SHA
				pr := &store.PullRow{
					IssuePK:        pullIss.PK,
					RepoPK:         repo.PK,
					BaseRef:        "main",
					BaseSHA:        sha,
					HeadRef:        fmt.Sprintf("feature-%d", i),
					HeadSHA:        sha,
					MergeableState: "unknown",
					CreatedAt:      when,
					UpdatedAt:      when,
				}
				if err := tx.SeedPull(ctx, pr); err != nil {
					return fmt.Errorf("SeedPull %d: %w", i, err)
				}
			}

			// Look up issue #1 so we can seed comments on it.
			return nil
		})
	})
	if err != nil {
		b.Fatalf("bulk seed: %v", err)
	}

	// Resolve issue #1 for the comment seed.
	issRow, err := st.GetIssueByNumber(ctx, repo.PK, 1)
	if err != nil {
		b.Fatalf("GetIssueByNumber(1): %v", err)
	}

	// Seed 100 comments on issue #1 and 50 events in a separate transaction
	// (outside BulkLoad to keep the helper simple).
	err = st.WithTx(ctx, func(tx *store.Tx) error {
		for i := 1; i <= 100; i++ {
			when := base.Add(time.Duration(i) * time.Second)
			c := &store.CommentRow{
				IssuePK:   issRow.PK,
				UserPK:    owner.PK,
				Body:      fmt.Sprintf("comment body %d", i),
				CreatedAt: when,
				UpdatedAt: when,
			}
			if err := tx.SeedComment(ctx, c); err != nil {
				return fmt.Errorf("SeedComment %d: %w", i, err)
			}
		}
		return nil
	})
	if err != nil {
		b.Fatalf("seed comments: %v", err)
	}

	// Seed 50 events.
	for i := 1; i <= 50; i++ {
		ev := &store.EventRow{
			Event:   "issues",
			Action:  "opened",
			ActorPK: owner.PK,
			RepoPK:  repo.PK,
			Public:  true,
		}
		if err := st.InsertEvent(ctx, ev); err != nil {
			b.Fatalf("InsertEvent %d: %v", i, err)
		}
	}

	// Advance the issue number allocator past the highest seeded number so any
	// live writes in the environment don't collide.
	if err := st.SetNextIssueNumber(ctx, repo.PK, 601); err != nil {
		b.Fatalf("SetNextIssueNumber: %v", err)
	}

	return &benchEnv{
		ctx:    ctx,
		st:     st,
		gs:     gs,
		repos:  repos,
		issues: issues,
		prs:    prs,
		search: search,
		events: events,
		owner:  owner,
		repo:   repo,
		issueN: 1,
	}
}

// BenchmarkAssembleIssueList_30 measures ListIssues assembly for a page of 30
// issues. Target: <= 1.5 ms/op.
func BenchmarkAssembleIssueList_30(b *testing.B) {
	env := newBenchEnv(b)
	q := IssueQuery{State: "open", Page: 1, PerPage: 30}
	b.ResetTimer()
	for b.Loop() {
		issues, _, err := env.issues.ListIssues(env.ctx, env.owner.PK, "octocat", "hello", q)
		if err != nil {
			b.Fatalf("ListIssues: %v", err)
		}
		if len(issues) != 30 {
			b.Fatalf("got %d issues, want 30", len(issues))
		}
	}
}

// BenchmarkAssemblePRList_30 measures ListPRs assembly for a page of 30 pull
// requests. Target: <= 2.5 ms/op.
func BenchmarkAssemblePRList_30(b *testing.B) {
	env := newBenchEnv(b)
	q := PRQuery{State: "open", Page: 1, PerPage: 30}
	b.ResetTimer()
	for b.Loop() {
		prs, _, err := env.prs.ListPRs(env.ctx, env.owner.PK, "octocat", "hello", q)
		if err != nil {
			b.Fatalf("ListPRs: %v", err)
		}
		if len(prs) != 30 {
			b.Fatalf("got %d PRs, want 30", len(prs))
		}
	}
}

// BenchmarkAssembleSearch_30 measures SearchIssues result assembly for a page
// of 30 results. Target: <= 2.0 ms/op.
func BenchmarkAssembleSearch_30(b *testing.B) {
	env := newBenchEnv(b)
	// Use an open-ended free-text query that matches all issues in the repo.
	// Scope by user: so the search service can resolve the repo set.
	raw := "user:octocat"
	b.ResetTimer()
	for b.Loop() {
		hits, _, err := env.search.SearchIssues(env.ctx, env.owner.PK, raw, "created", "desc", 1, 30)
		if err != nil {
			b.Fatalf("SearchIssues: %v", err)
		}
		if len(hits) != 30 {
			b.Fatalf("got %d hits, want 30", len(hits))
		}
	}
}

// BenchmarkAssembleCommentList_100 measures ListComments assembly for 100
// comments on a single issue. Target: <= 1.0 ms/op.
func BenchmarkAssembleCommentList_100(b *testing.B) {
	env := newBenchEnv(b)
	b.ResetTimer()
	for b.Loop() {
		comments, err := env.issues.ListComments(env.ctx, env.owner.PK, "octocat", "hello", env.issueN, 1, 100)
		if err != nil {
			b.Fatalf("ListComments: %v", err)
		}
		if len(comments) != 100 {
			b.Fatalf("got %d comments, want 100", len(comments))
		}
	}
}

// BenchmarkAssembleEventFeed_50 measures the event feed assembly for 50 events
// from PublicFeed. Target: <= 1.0 ms/op.
func BenchmarkAssembleEventFeed_50(b *testing.B) {
	env := newBenchEnv(b)
	b.ResetTimer()
	for b.Loop() {
		evs, err := env.events.PublicFeed(env.ctx, 50)
		if err != nil {
			b.Fatalf("PublicFeed: %v", err)
		}
		if len(evs) != 50 {
			b.Fatalf("got %d events, want 50", len(evs))
		}
	}
}

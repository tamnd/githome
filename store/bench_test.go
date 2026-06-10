package store

// Microbenchmarks for the store layer. Each benchmark opens a fresh SQLite
// database in b.TempDir(), seeds the corpus it needs, calls b.ResetTimer(),
// and then runs the query under test in the benchmark loop.
//
// Run with:
//
//	go test ./store -bench=. -benchmem -count=1

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// benchStore opens a migrated SQLite Store backed by a temp file and returns
// it together with a cancel function. The caller registers t.Cleanup to close.
func benchStore(b *testing.B) *Store {
	b.Helper()
	dsn := "sqlite://" + filepath.Join(b.TempDir(), "bench.db")
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		b.Fatalf("Migrate: %v", err)
	}
	return st
}

// benchSeedUser inserts a user and returns its row.
func benchSeedUser(b *testing.B, st *Store, login string) *UserRow {
	b.Helper()
	ctx := context.Background()
	u := &UserRow{Login: login, Type: "User"}
	if err := st.InsertUser(ctx, u); err != nil {
		b.Fatalf("InsertUser %s: %v", login, err)
	}
	return u
}

// benchSeedRepo inserts a repository under owner and returns its row.
func benchSeedRepo(b *testing.B, st *Store, owner *UserRow, name string) *RepoRow {
	b.Helper()
	ctx := context.Background()
	r := &RepoRow{OwnerPK: owner.PK, Name: name, DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, r); err != nil {
		b.Fatalf("InsertRepo %s: %v", name, err)
	}
	return r
}

// benchSeedIssues inserts n open issues into the repository using SeedIssue so
// timestamps are preserved (one-minute apart, ascending) and returns their PKs.
func benchSeedIssues(b *testing.B, st *Store, repoPK, userPK int64, n int) []int64 {
	b.Helper()
	ctx := context.Background()
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	pks := make([]int64, 0, n)
	err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *Tx) error {
			for i := 1; i <= n; i++ {
				when := base.Add(time.Duration(i) * time.Minute)
				iss := &IssueRow{
					RepoPK:    repoPK,
					Number:    int64(i),
					UserPK:    userPK,
					Title:     fmt.Sprintf("issue %d fix crash in module", i),
					State:     "open",
					CreatedAt: when,
					UpdatedAt: when,
				}
				if err := tx.SeedIssue(ctx, iss); err != nil {
					return err
				}
				pks = append(pks, iss.PK)
			}
			return nil
		})
	})
	if err != nil {
		b.Fatalf("seed %d issues: %v", n, err)
	}
	// Advance the number allocator past the seeded numbers.
	if err := st.SetNextIssueNumber(ctx, repoPK, int64(n+1)); err != nil {
		b.Fatalf("SetNextIssueNumber: %v", err)
	}
	return pks
}

// benchSeedLabels inserts labels and attaches them to every issue in issuePKs.
func benchSeedLabels(b *testing.B, st *Store, repoPK int64, names []string, issuePKs []int64) []*LabelRow {
	b.Helper()
	ctx := context.Background()
	labels := make([]*LabelRow, len(names))
	for i, name := range names {
		l := &LabelRow{RepoPK: repoPK, Name: name, Color: "d73a4a"}
		if err := st.InsertLabel(ctx, l); err != nil {
			b.Fatalf("InsertLabel %s: %v", name, err)
		}
		labels[i] = l
	}
	labelPKs := make([]int64, len(labels))
	for i, l := range labels {
		labelPKs[i] = l.PK
	}
	for _, issuePK := range issuePKs {
		if err := st.WithTx(ctx, func(tx *Tx) error {
			return tx.AttachLabels(ctx, issuePK, labelPKs)
		}); err != nil {
			b.Fatalf("AttachLabels: %v", err)
		}
	}
	return labels
}

// BenchmarkListIssues_30PerPage_open lists 30 open issues from a repo with 300.
func BenchmarkListIssues_30PerPage_open(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	benchSeedIssues(b, st, repo.PK, owner.PK, 300)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := st.ListIssues(ctx, repo.PK, IssueFilter{State: "open", Limit: 30})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkListIssues_30PerPage_2Labels lists 30 issues filtered by 2 labels
// from a repo where every issue carries both labels.
func BenchmarkListIssues_30PerPage_2Labels(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	issuePKs := benchSeedIssues(b, st, repo.PK, owner.PK, 300)
	benchSeedLabels(b, st, repo.PK, []string{"bug", "enhancement"}, issuePKs)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := st.ListIssues(ctx, repo.PK, IssueFilter{
			State:  "open",
			Labels: []string{"bug", "enhancement"},
			Limit:  30,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkListIssues_deepPage_page50 lists page 50 (OFFSET 1470) of issues
// in a repo with 1500 open issues.
func BenchmarkListIssues_deepPage_page50(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	benchSeedIssues(b, st, repo.PK, owner.PK, 1500)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := st.ListIssues(ctx, repo.PK, IssueFilter{
			State:  "open",
			Limit:  30,
			Offset: 1470, // page 50: rows 1471-1500
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkListPulls_30PerPage lists 30 open pull requests from a repo with 300.
func BenchmarkListPulls_30PerPage(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")

	// Seed 300 pull requests: each needs both an issue row (IsPull=true) and a
	// pull_requests extension row.
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *Tx) error {
			for i := 1; i <= 300; i++ {
				when := base.Add(time.Duration(i) * time.Minute)
				sha := fmt.Sprintf("%040x", i)
				iss := &IssueRow{
					RepoPK:    repo.PK,
					Number:    int64(i),
					UserPK:    owner.PK,
					IsPull:    true,
					Title:     fmt.Sprintf("pull request %d", i),
					State:     "open",
					CreatedAt: when,
					UpdatedAt: when,
				}
				if err := tx.SeedIssue(ctx, iss); err != nil {
					return err
				}
				pr := &PullRow{
					IssuePK:   iss.PK,
					RepoPK:    repo.PK,
					BaseRef:   "main",
					BaseSHA:   sha,
					HeadRef:   fmt.Sprintf("feature-%d", i),
					HeadSHA:   sha,
					CreatedAt: when,
					UpdatedAt: when,
				}
				if err := tx.SeedPull(ctx, pr); err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		b.Fatalf("seed pulls: %v", err)
	}
	if err := st.SetNextIssueNumber(ctx, repo.PK, 301); err != nil {
		b.Fatalf("SetNextIssueNumber: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := st.ListPulls(ctx, repo.PK, "open", 30, 0)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSearchIssues_term runs a cross-repo issue search with one search
// term over a corpus of 300 issues.
func BenchmarkSearchIssues_term(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	benchSeedIssues(b, st, repo.PK, owner.PK, 300)

	q := IssueSearch{
		ViewerPK:   owner.PK,
		Terms:      []string{"crash"},
		MatchTitle: true,
		MatchBody:  true,
		RepoPKs:    []int64{repo.PK},
		Limit:      30,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := st.SearchIssues(ctx, q)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSearchRepos_term runs a cross-repo repository search with one term
// over a corpus of 50 repositories.
func BenchmarkSearchRepos_term(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")

	// Seed 50 repositories with names that include the search term.
	for i := range 50 {
		_ = benchSeedRepo(b, st, owner, fmt.Sprintf("go-parser-%d", i))
	}

	q := RepoSearch{
		ViewerPK: owner.PK,
		Terms:    []string{"parser"},
		Limit:    30,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := st.SearchRepositories(ctx, q)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLabelsByIssue loads all labels for a single issue that carries 3
// labels, the typical per-issue label-hydration call.
func BenchmarkLabelsByIssue(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	issuePKs := benchSeedIssues(b, st, repo.PK, owner.PK, 10)
	// Attach 3 labels to the first issue.
	benchSeedLabels(b, st, repo.PK, []string{"bug", "enhancement", "documentation"}, issuePKs[:1])
	targetPK := issuePKs[0]

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := st.LabelsByIssuePKs(ctx, []int64{targetPK})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkListAssigneePKs loads assignee PKs for a single issue with 3
// assignees.
func BenchmarkListAssigneePKs(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	issuePKs := benchSeedIssues(b, st, repo.PK, owner.PK, 10)
	targetPK := issuePKs[0]

	// Create 3 users and assign them.
	var userPKs []int64
	for i := range 3 {
		u := benchSeedUser(b, st, fmt.Sprintf("assignee-%d", i))
		userPKs = append(userPKs, u.PK)
	}
	if err := st.WithTx(ctx, func(tx *Tx) error {
		return tx.AddAssignees(ctx, targetPK, userPKs)
	}); err != nil {
		b.Fatalf("AddAssignees: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := st.ListAssigneePKs(ctx, targetPK)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkListComments_longThread lists comments on an issue with 100 replies.
func BenchmarkListComments_longThread(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	issuePKs := benchSeedIssues(b, st, repo.PK, owner.PK, 5)
	threadIssue := issuePKs[0]

	// Seed 100 comments on the issue.
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *Tx) error {
			for i := range 100 {
				when := base.Add(time.Duration(i) * time.Minute)
				c := &CommentRow{
					IssuePK:   threadIssue,
					UserPK:    owner.PK,
					Body:      fmt.Sprintf("comment %d: looks good to me, nice work on this PR", i),
					CreatedAt: when,
					UpdatedAt: when,
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

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := st.ListIssueComments(ctx, threadIssue, 100, 0)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkListEvents_repoFeed lists 50 events from a repository's activity
// feed backed by 200 seeded events.
func BenchmarkListEvents_repoFeed(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	issuePKs := benchSeedIssues(b, st, repo.PK, owner.PK, 20)

	// Seed 200 events spread across the seeded issues.
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *Tx) error {
			for i := range 200 {
				when := base.Add(time.Duration(i) * time.Minute)
				issuePK := issuePKs[i%len(issuePKs)]
				e := &EventRow{
					Event:     "issues",
					Action:    "opened",
					ActorPK:   owner.PK,
					RepoPK:    repo.PK,
					IssuePK:   &issuePK,
					Public:    true,
					CreatedAt: when,
				}
				if err := tx.InsertEvent(ctx, e); err != nil {
					return err
				}
			}
			return nil
		})
	}); err != nil {
		b.Fatalf("seed events: %v", err)
	}

	f := EventFilter{RepoPK: &repo.PK, Limit: 50}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := st.ListEvents(ctx, f)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAllocDBID measures the per-call cost of the global ID allocator,
// which every write path calls at least once.
func BenchmarkAllocDBID(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := st.AllocDBID(ctx)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkClaimJob measures the cost of claiming a job from the queue, the
// hot path the background worker hits on every tick. The queue is pre-loaded
// with 50 queued jobs; ClaimJob returns ErrNotFound once drained and the bench
// re-seeds between N iterations via b.StopTimer / b.StartTimer.
func BenchmarkClaimJob(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()

	const queueDepth = 50

	enqueueN := func(n int) {
		for i := range n {
			j := &JobRow{Kind: "recompute_mergeability", Payload: fmt.Sprintf(`{"pull_pk":%d}`, i+1)}
			if _, err := st.EnqueueJob(ctx, j); err != nil {
				b.Fatalf("EnqueueJob: %v", err)
			}
		}
	}
	// Initial fill.
	enqueueN(queueDepth)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := st.ClaimJob(ctx)
		if err == ErrNotFound {
			// Queue drained; refill without counting toward the benchmark time.
			b.StopTimer()
			enqueueN(queueDepth)
			b.StartTimer()
			_, err = st.ClaimJob(ctx)
		}
		if err != nil && err != ErrNotFound {
			b.Fatalf("ClaimJob: %v", err)
		}
	}
}

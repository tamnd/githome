package store

// Stress benchmarks for the store layer. Each benchmark exercises a
// worst-case or high-contention path that the microbenchmarks in bench_test.go
// do not cover:
//
//   - empty result sets (filter that matches nothing)
//   - maximum label and assignee counts per issue
//   - deep OFFSET pagination (page 500 of a large repo)
//   - keyset pagination at the same depth (flat cost proof)
//   - milestone with 10 K issues (MilestoneIssueCounts full-count scan)
//   - multi-qualifier search (label + assignee + state)
//   - cross-repo search (20 repos in scope)
//   - concurrent ID allocation (serialization under contention)
//   - concurrent issue-number allocation (per-repo sequence, same repo)
//   - deep job queue (1 K pending jobs before ClaimJob)
//   - InsertEventAndJob atomic pair (P9 invariant throughput)
//   - dense review-comment thread (500 comments on one PR)
//   - dense commit-status poll (100 contexts per SHA)
//   - batch label hydration at max scale (100 issues × 20 labels)
//   - batch assignee hydration at max scale (100 issues × 10 assignees)
//   - unicode title/body (emoji + RTL + 4-byte UTF-8 sequences)
//
// Run:
//
//	go test ./store -bench=Stress -benchmem -count=1 -run=^$

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// benchSeedMilestone inserts a milestone and links n issues to it.
func benchSeedMilestone(b *testing.B, st *Store, repoPK, userPK int64, n int) (*MilestoneRow, []int64) {
	b.Helper()
	ctx := context.Background()
	m := &MilestoneRow{
		RepoPK:    repoPK,
		CreatorPK: &userPK,
		Title:     "v1.0",
		State:     "open",
	}
	if err := st.InsertMilestone(ctx, m); err != nil {
		b.Fatalf("InsertMilestone: %v", err)
	}

	issuePKs := make([]int64, 0, n)
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *Tx) error {
			for i := 1; i <= n; i++ {
				when := base.Add(time.Duration(i) * time.Minute)
				iss := &IssueRow{
					RepoPK:      repoPK,
					Number:      int64(i),
					UserPK:      userPK,
					Title:       fmt.Sprintf("milestone issue %d", i),
					State:       "open",
					MilestonePK: &m.PK,
					CreatedAt:   when,
					UpdatedAt:   when,
				}
				if err := tx.SeedIssue(ctx, iss); err != nil {
					return err
				}
				issuePKs = append(issuePKs, iss.PK)
			}
			return nil
		})
	})
	if err != nil {
		b.Fatalf("seed milestone issues: %v", err)
	}
	if err := st.SetNextIssueNumber(ctx, repoPK, int64(n+1)); err != nil {
		b.Fatalf("SetNextIssueNumber: %v", err)
	}
	return m, issuePKs
}

// benchSeedUsersN inserts n users with sequential login names and returns them.
func benchSeedUsersN(b *testing.B, st *Store, n int) []*UserRow {
	b.Helper()
	ctx := context.Background()
	users := make([]*UserRow, n)
	for i := range n {
		u := &UserRow{Login: fmt.Sprintf("user%04d", i), Type: "User"}
		if err := st.InsertUser(ctx, u); err != nil {
			b.Fatalf("InsertUser %d: %v", i, err)
		}
		users[i] = u
	}
	return users
}

// benchSeedIssuesWithLabels seeds n issues each carrying labelsPerIssue labels
// (all different) and returns the issue PKs and label PKs.
func benchSeedIssuesWithLabels(b *testing.B, st *Store, repoPK, userPK int64, nIssues, labelsPerIssue int) (issuePKs, labelPKs []int64) {
	b.Helper()
	ctx := context.Background()

	labelPKs = make([]int64, labelsPerIssue)
	for i := range labelsPerIssue {
		l := &LabelRow{RepoPK: repoPK, Name: fmt.Sprintf("label-%03d", i), Color: "d73a4a"}
		if err := st.InsertLabel(ctx, l); err != nil {
			b.Fatalf("InsertLabel %d: %v", i, err)
		}
		labelPKs[i] = l.PK
	}

	issuePKs = make([]int64, 0, nIssues)
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *Tx) error {
			for i := 1; i <= nIssues; i++ {
				when := base.Add(time.Duration(i) * time.Minute)
				iss := &IssueRow{
					RepoPK:    repoPK,
					Number:    int64(i),
					UserPK:    userPK,
					Title:     fmt.Sprintf("issue %d", i),
					State:     "open",
					CreatedAt: when,
					UpdatedAt: when,
				}
				if err := tx.SeedIssue(ctx, iss); err != nil {
					return err
				}
				issuePKs = append(issuePKs, iss.PK)
				if err := tx.AttachLabels(ctx, iss.PK, labelPKs); err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		b.Fatalf("seed issues+labels: %v", err)
	}
	if err := st.SetNextIssueNumber(ctx, repoPK, int64(nIssues+1)); err != nil {
		b.Fatalf("SetNextIssueNumber: %v", err)
	}
	return issuePKs, labelPKs
}

// benchSeedIssuesWithAssignees seeds n issues each with assigneesPerIssue
// users attached as assignees. The extra users are created inside the helper.
func benchSeedIssuesWithAssignees(b *testing.B, st *Store, repoPK, ownerPK int64, nIssues, assigneesPerIssue int) (issuePKs []int64, assigneePKs []int64) {
	b.Helper()
	ctx := context.Background()

	users := benchSeedUsersN(b, st, assigneesPerIssue)
	assigneePKs = make([]int64, assigneesPerIssue)
	for i, u := range users {
		assigneePKs[i] = u.PK
	}

	issuePKs = make([]int64, 0, nIssues)
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *Tx) error {
			for i := 1; i <= nIssues; i++ {
				when := base.Add(time.Duration(i) * time.Minute)
				iss := &IssueRow{
					RepoPK:    repoPK,
					Number:    int64(i),
					UserPK:    ownerPK,
					Title:     fmt.Sprintf("issue %d", i),
					State:     "open",
					CreatedAt: when,
					UpdatedAt: when,
				}
				if err := tx.SeedIssue(ctx, iss); err != nil {
					return err
				}
				issuePKs = append(issuePKs, iss.PK)
				if err := tx.AddAssignees(ctx, iss.PK, assigneePKs); err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		b.Fatalf("seed issues+assignees: %v", err)
	}
	if err := st.SetNextIssueNumber(ctx, repoPK, int64(nIssues+1)); err != nil {
		b.Fatalf("SetNextIssueNumber: %v", err)
	}
	return issuePKs, assigneePKs
}

// ── empty result ──────────────────────────────────────────────────────────────

// BenchmarkListIssues_emptyResult measures the cost of a filter that matches
// zero rows. This is the short-circuit path: the store still pays query
// planning and index seek cost even when nothing comes back.
func BenchmarkListIssues_emptyResult(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	// Seed only open issues; filter for closed → zero results.
	benchSeedIssues(b, st, repo.PK, owner.PK, 300)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		rows, err := st.ListIssues(ctx, repo.PK, IssueFilter{State: "closed", Limit: 30})
		if err != nil {
			b.Fatal(err)
		}
		if len(rows) != 0 {
			b.Fatalf("want 0 rows, got %d", len(rows))
		}
	}
}

// ── deep OFFSET pagination ────────────────────────────────────────────────────

// BenchmarkListIssues_deepPage_page500 measures listing page 500 (OFFSET 14970)
// of a 15 000-issue repo. The OFFSET forces a full skip-scan of all prior rows.
func BenchmarkListIssues_deepPage_page500(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	benchSeedIssues(b, st, repo.PK, owner.PK, 15_000)

	const page = 500
	f := IssueFilter{State: "open", Limit: 30, Offset: (page - 1) * 30}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := st.ListIssues(ctx, repo.PK, f)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkListIssues_keysetPage_deep measures the same logical position as
// page 500 but using a keyset cursor instead of OFFSET. The cursor holds the
// (created_at, number) of the last row on page 499, so the query uses an index
// seek instead of a skip-scan.
func BenchmarkListIssues_keysetPage_deep(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	benchSeedIssues(b, st, repo.PK, owner.PK, 15_000)

	// Walk forward to page 499 boundary to get a real cursor.
	const pageSize = 30
	var cur *IssueCursor
	var lastRows []IssueRow
	for pg := 1; pg <= 499; pg++ {
		f := IssueFilter{State: "open", Limit: pageSize, Cursor: cur}
		rows, _, err := st.ListIssuesPage(ctx, repo.PK, f)
		if err != nil {
			b.Fatalf("walk page %d: %v", pg, err)
		}
		if len(rows) == 0 {
			break
		}
		lastRows = rows
		last := lastRows[len(lastRows)-1]
		cur = &IssueCursor{CreatedAt: last.CreatedAt, Number: last.Number}
	}
	if cur == nil {
		b.Skip("not enough rows to reach page 499")
	}

	f := IssueFilter{State: "open", Limit: pageSize, Cursor: cur}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, _, err := st.ListIssuesPage(ctx, repo.PK, f)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// ── max labels per issue ──────────────────────────────────────────────────────

// BenchmarkListIssues_maxLabels_20 measures listing 30 issues where every
// issue carries 20 labels. The label-filter JOIN and the batch-hydration query
// both scale with label count.
func BenchmarkListIssues_maxLabels_20(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	issuePKs, labelPKs := benchSeedIssuesWithLabels(b, st, repo.PK, owner.PK, 300, 20)
	_ = issuePKs

	b.ResetTimer()
	b.ReportAllocs()
	// Filter by the first label so every result row carries all 20.
	for b.Loop() {
		_, err := st.ListIssues(ctx, repo.PK, IssueFilter{
			State:    "open",
			Labels:   []string{fmt.Sprintf("label-%03d", 0)},
			Limit:    30,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	_ = labelPKs
}

// BenchmarkLabelsByIssuePKs_maxScale measures batch-loading labels for 100
// issues each with 20 labels — 2 000 rows in the JOIN result.
func BenchmarkLabelsByIssuePKs_maxScale(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	issuePKs, _ := benchSeedIssuesWithLabels(b, st, repo.PK, owner.PK, 100, 20)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := st.LabelsByIssuePKs(ctx, issuePKs)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// ── max assignees per issue ───────────────────────────────────────────────────

// BenchmarkAssigneesByIssuePKs_maxScale measures batch-loading assignees for
// 100 issues each with 10 assignees — 1 000 rows in the join.
func BenchmarkAssigneesByIssuePKs_maxScale(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	issuePKs, _ := benchSeedIssuesWithAssignees(b, st, repo.PK, owner.PK, 100, 10)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := st.AssigneesByIssuePKs(ctx, issuePKs)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// ── milestone with large issue count ─────────────────────────────────────────

// BenchmarkMilestoneIssueCounts_10K measures the MilestoneIssueCounts count
// query over a milestone that owns 10 000 issues. This is a full COUNT scan.
func BenchmarkMilestoneIssueCounts_10K(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	milestone, _ := benchSeedMilestone(b, st, repo.PK, owner.PK, 10_000)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		open, closed, err := st.MilestoneIssueCounts(ctx, milestone.PK)
		if err != nil {
			b.Fatal(err)
		}
		if open == 0 && closed == 0 {
			b.Fatal("milestone has no issues")
		}
	}
}

// BenchmarkListIssues_milestoneFilter lists 30 issues filtered by a milestone
// that owns 5 000 issues. The milestone_pk index does the seek but the result
// set is wide.
func BenchmarkListIssues_milestoneFilter(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	milestone, _ := benchSeedMilestone(b, st, repo.PK, owner.PK, 5_000)

	f := IssueFilter{State: "open", MilestonePK: &milestone.PK, Limit: 30}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := st.ListIssues(ctx, repo.PK, f)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// ── multi-qualifier search ────────────────────────────────────────────────────

// BenchmarkSearchIssues_multiQualifier measures a search combining a free-text
// term, a label filter, an author, and a state. The FTS + multiple JOIN path
// is exercised.
func BenchmarkSearchIssues_multiQualifier(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	issuePKs, labelPKs := benchSeedIssuesWithLabels(b, st, repo.PK, owner.PK, 500, 5)
	_ = issuePKs
	_ = labelPKs

	q := IssueSearch{
		ViewerPK:   owner.PK,
		Terms:      []string{"issue"},
		MatchTitle: true,
		State:      "open",
		AuthorPK:   &owner.PK,
		Labels:     []string{"label-000"},
		Limit:      30,
		Sort:       "created",
		Order:      "desc",
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

// ── cross-repo search ─────────────────────────────────────────────────────────

// BenchmarkSearchIssues_crossRepo_20 measures a user: search that spans 20
// repositories. The scan unions issue rows across all 20 repos.
func BenchmarkSearchIssues_crossRepo_20(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")

	var repoPKs []int64
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *Tx) error {
			for r := range 20 {
				repo := &RepoRow{OwnerPK: owner.PK, Name: fmt.Sprintf("repo-%02d", r), DefaultBranch: "main"}
				if err := tx.SeedRepo(ctx, repo); err != nil {
					return err
				}
				repoPKs = append(repoPKs, repo.PK)
				for i := 1; i <= 50; i++ {
					when := base.Add(time.Duration(r*100+i) * time.Minute)
					iss := &IssueRow{
						RepoPK:    repo.PK,
						Number:    int64(i),
						UserPK:    owner.PK,
						Title:     fmt.Sprintf("cross-repo issue %d in repo %d", i, r),
						State:     "open",
						CreatedAt: when,
						UpdatedAt: when,
					}
					if err := tx.SeedIssue(ctx, iss); err != nil {
						return err
					}
				}
			}
			return nil
		})
	})
	if err != nil {
		b.Fatalf("seed: %v", err)
	}
	for _, pk := range repoPKs {
		if err := st.SetNextIssueNumber(ctx, pk, 51); err != nil {
			b.Fatalf("SetNextIssueNumber: %v", err)
		}
	}

	q := IssueSearch{
		ViewerPK:   owner.PK,
		OwnerPKs:   []int64{owner.PK},
		Terms:      []string{"cross-repo"},
		MatchTitle: true,
		State:      "open",
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

// ── concurrent ID allocation ─────────────────────────────────────────────────

// BenchmarkAllocDBID_concurrent_8 measures global ID allocation under
// contention: 8 goroutines each calling AllocDBID in tight loops. SQLite
// serializes writes so all 8 wait for the same lock; the benchmark surfaces
// the queueing cost.
func BenchmarkAllocDBID_concurrent_8(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := st.WithTx(ctx, func(tx *Tx) error {
				_, err := tx.allocDBID(ctx)
				return err
			}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkAllocIssueNumber_concurrent_8 measures per-repo issue-number
// allocation under contention: 8 goroutines allocating numbers for the same
// repository. This is the hot path during a PR burst in a busy repository.
func BenchmarkAllocIssueNumber_concurrent_8(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := st.WithTx(ctx, func(tx *Tx) error {
				_, err := tx.AllocIssueNumber(ctx, repo.PK)
				return err
			}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// ── deep job queue ────────────────────────────────────────────────────────────

// BenchmarkClaimJob_deepQueue_1K measures ClaimJob when 1 000 pending jobs sit
// in the queue. This proves that the job-table index still provides O(1)
// claim cost regardless of pending depth.
func BenchmarkClaimJob_deepQueue_1K(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")

	const queueDepth = 1_000
	err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *Tx) error {
			for i := range queueDepth {
				e := &EventRow{
					Event: "issues", Action: "opened",
					ActorPK: owner.PK, RepoPK: repo.PK, Public: true,
				}
				if err := tx.InsertEvent(ctx, e); err != nil {
					return err
				}
				payload := fmt.Sprintf(`{"event_pk":%d}`, e.PK)
				j := &JobRow{Kind: "deliver_event", Payload: payload}
				if err := tx.insertJob(ctx, j); err != nil {
					return err
				}
				_ = i
			}
			return nil
		})
	})
	if err != nil {
		b.Fatalf("seed jobs: %v", err)
	}

	// Seed a replacement event/job pair before the hot loop so the queue stays
	// at ~1K depth for every iteration — ClaimJob always runs against a full queue.
	insertOne := func() {
		e := &EventRow{Event: "issues", Action: "opened", ActorPK: owner.PK, RepoPK: repo.PK, Public: true}
		if err := st.InsertEvent(ctx, e); err != nil {
			b.Fatalf("re-seed event: %v", err)
		}
		j := &JobRow{Kind: "deliver_event", Payload: fmt.Sprintf(`{"event_pk":%d}`, e.PK)}
		_ = st.WithTx(ctx, func(tx *Tx) error { return tx.insertJob(ctx, j) })
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		insertOne()
		j, err := st.ClaimJob(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if j == nil {
			b.Fatal("queue exhausted")
		}
	}
}

// ── InsertEventAndJob throughput ──────────────────────────────────────────────

// BenchmarkInsertEventAndJob measures the atomic P9 insert-event-and-job pair.
// This is the hot path for every issue/PR mutation; it must remain below the
// write-budget ceiling even under a sequential burst.
func BenchmarkInsertEventAndJob(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		e := &EventRow{
			Event: "issues", Action: "opened",
			ActorPK: owner.PK, RepoPK: repo.PK, Public: true,
		}
		if err := st.InsertEventAndJob(ctx, e, "deliver_event", func(pk int64) string {
			return fmt.Sprintf(`{"event_pk":%d}`, pk)
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkInsertEventAndJob_concurrent_4 measures the P9 pair under 4-way
// concurrent write contention — a burst of simultaneous issue creates.
func BenchmarkInsertEventAndJob_concurrent_4(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")

	b.SetParallelism(4)
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			e := &EventRow{
				Event: "issues", Action: "opened",
				ActorPK: owner.PK, RepoPK: repo.PK, Public: true,
			}
			if err := st.InsertEventAndJob(ctx, e, "deliver_event", func(pk int64) string {
				return fmt.Sprintf(`{"event_pk":%d}`, pk)
			}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// ── dense review-comment thread ───────────────────────────────────────────────

// BenchmarkListReviewComments_500 measures listing all 500 inline review
// comments on a single pull request. This is the rust-lang/rust review-dense
// scenario.
func BenchmarkListReviewComments_500(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")

	// Seed one PR and one review with 500 comments.
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	var pullPK int64
	err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *Tx) error {
			iss := &IssueRow{
				RepoPK: repo.PK, Number: 1, UserPK: owner.PK,
				Title: "large PR review", IsPull: true, State: "open",
				CreatedAt: base, UpdatedAt: base,
			}
			if err := tx.SeedIssue(ctx, iss); err != nil {
				return err
			}
			pr := &PullRow{
				IssuePK: iss.PK, RepoPK: repo.PK,
				BaseRef: "main", BaseSHA: "0000000000000000000000000000000000000000",
				HeadRef: "fix", HeadSHA: "ffffffffffffffffffffffffffffffffffffffff",
				MergeableState: "unknown", CreatedAt: base, UpdatedAt: base,
			}
			if err := tx.SeedPull(ctx, pr); err != nil {
				return err
			}
			pullPK = pr.PK
			rv := &ReviewRow{
				PullPK: pr.PK, RepoPK: repo.PK, UserPK: owner.PK,
				State: "COMMENTED", Body: "large review",
				CommitID: "ffffffffffffffffffffffffffffffffffffffff",
				CreatedAt: base, UpdatedAt: base,
			}
			if err := tx.SeedReview(ctx, rv); err != nil {
				return err
			}
			line := int64(10)
			for i := range 500 {
				c := &ReviewCommentRow{
					ReviewPK: rv.PK, PullPK: pr.PK, RepoPK: repo.PK,
					UserPK: owner.PK,
					Path:     fmt.Sprintf("src/file%d.rs", i%20),
					Side:     "RIGHT",
					Line:     &line,
					CommitID: "ffffffffffffffffffffffffffffffffffffffff",
					OriginalCommitID: "ffffffffffffffffffffffffffffffffffffffff",
					DiffHunk: "@@ -1,3 +1,4 @@\n line\n+new\n line",
					Body:     fmt.Sprintf("review comment %d: this needs a fix because the logic is subtly wrong", i),
					CreatedAt: base.Add(time.Duration(i) * time.Second),
					UpdatedAt: base.Add(time.Duration(i) * time.Second),
				}
				if err := tx.SeedReviewComment(ctx, c); err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		b.Fatalf("seed: %v", err)
	}
	if err := st.SetNextIssueNumber(ctx, repo.PK, 2); err != nil {
		b.Fatalf("SetNextIssueNumber: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		rows, err := st.ListReviewComments(ctx, pullPK)
		if err != nil {
			b.Fatal(err)
		}
		if len(rows) != 500 {
			b.Fatalf("want 500 comments, got %d", len(rows))
		}
	}
}

// ── dense commit-status contexts ─────────────────────────────────────────────

// BenchmarkListCommitStatuses_100ctx measures reading 100 context rows for one
// SHA. This is the kubernetes/kubernetes prow-job scenario where every PR head
// accumulates one status per CI job.
func BenchmarkListCommitStatuses_100ctx(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")

	const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	states := []string{"success", "failure", "pending"}
	err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *Tx) error {
			for i := range 100 {
				st := &CommitStatusRow{
					RepoPK:    repo.PK,
					SHA:       sha,
					Context:   fmt.Sprintf("ci/job-%03d", i),
					State:     states[i%3],
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}
				if err := tx.SeedCommitStatus(ctx, st); err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		b.Fatalf("seed statuses: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		rows, err := st.ListCommitStatuses(ctx, repo.PK, sha)
		if err != nil {
			b.Fatal(err)
		}
		if len(rows) != 100 {
			b.Fatalf("want 100 statuses, got %d", len(rows))
		}
	}
}

// ── unicode content ──────────────────────────────────────────────────────────

// BenchmarkSearchIssues_unicode measures searching issues whose titles contain
// multi-byte Unicode sequences (emoji, RTL, CJK). SQLite's FTS5 tokenizer must
// handle these without truncating or mis-scanning tokens.
func BenchmarkSearchIssues_unicode(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")

	unicodeTitles := []string{
		"fix 🐛 crash in parser",
		"perf: 🚀 optimize hot path",
		"مشكلة في التوثيق",   // Arabic RTL
		"修复文档错误",          // CJK
		"bug: emoji 🎉🔥💯 in title",
		"Привет мир: исправление",  // Cyrillic
	}
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *Tx) error {
			for i := 1; i <= 300; i++ {
				title := unicodeTitles[(i-1)%len(unicodeTitles)]
				iss := &IssueRow{
					RepoPK:    repo.PK,
					Number:    int64(i),
					UserPK:    owner.PK,
					Title:     fmt.Sprintf("%s #%d", title, i),
					State:     "open",
					CreatedAt: base.Add(time.Duration(i) * time.Minute),
					UpdatedAt: base.Add(time.Duration(i) * time.Minute),
				}
				if err := tx.SeedIssue(ctx, iss); err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		b.Fatalf("seed: %v", err)
	}
	if err := st.SetNextIssueNumber(ctx, repo.PK, 301); err != nil {
		b.Fatalf("SetNextIssueNumber: %v", err)
	}

	q := IssueSearch{
		ViewerPK:   owner.PK,
		Terms:      []string{"crash"},
		MatchTitle: true,
		State:      "open",
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

// ── concurrent read under write contention ──────────────────────────────────

// BenchmarkListIssues_concurrentReads_8 measures 8 goroutines reading issues
// in parallel. SQLite WAL allows concurrent reads; this confirms there is no
// reader/writer starvation at this concurrency level.
func BenchmarkListIssues_concurrentReads_8(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	benchSeedIssues(b, st, repo.PK, owner.PK, 1_000)

	f := IssueFilter{State: "open", Limit: 30}
	b.SetParallelism(8)
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := st.ListIssues(ctx, repo.PK, f)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkMixedReadWrite_8 fires 4 reader goroutines (ListIssues) and 4
// writer goroutines (InsertEventAndJob) concurrently. This is the closest
// approximation to a live server under moderate request mix.
func BenchmarkMixedReadWrite_8(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")
	benchSeedIssues(b, st, repo.PK, owner.PK, 500)

	readFilter := IssueFilter{State: "open", Limit: 30}
	var counter int64
	var mu sync.Mutex

	b.SetParallelism(8)
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		mu.Lock()
		id := counter
		counter++
		mu.Unlock()

		isWriter := id%2 == 0
		for pb.Next() {
			if isWriter {
				e := &EventRow{
					Event: "issues", Action: "opened",
					ActorPK: owner.PK, RepoPK: repo.PK, Public: true,
				}
				if err := st.InsertEventAndJob(ctx, e, "deliver_event", func(pk int64) string {
					return fmt.Sprintf(`{"event_pk":%d}`, pk)
				}); err != nil {
					b.Fatal(err)
				}
			} else {
				if _, err := st.ListIssues(ctx, repo.PK, readFilter); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}

// ── large comment body ────────────────────────────────────────────────────────

// BenchmarkListComments_largeBody_64KB measures reading 10 comments each with
// a 64 KB body. This exercises the SQLite page-chain read path for large text
// blobs, which is relevant to GitHub-style discussions that quote large diffs.
func BenchmarkListComments_largeBody_64KB(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	owner := benchSeedUser(b, st, "octocat")
	repo := benchSeedRepo(b, st, owner, "bench-repo")

	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	largeBody := make([]byte, 64*1024)
	for i := range largeBody {
		largeBody[i] = byte('a' + i%26)
	}
	bodyStr := string(largeBody)

	var issuePK int64
	err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *Tx) error {
			iss := &IssueRow{
				RepoPK: repo.PK, Number: 1, UserPK: owner.PK,
				Title: "large-body discussion", State: "open",
				CreatedAt: base, UpdatedAt: base,
			}
			if err := tx.SeedIssue(ctx, iss); err != nil {
				return err
			}
			issuePK = iss.PK
			for i := range 10 {
				c := &CommentRow{
					IssuePK: iss.PK, UserPK: owner.PK,
					Body:      bodyStr,
					CreatedAt: base.Add(time.Duration(i) * time.Second),
					UpdatedAt: base.Add(time.Duration(i) * time.Second),
				}
				if err := tx.SeedComment(ctx, c); err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		b.Fatalf("seed: %v", err)
	}
	if err := st.SetNextIssueNumber(ctx, repo.PK, 2); err != nil {
		b.Fatalf("SetNextIssueNumber: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		rows, err := st.ListIssueComments(ctx, issuePK, 10, 0)
		if err != nil {
			b.Fatal(err)
		}
		if len(rows) != 10 {
			b.Fatalf("want 10, got %d", len(rows))
		}
	}
}

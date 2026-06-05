package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamnd/githome/store"
)

// seedReview opens a submitted review on a pull request, the row a review and its
// inline comments are written against.
func seedReview(t *testing.T, st *store.Store, repo *store.RepoRow, pr *store.PullRow, state string) *store.ReviewRow {
	t.Helper()
	ctx := context.Background()
	r := &store.ReviewRow{
		PullPK:      pr.PK,
		RepoPK:      repo.PK,
		UserPK:      repo.OwnerPK,
		State:       state,
		Body:        "looks good",
		CommitID:    pr.HeadSHA,
		SubmittedAt: ptrTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
	}
	if err := st.WithTx(ctx, func(tx *store.Tx) error {
		return tx.InsertReview(ctx, r)
	}); err != nil {
		t.Fatalf("seedReview: %v", err)
	}
	return r
}

func ptrTime(t time.Time) *time.Time { return &t }
func ptrI64(v int64) *int64          { return &v }
func ptrStr(s string) *string        { return &s }

func TestReviewCreateAndList(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})
		_, pr := seedPull(t, st, repo, "add a feature", "main", "feature")

		approved := seedReview(t, st, repo, pr, "APPROVED")
		if approved.PK == 0 || approved.DBID == 0 || approved.CreatedAt.IsZero() {
			t.Fatalf("server fields not filled: %+v", approved)
		}

		byDBID, err := st.GetReviewByDBID(ctx, approved.DBID)
		if err != nil || byDBID.PK != approved.PK {
			t.Fatalf("GetReviewByDBID: %v row=%+v", err, byDBID)
		}
		if byDBID.State != "APPROVED" || byDBID.SubmittedAt == nil {
			t.Errorf("round-trip mismatch: %+v", byDBID)
		}

		reviews, err := st.ListReviews(ctx, pr.PK)
		if err != nil {
			t.Fatalf("ListReviews: %v", err)
		}
		if len(reviews) != 1 {
			t.Fatalf("ListReviews returned %d, want 1", len(reviews))
		}

		if _, err := st.GetReviewByDBID(ctx, 999999); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("missing review: err = %v, want ErrNotFound", err)
		}
	})
}

func TestPendingReviewIsPrivateUntilSubmitted(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})
		_, pr := seedPull(t, st, repo, "add a feature", "main", "feature")

		// A pending draft (no submitted_at) is found by the author lookup but is
		// excluded from the public review list.
		pending := &store.ReviewRow{PullPK: pr.PK, RepoPK: repo.PK, UserPK: repo.OwnerPK, State: "PENDING"}
		if err := st.WithTx(ctx, func(tx *store.Tx) error { return tx.InsertReview(ctx, pending) }); err != nil {
			t.Fatalf("insert pending: %v", err)
		}

		got, err := st.PendingReviewFor(ctx, pr.PK, repo.OwnerPK)
		if err != nil || got.PK != pending.PK {
			t.Fatalf("PendingReviewFor: %v row=%+v", err, got)
		}
		reviews, err := st.ListReviews(ctx, pr.PK)
		if err != nil {
			t.Fatalf("ListReviews: %v", err)
		}
		if len(reviews) != 0 {
			t.Errorf("pending draft leaked into list: %+v", reviews)
		}

		// Submitting flips it to a real review the list shows.
		if err := st.WithTx(ctx, func(tx *store.Tx) error {
			return tx.SubmitReview(ctx, pending.PK, "APPROVED", "ship it", pr.HeadSHA, time.Now().UTC())
		}); err != nil {
			t.Fatalf("SubmitReview: %v", err)
		}
		if _, err := st.PendingReviewFor(ctx, pr.PK, repo.OwnerPK); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("after submit PendingReviewFor err = %v, want ErrNotFound", err)
		}
		reviews, _ = st.ListReviews(ctx, pr.PK)
		if len(reviews) != 1 || reviews[0].State != "APPROVED" {
			t.Errorf("after submit list = %+v", reviews)
		}
	})
}

func TestReviewCommentThreadResolve(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})
		_, pr := seedPull(t, st, repo, "add a feature", "main", "feature")
		review := seedReview(t, st, repo, pr, "COMMENTED")

		// A root comment and a reply form one thread.
		root := &store.ReviewCommentRow{
			ReviewPK: review.PK, PullPK: pr.PK, RepoPK: repo.PK, UserPK: repo.OwnerPK,
			Path: "feature.txt", Side: "RIGHT", Line: ptrI64(1), CommitID: pr.HeadSHA,
			DiffHunk: "@@ -0,0 +1 @@", Body: "nit: rename this",
		}
		if err := st.WithTx(ctx, func(tx *store.Tx) error { return tx.InsertReviewComment(ctx, root) }); err != nil {
			t.Fatalf("insert root: %v", err)
		}
		reply := &store.ReviewCommentRow{
			ReviewPK: review.PK, PullPK: pr.PK, RepoPK: repo.PK, UserPK: repo.OwnerPK,
			Path: "feature.txt", Side: "RIGHT", Line: ptrI64(1), CommitID: pr.HeadSHA,
			InReplyToPK: ptrI64(root.PK), Body: "done",
		}
		if err := st.WithTx(ctx, func(tx *store.Tx) error { return tx.InsertReviewComment(ctx, reply) }); err != nil {
			t.Fatalf("insert reply: %v", err)
		}

		comments, err := st.ListReviewComments(ctx, pr.PK)
		if err != nil {
			t.Fatalf("ListReviewComments: %v", err)
		}
		if len(comments) != 2 {
			t.Fatalf("ListReviewComments returned %d, want 2", len(comments))
		}

		// Resolving the root resolves the whole thread, reply included.
		if err := st.SetThreadResolved(ctx, root.PK, true, ptrI64(repo.OwnerPK)); err != nil {
			t.Fatalf("SetThreadResolved: %v", err)
		}
		comments, _ = st.ListReviewComments(ctx, pr.PK)
		for _, c := range comments {
			if !c.Resolved {
				t.Errorf("comment %d not resolved after thread resolve", c.PK)
			}
			if c.ResolvedByPK == nil || *c.ResolvedByPK != repo.OwnerPK {
				t.Errorf("comment %d resolved_by = %v, want %d", c.PK, c.ResolvedByPK, repo.OwnerPK)
			}
		}

		// Unresolving clears the flag and the resolver.
		if err := st.SetThreadResolved(ctx, root.PK, false, nil); err != nil {
			t.Fatalf("SetThreadResolved unresolve: %v", err)
		}
		comments, _ = st.ListReviewComments(ctx, pr.PK)
		for _, c := range comments {
			if c.Resolved || c.ResolvedByPK != nil {
				t.Errorf("comment %d still resolved after unresolve: %+v", c.PK, c)
			}
		}
	})
}

func TestCommitStatusesAndCheckRuns(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})
		sha := "2222222222222222222222222222222222222222"

		// Two reports against the same context: the newest sorts first.
		for _, state := range []string{"pending", "success"} {
			s := &store.CommitStatusRow{RepoPK: repo.PK, SHA: sha, State: state, Context: "ci/test", CreatorPK: ptrI64(repo.OwnerPK)}
			if err := st.InsertCommitStatus(ctx, s); err != nil {
				t.Fatalf("InsertCommitStatus: %v", err)
			}
		}
		statuses, err := st.ListCommitStatuses(ctx, repo.PK, sha)
		if err != nil {
			t.Fatalf("ListCommitStatuses: %v", err)
		}
		if len(statuses) != 2 || statuses[0].State != "success" {
			t.Fatalf("statuses = %+v, want newest (success) first", statuses)
		}

		// A check run lives in a suite resolved by (repo, head, app); two ensures
		// for the same head return the same suite.
		suite, err := st.EnsureCheckSuite(ctx, repo.PK, sha, "githome")
		if err != nil {
			t.Fatalf("EnsureCheckSuite: %v", err)
		}
		again, err := st.EnsureCheckSuite(ctx, repo.PK, sha, "githome")
		if err != nil || again.PK != suite.PK {
			t.Fatalf("EnsureCheckSuite not idempotent: %v %+v", err, again)
		}
		run := &store.CheckRunRow{
			SuitePK: suite.PK, RepoPK: repo.PK, HeadSHA: sha, Name: "build",
			Status: "in_progress", StartedAt: ptrTime(time.Now().UTC()),
		}
		if err := st.InsertCheckRun(ctx, run); err != nil {
			t.Fatalf("InsertCheckRun: %v", err)
		}
		run.Status = "completed"
		run.Conclusion = ptrStr("success")
		run.CompletedAt = ptrTime(time.Now().UTC())
		if err := st.UpdateCheckRun(ctx, run); err != nil {
			t.Fatalf("UpdateCheckRun: %v", err)
		}
		got, err := st.GetCheckRun(ctx, run.DBID)
		if err != nil {
			t.Fatalf("GetCheckRun: %v", err)
		}
		if got.Status != "completed" || got.Conclusion == nil || *got.Conclusion != "success" {
			t.Errorf("run round-trip = %+v", got)
		}
		runs, err := st.ListCheckRunsForRef(ctx, repo.PK, sha)
		if err != nil || len(runs) != 1 {
			t.Fatalf("ListCheckRunsForRef: %v len=%d", err, len(runs))
		}
	})
}

func TestPullCheckStateUpsert(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})
		_, pr := seedPull(t, st, repo, "add a feature", "main", "feature")

		if _, err := st.GetPullCheckState(ctx, pr.PK); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("fresh GetPullCheckState err = %v, want ErrNotFound", err)
		}
		now := time.Now().UTC()
		if err := st.UpsertPullCheckState(ctx, pr.PK, ptrStr("APPROVED"), "SUCCESS", now); err != nil {
			t.Fatalf("UpsertPullCheckState insert: %v", err)
		}
		got, err := st.GetPullCheckState(ctx, pr.PK)
		if err != nil || got.ReviewDecision == nil || *got.ReviewDecision != "APPROVED" || got.RollupState != "SUCCESS" {
			t.Fatalf("after insert = %+v err=%v", got, err)
		}
		// A second upsert overwrites in place.
		if err := st.UpsertPullCheckState(ctx, pr.PK, nil, "PENDING", now); err != nil {
			t.Fatalf("UpsertPullCheckState update: %v", err)
		}
		got, _ = st.GetPullCheckState(ctx, pr.PK)
		if got.ReviewDecision != nil || got.RollupState != "PENDING" {
			t.Errorf("after update = %+v", got)
		}
	})
}

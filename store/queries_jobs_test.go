package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/tamnd/githome/store"
)

func TestEnqueueJobAndList(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		j := &store.JobRow{Kind: "push_event", Payload: `{"repo":1}`}
		deduped, err := st.EnqueueJob(ctx, j)
		if err != nil {
			t.Fatalf("EnqueueJob: %v", err)
		}
		if deduped {
			t.Fatal("first enqueue must not be deduped")
		}
		if j.PK == 0 || j.State != "queued" || j.MaxAttempts == 0 {
			t.Fatalf("server fields not filled back: %+v", j)
		}

		// An empty payload is normalized to an empty JSON object.
		empty := &store.JobRow{Kind: "reindex_search"}
		if _, err := st.EnqueueJob(ctx, empty); err != nil {
			t.Fatalf("EnqueueJob empty payload: %v", err)
		}

		jobs, err := st.ListJobs(ctx)
		if err != nil {
			t.Fatalf("ListJobs: %v", err)
		}
		if len(jobs) != 2 {
			t.Fatalf("got %d jobs, want 2", len(jobs))
		}
		if jobs[1].Payload != "{}" {
			t.Fatalf("empty payload = %q, want {}", jobs[1].Payload)
		}
	})
}

func TestEnqueueJobDedupe(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		key := "mergeability:pr:7"
		first, err := st.EnqueueJob(ctx, &store.JobRow{Kind: "recompute_mergeability", DedupeKey: key})
		if err != nil {
			t.Fatalf("first EnqueueJob: %v", err)
		}
		if first {
			t.Fatal("first enqueue of a key must not be deduped")
		}
		second, err := st.EnqueueJob(ctx, &store.JobRow{Kind: "recompute_mergeability", DedupeKey: key})
		if err != nil {
			t.Fatalf("second EnqueueJob: %v", err)
		}
		if !second {
			t.Fatal("second enqueue of an active key must be deduped")
		}

		jobs, err := st.ListJobs(ctx)
		if err != nil {
			t.Fatalf("ListJobs: %v", err)
		}
		if len(jobs) != 1 {
			t.Fatalf("dedupe left %d jobs, want 1", len(jobs))
		}
	})
}

func TestClaimRunSettle(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		// An empty queue claims nothing.
		if _, err := st.ClaimJob(ctx); err != store.ErrNotFound {
			t.Fatalf("claim empty queue: got %v, want ErrNotFound", err)
		}

		a := &store.JobRow{Kind: "recompute_mergeability", Payload: `{"issue_pk":1}`}
		b := &store.JobRow{Kind: "recompute_mergeability", Payload: `{"issue_pk":2}`}
		if _, err := st.EnqueueJob(ctx, a); err != nil {
			t.Fatalf("enqueue a: %v", err)
		}
		if _, err := st.EnqueueJob(ctx, b); err != nil {
			t.Fatalf("enqueue b: %v", err)
		}

		// Two claims drain the two queued jobs; a third finds nothing.
		first, err := st.ClaimJob(ctx)
		if err != nil {
			t.Fatalf("first claim: %v", err)
		}
		if first.State != "running" || first.Attempts != 1 {
			t.Fatalf("claimed job = %+v, want running with attempt 1", first)
		}
		second, err := st.ClaimJob(ctx)
		if err != nil {
			t.Fatalf("second claim: %v", err)
		}
		if first.PK == second.PK {
			t.Fatal("two claims returned the same job")
		}
		if _, err := st.ClaimJob(ctx); err != store.ErrNotFound {
			t.Fatalf("third claim: got %v, want ErrNotFound", err)
		}

		// Completing the first removes it; failing the second with attempts to
		// spare requeues it for the future, so it is not immediately claimable.
		if err := st.CompleteJob(ctx, first.PK); err != nil {
			t.Fatalf("complete: %v", err)
		}
		if err := st.FailJob(ctx, second.PK, second.Attempts, second.MaxAttempts, "boom", 60); err != nil {
			t.Fatalf("fail (retry): %v", err)
		}
		if _, err := st.ClaimJob(ctx); err != store.ErrNotFound {
			t.Fatalf("claim during backoff: got %v, want ErrNotFound", err)
		}

		// Exhausting attempts parks the job dead, never claimable again.
		if err := st.FailJob(ctx, second.PK, second.MaxAttempts, second.MaxAttempts, "boom", 60); err != nil {
			t.Fatalf("fail (dead): %v", err)
		}
		jobs, err := st.ListJobs(ctx)
		if err != nil {
			t.Fatalf("ListJobs: %v", err)
		}
		if len(jobs) != 1 || jobs[0].State != "dead" {
			t.Fatalf("after settle, jobs = %+v, want one dead job", jobs)
		}
	})
}

func TestTouchRepoPushedAt(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		u := &store.UserRow{Login: "octocat", Type: "User"}
		if err := st.InsertUser(ctx, u); err != nil {
			t.Fatalf("InsertUser: %v", err)
		}
		repo := &store.RepoRow{OwnerPK: u.PK, Name: "hello"}
		if err := st.InsertRepo(ctx, repo); err != nil {
			t.Fatalf("InsertRepo: %v", err)
		}
		if repo.PushedAt != nil {
			t.Fatalf("fresh repo pushed_at = %v, want nil", repo.PushedAt)
		}

		when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
		if err := st.TouchRepoPushedAt(ctx, repo.PK, when); err != nil {
			t.Fatalf("TouchRepoPushedAt: %v", err)
		}

		got, err := st.RepoByPK(ctx, repo.PK)
		if err != nil {
			t.Fatalf("RepoByPK: %v", err)
		}
		if got.PushedAt == nil || !got.PushedAt.Equal(when) {
			t.Fatalf("pushed_at = %v, want %v", got.PushedAt, when)
		}
	})
}

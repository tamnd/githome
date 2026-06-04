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

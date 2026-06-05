package worker_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tamnd/githome/store"
	"github.com/tamnd/githome/worker"
)

// openStore builds a fresh sqlite-backed store for a runtime test.
func openStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "githome.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return st
}

func TestRuntimeRunsAndCompletes(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	rt := worker.NewRuntime(st, nil, 0)

	var seen []string
	rt.Register("recompute_mergeability", func(_ context.Context, job store.JobRow) error {
		seen = append(seen, job.Payload)
		return nil
	})

	if _, err := st.EnqueueJob(ctx, &store.JobRow{Kind: "recompute_mergeability", Payload: `{"issue_pk":1}`}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	worked, err := rt.RunOnce(ctx)
	if err != nil || !worked {
		t.Fatalf("RunOnce: worked=%v err=%v", worked, err)
	}
	if len(seen) != 1 || seen[0] != `{"issue_pk":1}` {
		t.Fatalf("handler saw %v, want one issue_pk=1 payload", seen)
	}

	// The completed job is gone, so the next drain finds nothing.
	worked, err = rt.RunOnce(ctx)
	if err != nil || worked {
		t.Fatalf("second RunOnce: worked=%v err=%v, want idle", worked, err)
	}
	jobs, _ := st.ListJobs(ctx)
	if len(jobs) != 0 {
		t.Fatalf("completed job left behind: %+v", jobs)
	}
}

func TestRuntimeRetriesThenDies(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	rt := worker.NewRuntime(st, nil, 0)

	rt.Register("flaky", func(_ context.Context, _ store.JobRow) error {
		return errors.New("always fails")
	})
	if _, err := st.EnqueueJob(ctx, &store.JobRow{Kind: "flaky"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// First run fails and requeues with backoff: the job survives, not dead yet.
	if _, err := rt.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	jobs, _ := st.ListJobs(ctx)
	if len(jobs) != 1 || jobs[0].State != "queued" {
		t.Fatalf("after one failure, jobs = %+v, want one queued", jobs)
	}
	if jobs[0].State == "dead" {
		t.Fatal("a job with attempts left must not be dead")
	}
}

func TestRuntimeNoHandlerParksJob(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	rt := worker.NewRuntime(st, nil, 0)

	if _, err := st.EnqueueJob(ctx, &store.JobRow{Kind: "unregistered"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := rt.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	jobs, _ := st.ListJobs(ctx)
	if len(jobs) != 1 || jobs[0].State != "dead" {
		t.Fatalf("unhandled job = %+v, want one dead job", jobs)
	}
}

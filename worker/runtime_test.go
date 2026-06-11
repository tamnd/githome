package worker_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

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

func TestRuntimeWorkersBypassSlowJob(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st := openStore(t)
	rt := worker.NewRuntime(st, nil, time.Millisecond)
	rt.SetWorkers(2)

	// The slow job parks until the fast one has run. With one claim loop this
	// deadlocks (the loop is stuck inside the slow handler and never claims
	// the fast job); a second loop slips past it.
	fastRan := make(chan struct{})
	rt.Register("slow", func(ctx context.Context, _ store.JobRow) error {
		select {
		case <-fastRan:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	rt.Register("fast", func(_ context.Context, _ store.JobRow) error {
		close(fastRan)
		return nil
	})

	if _, err := st.EnqueueJob(ctx, &store.JobRow{Kind: "slow"}); err != nil {
		t.Fatalf("enqueue slow: %v", err)
	}
	if _, err := st.EnqueueJob(ctx, &store.JobRow{Kind: "fast"}); err != nil {
		t.Fatalf("enqueue fast: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = rt.Run(ctx)
		close(done)
	}()

	// Both jobs complete only if the fast one ran around the slow one.
	for {
		jobs, err := st.ListJobs(ctx)
		if err != nil {
			t.Fatalf("ListJobs: %v", err)
		}
		if len(jobs) == 0 {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("queue never drained: %+v", jobs)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}

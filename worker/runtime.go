package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/tamnd/githome/store"
)

// The run loop that drains the queue the enqueue seam fills. A single Runtime
// claims one runnable job at a time, dispatches it to the handler registered for
// its kind, and either completes it or fails it with backoff. Claiming leases
// the job atomically in the store, so several Runtimes against one Postgres can
// run side by side; against SQLite the single writer serializes them. The loop
// owns no timers a test cannot control: RunOnce drains exactly one job, and Run
// is RunOnce plus an idle sleep.

// Handler runs one job of a given kind. A nil error completes the job; a
// non-nil error fails it, scheduling a retry until its attempts run out.
type Handler func(ctx context.Context, job store.JobRow) error

// RunStore is the slice of the store the run loop drives the queue through: the
// atomic claim, the completion delete, and the failure requeue.
type RunStore interface {
	ClaimJob(ctx context.Context) (*store.JobRow, error)
	CompleteJob(ctx context.Context, pk int64) error
	FailJob(ctx context.Context, pk int64, attempts, maxAttempts int, reason string, backoffSeconds int) error
}

// Runtime is the queue's consumer: a handler registry plus the claim-run-settle
// loop over the store.
type Runtime struct {
	store    RunStore
	log      *slog.Logger
	handlers map[string]Handler
	idle     time.Duration
	workers  int
}

// NewRuntime builds a Runtime over the store. idle is how long Run waits before
// polling again when the queue is empty; a zero or negative value uses a one
// second default.
func NewRuntime(st RunStore, log *slog.Logger, idle time.Duration) *Runtime {
	if log == nil {
		log = slog.Default()
	}
	if idle <= 0 {
		idle = time.Second
	}
	return &Runtime{store: st, log: log, handlers: map[string]Handler{}, idle: idle}
}

// Register binds a handler to a job kind. A second registration for the same
// kind replaces the first, which keeps wiring order from mattering.
func (r *Runtime) Register(kind string, h Handler) {
	r.handlers[kind] = h
}

// SetWorkers sets how many claim loops Run drives. One slow handler (a webhook
// delivery waiting out a dead endpoint) used to head-of-line-block every other
// job; with n loops the queue keeps moving around it. Values below one are
// treated as one. Jobs may run concurrently and so may two deliveries to the
// same hook, which matches the at-least-once, unordered contract the retry
// path already imposes on consumers.
func (r *Runtime) SetWorkers(n int) {
	if n < 1 {
		n = 1
	}
	r.workers = n
}

// Run drains the queue until ctx is canceled, sleeping idle between polls when
// it finds nothing to do. It returns ctx.Err() on cancellation, the normal way
// a graceful shutdown ends it. With SetWorkers above one it drives that many
// claim loops; the store's atomic claim keeps them off each other's jobs.
func (r *Runtime) Run(ctx context.Context) error {
	n := r.workers
	if n < 1 {
		n = 1
	}
	if n == 1 {
		return r.runLoop(ctx)
	}
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.runLoop(ctx)
		}()
	}
	wg.Wait()
	return ctx.Err()
}

// runLoop is one claim-run-settle loop, the body Run fans out.
func (r *Runtime) runLoop(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		worked, err := r.RunOnce(ctx)
		if err != nil {
			r.log.Error("worker run", "error", err)
		}
		if worked {
			continue // drain greedily while work remains
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.idle):
		}
	}
}

// RunOnce claims and runs a single job. It reports worked=false when the queue
// held nothing runnable, the signal Run uses to switch to its idle sleep. A
// handler panic or error fails the job with backoff rather than killing the
// loop; a missing handler fails it permanently, since no retry will ever find
// one.
func (r *Runtime) RunOnce(ctx context.Context) (worked bool, err error) {
	job, err := r.store.ClaimJob(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	h, ok := r.handlers[job.Kind]
	if !ok {
		// No handler: park it permanently by exhausting its attempts.
		r.log.Warn("worker: no handler", "kind", job.Kind, "job", job.PK)
		return true, r.store.FailJob(ctx, job.PK, job.MaxAttempts, job.MaxAttempts, "no handler for kind "+job.Kind, 0)
	}

	runErr := runGuarded(ctx, h, *job)
	if runErr != nil {
		r.log.Error("worker: job failed", "kind", job.Kind, "job", job.PK, "attempt", job.Attempts, "error", runErr)
		return true, r.store.FailJob(ctx, job.PK, job.Attempts, job.MaxAttempts, runErr.Error(), backoffSeconds(job.Attempts))
	}
	return true, r.store.CompleteJob(ctx, job.PK)
}

// runGuarded runs a handler, turning a panic into an error so one bad job cannot
// take the loop down with it.
func runGuarded(ctx context.Context, h Handler, job store.JobRow) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = errors.New("worker: handler panicked")
		}
	}()
	return h(ctx, job)
}

// backoffSeconds is the exponential retry delay for a job's nth attempt, doubling
// from two seconds and capping at five minutes so a stuck job neither hammers the
// queue nor stalls forever.
func backoffSeconds(attempts int) int {
	const maxBackoff = 300
	d := 1
	for i := 0; i < attempts && d < maxBackoff; i++ {
		d *= 2
	}
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}

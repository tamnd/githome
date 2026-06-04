// Package worker is the background job queue: the enqueue API job producers
// submit through, and (from a later milestone) the claim-and-run loop that
// drains it. M3 introduces only the enqueue seam, which the post-receive push
// sink uses to record push events and search reindexes; the run loop and the
// per-kind handlers land with the milestones that consume each job kind.
package worker

import (
	"context"

	"github.com/tamnd/githome/store"
)

// Enqueuer accepts a background job. kind names the handler that will run it,
// payload is the job's JSON arguments (empty means an empty object), and
// dedupeKey, when non-empty, collapses jobs with the same key while one is still
// queued or running so a burst of triggers does not pile up redundant work. It
// reports deduped=true when an active job with the same key already existed and
// this submission was folded into it.
type Enqueuer interface {
	Enqueue(ctx context.Context, kind, payload, dedupeKey string) (deduped bool, err error)
}

// JobStore is the slice of the store the enqueuer writes through: a single
// queue insert with dedupe handling. The store satisfies it directly.
type JobStore interface {
	EnqueueJob(ctx context.Context, j *store.JobRow) (bool, error)
}

// StoreEnqueuer is the store-backed Enqueuer: it persists each job as a row in
// the jobs table so the work survives a restart and any process running the
// claim loop can pick it up.
type StoreEnqueuer struct {
	store JobStore
}

// NewStoreEnqueuer builds a StoreEnqueuer over the job store.
func NewStoreEnqueuer(st JobStore) *StoreEnqueuer {
	return &StoreEnqueuer{store: st}
}

// Enqueue inserts the job into the queue, folding it into an active job with the
// same dedupe key when one exists.
func (e *StoreEnqueuer) Enqueue(ctx context.Context, kind, payload, dedupeKey string) (bool, error) {
	return e.store.EnqueueJob(ctx, &store.JobRow{Kind: kind, Payload: payload, DedupeKey: dedupeKey})
}

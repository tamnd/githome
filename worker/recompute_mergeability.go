package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tamnd/githome/store"
)

// The recompute_mergeability handler. The pull request service produces these
// jobs when a pull request opens or its base or head moves, and computes the
// merge state when one runs. The handler stays on the store side of the
// dependency line: it decodes the job payload and dispatches into the recomputer
// the wiring passes in, which the pull request service satisfies, so the worker
// package never imports domain.

// MergeabilityRecomputer computes and persists a pull request's merge state for
// the issue that backs it. The domain pull request service implements it.
type MergeabilityRecomputer interface {
	RecomputeMergeability(ctx context.Context, issuePK int64) error
}

// recomputePayload mirrors the job body the producer writes; the worker decodes
// its own copy rather than importing the domain that marshals it.
type recomputePayload struct {
	IssuePK int64 `json:"issue_pk"`
}

// RecomputeMergeabilityHandler binds the recompute_mergeability kind to the
// recomputer. A payload missing its issue_pk is a permanent error, since no
// retry can repair a malformed job.
func RecomputeMergeabilityHandler(rec MergeabilityRecomputer) Handler {
	return func(ctx context.Context, job store.JobRow) error {
		var p recomputePayload
		if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
			return fmt.Errorf("recompute_mergeability: bad payload: %w", err)
		}
		if p.IssuePK == 0 {
			return fmt.Errorf("recompute_mergeability: missing issue_pk")
		}
		return rec.RecomputeMergeability(ctx, p.IssuePK)
	}
}

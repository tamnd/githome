package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tamnd/githome/store"
)

// The recompute_review_decision handler. The review and checks services produce
// these jobs when a review is submitted or dismissed, a reply lands, or a status
// or check is reported against a pull request's head; running one resolves and
// caches the pull request's review decision and status check rollup. Like the
// mergeability handler it stays on the store side of the dependency line: it
// decodes the job payload and dispatches into the recomputer the wiring passes in,
// so the worker package never imports domain.

// ReviewDecisionRecomputer resolves and caches a pull request's review decision
// and status check rollup for the issue that backs it. The domain review service
// implements it.
type ReviewDecisionRecomputer interface {
	RecomputeReviewDecision(ctx context.Context, issuePK int64) error
}

// RecomputeReviewDecisionHandler binds the recompute_review_decision kind to the
// recomputer. A payload missing its issue_pk is a permanent error, since no retry
// can repair a malformed job. It reuses recomputePayload, the shared issue_pk job
// body the mergeability handler also decodes.
func RecomputeReviewDecisionHandler(rec ReviewDecisionRecomputer) Handler {
	return func(ctx context.Context, job store.JobRow) error {
		var p recomputePayload
		if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
			return fmt.Errorf("recompute_review_decision: bad payload: %w", err)
		}
		if p.IssuePK == 0 {
			return fmt.Errorf("recompute_review_decision: missing issue_pk")
		}
		return rec.RecomputeReviewDecision(ctx, p.IssuePK)
	}
}

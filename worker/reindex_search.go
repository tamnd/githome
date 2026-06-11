package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tamnd/githome/store"
)

// The reindex_search handler. The push path enqueues these jobs when a
// repository's default branch moves; the search service rebuilds the
// repository's code index when one runs. As with the mergeability handler the
// worker decodes its own payload copy and dispatches into the interface the
// wiring passes in, so the worker package never imports domain.

// CodeReindexer rebuilds a repository's code search index from its current
// head. The domain search service implements it.
type CodeReindexer interface {
	ReindexRepoCode(ctx context.Context, repoPK int64) error
}

// reindexPayload mirrors the job body the push path writes.
type reindexPayload struct {
	RepoPK int64 `json:"repo_pk"`
}

// ReindexSearchHandler binds the reindex_search kind to the reindexer. A
// payload missing its repo_pk is a permanent error, since no retry can repair
// a malformed job.
func ReindexSearchHandler(rx CodeReindexer) Handler {
	return func(ctx context.Context, job store.JobRow) error {
		var p reindexPayload
		if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
			return fmt.Errorf("reindex_search: bad payload: %w", err)
		}
		if p.RepoPK == 0 {
			return fmt.Errorf("reindex_search: missing repo_pk")
		}
		return rx.ReindexRepoCode(ctx, p.RepoPK)
	}
}

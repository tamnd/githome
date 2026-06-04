package worker_test

import (
	"context"
	"testing"

	"github.com/tamnd/githome/store"
	"github.com/tamnd/githome/worker"
)

// recordingStore is a fake JobStore that records every enqueued row and honors
// dedupe keys, standing in for the real queue so the test asserts exactly what a
// producer submits.
type recordingStore struct {
	rows   []store.JobRow
	active map[string]bool
}

func (s *recordingStore) EnqueueJob(_ context.Context, j *store.JobRow) (bool, error) {
	if j.DedupeKey != "" {
		if s.active[j.DedupeKey] {
			return true, nil
		}
		if s.active == nil {
			s.active = map[string]bool{}
		}
		s.active[j.DedupeKey] = true
	}
	s.rows = append(s.rows, *j)
	return false, nil
}

func TestStoreEnqueuerForwards(t *testing.T) {
	ctx := context.Background()
	rec := &recordingStore{}
	enq := worker.NewStoreEnqueuer(rec)

	deduped, err := enq.Enqueue(ctx, "push_event", `{"repo_pk":5}`, "")
	if err != nil || deduped {
		t.Fatalf("first enqueue: deduped=%v err=%v", deduped, err)
	}
	if len(rec.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rec.rows))
	}
	if rec.rows[0].Kind != "push_event" || rec.rows[0].Payload != `{"repo_pk":5}` {
		t.Errorf("row[0] = %+v", rec.rows[0])
	}

	// A keyed job goes through once; a second with the same key is folded in.
	if d, _ := enq.Enqueue(ctx, "reindex_search", "", "reindex:repo:5"); d {
		t.Error("first keyed enqueue reported deduped")
	}
	if d, _ := enq.Enqueue(ctx, "reindex_search", "", "reindex:repo:5"); !d {
		t.Error("second keyed enqueue not deduped")
	}
	if len(rec.rows) != 2 {
		t.Fatalf("rows after dedupe = %d, want 2", len(rec.rows))
	}
	if rec.rows[1].DedupeKey != "reindex:repo:5" {
		t.Errorf("row[1].DedupeKey = %q", rec.rows[1].DedupeKey)
	}
}

package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// pruneFake records each cutoff it is asked to prune and cancels the loop's
// context after the first sweep so the test does not wait out the ticker.
type pruneFake struct {
	calls  atomic.Int32
	cutoff atomic.Value // time.Time of the last call
	cancel context.CancelFunc
}

func (p *pruneFake) PruneDeliveries(_ context.Context, before time.Time) (int64, error) {
	p.calls.Add(1)
	p.cutoff.Store(before)
	p.cancel()
	return 3, nil
}

// TestRunDeliveryRetention confirms the loop sweeps immediately on start with
// a cutoff one retention window in the past, and stops when its context is
// canceled.
func TestRunDeliveryRetention(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &pruneFake{cancel: cancel}

	done := make(chan struct{})
	go func() {
		RunDeliveryRetention(ctx, fake, nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("retention loop did not stop after context cancel")
	}
	if got := fake.calls.Load(); got != 1 {
		t.Fatalf("sweeps = %d, want 1", got)
	}
	cutoff := fake.cutoff.Load().(time.Time)
	want := time.Now().Add(-DeliveryRetention)
	if diff := cutoff.Sub(want); diff < -time.Minute || diff > time.Minute {
		t.Errorf("cutoff = %v, want about %v", cutoff, want)
	}
}

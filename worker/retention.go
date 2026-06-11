package worker

import (
	"context"
	"log/slog"
	"time"
)

// DeliveryRetention is how long a webhook delivery record is kept. Thirty days
// matches GitHub's own delivery log window; a record carries the full request
// and response bodies, so keeping them forever grows the table without bound.
const DeliveryRetention = 30 * 24 * time.Hour

// retentionSweepInterval is how often the retention loop prunes. The sweep is
// one indexed DELETE, so running it hourly keeps each pass small instead of
// letting a day's deliveries pile up into one long writer hold.
const retentionSweepInterval = time.Hour

// deliveryPruner is the slice of the store the retention loop needs.
type deliveryPruner interface {
	PruneDeliveries(ctx context.Context, before time.Time) (int64, error)
}

// RunDeliveryRetention deletes webhook deliveries older than DeliveryRetention,
// sweeping once immediately and then every retentionSweepInterval until ctx is
// canceled. The server starts it as a background goroutine alongside the job
// runtime.
func RunDeliveryRetention(ctx context.Context, st deliveryPruner, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	sweep := func() {
		n, err := st.PruneDeliveries(ctx, time.Now().Add(-DeliveryRetention))
		if err != nil {
			if ctx.Err() == nil {
				log.Error("webhook delivery prune failed", "err", err)
			}
			return
		}
		if n > 0 {
			log.Info("webhook deliveries pruned", "rows", n)
		}
	}
	sweep()
	tick := time.NewTicker(retentionSweepInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			sweep()
		}
	}
}

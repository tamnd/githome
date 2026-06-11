package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/tamnd/githome/store"
)

// TestPruneDeliveries confirms the retention delete removes only deliveries
// older than the cutoff. The cutoff comparison crosses the dialect's stored
// timestamp format, which is exactly where a naive bound time.Time silently
// matches nothing on SQLite, so both directions are asserted.
func TestPruneDeliveries(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "hooked"})
		hook := &store.WebhookRow{
			RepoPK: repo.PK, Name: "web", URL: "https://example.com/hook",
			ContentType: "json", Active: true, Events: `["push"]`,
		}
		if err := st.InsertWebhook(ctx, hook); err != nil {
			t.Fatalf("InsertWebhook: %v", err)
		}
		d := &store.WebhookDeliveryRow{
			WebhookPK: hook.PK, GUID: "guid-1", Event: "push",
			RequestHeaders: "{}", ResponseHeaders: "{}",
		}
		if err := st.InsertDelivery(ctx, d); err != nil {
			t.Fatalf("InsertDelivery: %v", err)
		}

		// A cutoff in the past keeps the fresh delivery.
		n, err := st.PruneDeliveries(ctx, time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatalf("PruneDeliveries past cutoff: %v", err)
		}
		if n != 0 {
			t.Errorf("past cutoff pruned %d rows, want 0", n)
		}
		if list, _ := st.ListDeliveries(ctx, hook.PK, 10); len(list) != 1 {
			t.Fatalf("deliveries after past cutoff = %d, want 1", len(list))
		}

		// A cutoff in the future sweeps it.
		n, err = st.PruneDeliveries(ctx, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatalf("PruneDeliveries future cutoff: %v", err)
		}
		if n != 1 {
			t.Errorf("future cutoff pruned %d rows, want 1", n)
		}
		if list, _ := st.ListDeliveries(ctx, hook.PK, 10); len(list) != 0 {
			t.Fatalf("deliveries after future cutoff = %d, want 0", len(list))
		}
	})
}

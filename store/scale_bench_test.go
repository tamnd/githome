package store_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/tamnd/githome/store"
)

// TestScalePagination seeds a repository at real-world issue volume (vscode sits
// near 303k issues, nixpkgs near 525k pull requests) and measures the cost of a
// list request as page depth grows, comparing the OFFSET path against the keyset
// cursor path the read-flatness work added. It is gated behind GITHOME_SCALE so
// an ordinary `go test ./...` never pays the seed; set GITHOME_SCALE to the row
// count to run it, for example:
//
//	GITHOME_SCALE=300000 go test ./store -run TestScalePagination -v -timeout 30m
//
// Defaults to a throwaway SQLite file. Point GITHOME_SCALE_DSN at a running
// Postgres (the docker/postgres compose) to measure the same sweep on that
// engine; the keyset index is declared on both dialects, so the flat-vs-linear
// shape holds on each.
func TestScalePagination(t *testing.T) {
	raw := os.Getenv("GITHOME_SCALE")
	if raw == "" {
		t.Skip("set GITHOME_SCALE=<issue count> to run the real-world-scale pagination benchmark")
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		n = 300_000
	}

	ctx := context.Background()
	// Default to a throwaway SQLite file. Point GITHOME_SCALE_DSN at a running
	// Postgres (the docker/postgres compose, say) to measure the same sweep on
	// the engine the keyset fix's range-scan claim is about; the keyset index is
	// declared on both dialects, so the flat-vs-linear shape should hold on each.
	dsn := os.Getenv("GITHOME_SCALE_DSN")
	if dsn == "" {
		dsn = "sqlite://" + filepath.Join(t.TempDir(), "scale.db")
	}
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	// Start from a clean schema even on a reused Postgres database, the way the
	// dual-dialect store tests do, so a prior run's rows do not skew the seed.
	_ = st.Rollback(ctx, 1<<20)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Logf("engine: %s", st.Dialect())

	repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "scale", DefaultBranch: "main"})

	// Seed N open issues numbered 1..N, one minute apart, so every row has a
	// distinct (created_at, number) the keyset index can seek on. The newest issue
	// has the highest number, which is the order the list returns.
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	const step = time.Minute
	createdAt := func(number int) time.Time { return base.Add(time.Duration(number) * step) }

	seedStart := time.Now()
	err = st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *store.Tx) error {
			for i := 1; i <= n; i++ {
				when := createdAt(i)
				iss := &store.IssueRow{
					RepoPK:    repo.PK,
					Number:    int64(i),
					UserPK:    repo.OwnerPK,
					Title:     "issue " + strconv.Itoa(i),
					State:     "open",
					CreatedAt: when,
					UpdatedAt: when,
				}
				if err := tx.SeedIssue(ctx, iss); err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	seedDur := time.Since(seedStart)
	t.Logf("Track 1 seed: %d issues in %s = %.0f rows/sec",
		n, seedDur.Round(time.Millisecond), float64(n)/seedDur.Seconds())

	const perPage = 30

	// cursorAtOffset returns the keyset cursor that lands the next page at the
	// given offset: the row immediately before it. Offset 0 is the first page, so
	// it carries no cursor. Row rank r from the newest has number n-r.
	cursorAtOffset := func(offset int) *store.IssueCursor {
		if offset == 0 {
			return nil
		}
		number := n - (offset - 1)
		return &store.IssueCursor{CreatedAt: createdAt(number), Number: int64(number)}
	}

	// best runs fn a few times and returns the fastest, the most stable estimate
	// of a query's latency under a noisy machine.
	best := func(fn func()) time.Duration {
		lo := time.Duration(1<<63 - 1)
		for i := 0; i < 5; i++ {
			s := time.Now()
			fn()
			if d := time.Since(s); d < lo {
				lo = d
			}
		}
		return lo
	}

	// The COUNT the page-number path runs on every list request, and the flat
	// cursor path skips entirely.
	countDur := best(func() {
		if _, err := st.CountIssues(ctx, repo.PK, store.IssueFilter{}); err != nil {
			t.Fatalf("count: %v", err)
		}
	})
	t.Logf("Track 2 COUNT(*) over %d open issues: %s (the flat path avoids this every request)",
		n, countDur.Round(time.Microsecond))

	depths := []int{0, 100, 1000, 5000, 10000, 50000}
	t.Logf("Track 2 page latency by depth (per_page=%d), offset scan vs keyset seek:", perPage)
	t.Logf("  %10s %14s %14s %10s", "page", "offset", "keyset", "speedup")
	for _, depth := range depths {
		offset := depth * perPage
		if offset >= n {
			continue
		}

		offsetDur := best(func() {
			rows, err := st.ListIssues(ctx, repo.PK, store.IssueFilter{Limit: perPage, Offset: offset})
			if err != nil {
				t.Fatalf("offset list at %d: %v", offset, err)
			}
			if len(rows) != perPage {
				t.Fatalf("offset list at %d returned %d rows, want %d", offset, len(rows), perPage)
			}
		})

		cur := cursorAtOffset(offset)
		var keysetRows []store.IssueRow
		keysetDur := best(func() {
			rows, _, err := st.ListIssuesPage(ctx, repo.PK, store.IssueFilter{Limit: perPage, Cursor: cur})
			if err != nil {
				t.Fatalf("keyset page at %d: %v", offset, err)
			}
			keysetRows = rows
		})
		if len(keysetRows) != perPage {
			t.Fatalf("keyset page at offset %d returned %d rows, want %d", offset, len(keysetRows), perPage)
		}

		// The two paths must agree on the page contents, or the keyset speedup is
		// measuring the wrong rows.
		offsetRows, err := st.ListIssues(ctx, repo.PK, store.IssueFilter{Limit: perPage, Offset: offset})
		if err != nil {
			t.Fatalf("offset verify at %d: %v", offset, err)
		}
		for i := range offsetRows {
			if offsetRows[i].Number != keysetRows[i].Number {
				t.Fatalf("path divergence at offset %d row %d: offset=%d keyset=%d",
					offset, i, offsetRows[i].Number, keysetRows[i].Number)
			}
		}

		speedup := float64(offsetDur) / float64(keysetDur)
		t.Logf("  %10d %14s %14s %9.1fx",
			depth, offsetDur.Round(time.Microsecond), keysetDur.Round(time.Microsecond), speedup)
	}
}

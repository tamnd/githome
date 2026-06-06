package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tamnd/githome/store"
)

// dialectCase names a dialect and the DSN to reach it.
type dialectCase struct {
	name string
	dsn  string
}

// eachDialect runs fn for every dialect available in the test environment.
// SQLite always runs against a fresh temp-file database. Postgres runs only when
// GITHOME_TEST_POSTGRES_DSN is set (the CI postgres matrix leg sets it).
func eachDialect(t *testing.T, fn func(t *testing.T, st *store.Store)) {
	t.Helper()
	cases := []dialectCase{
		{name: "sqlite", dsn: "sqlite://" + filepath.Join(t.TempDir(), "githome.db")},
	}
	if dsn := os.Getenv("GITHOME_TEST_POSTGRES_DSN"); dsn != "" {
		cases = append(cases, dialectCase{name: "postgres", dsn: dsn})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			st, err := store.Open(ctx, c.dsn)
			if err != nil {
				t.Fatalf("Open(%s): %v", c.name, err)
			}
			t.Cleanup(func() { _ = st.Close() })
			// Start from a clean schema even on a reused Postgres database.
			_ = st.Rollback(ctx, 1<<20)
			fn(t, st)
		})
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("first Migrate: %v", err)
		}
		v1, err := st.Version(ctx)
		if err != nil {
			t.Fatalf("Version: %v", err)
		}
		if v1 == 0 {
			t.Fatalf("expected a non-zero schema version after migrate")
		}
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("second Migrate must be a no-op, got: %v", err)
		}
		v2, err := st.Version(ctx)
		if err != nil {
			t.Fatalf("Version: %v", err)
		}
		if v1 != v2 {
			t.Fatalf("schema version moved on a repeat migrate: %d -> %d", v1, v2)
		}
	})
}

func TestRollbackThenMigrate(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		top, err := st.Version(ctx)
		if err != nil {
			t.Fatalf("Version: %v", err)
		}
		if err := st.Rollback(ctx, 1<<20); err != nil {
			t.Fatalf("Rollback: %v", err)
		}
		if v, _ := st.Version(ctx); v != 0 {
			t.Fatalf("expected version 0 after full rollback, got %d", v)
		}
		// Re-migrating from scratch must reach the same version (down/up symmetry).
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("re-Migrate after rollback: %v", err)
		}
		if v, _ := st.Version(ctx); v != top {
			t.Fatalf("re-migrate reached %d, want %d", v, top)
		}
	})
}

func TestAllocDBIDIsMonotonic(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		var prev int64
		for range 5 {
			id, err := st.AllocDBID(ctx)
			if err != nil {
				t.Fatalf("AllocDBID: %v", err)
			}
			if id <= prev {
				t.Fatalf("ids must strictly increase: got %d after %d", id, prev)
			}
			prev = id
		}
	})
}

func TestPing(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		if err := st.Ping(context.Background()); err != nil {
			t.Fatalf("Ping: %v", err)
		}
	})
}

func TestOpenRejectsUnknownDSN(t *testing.T) {
	if _, err := store.Open(context.Background(), "mysql://nope"); err == nil {
		t.Fatal("expected Open to reject an unknown DSN scheme")
	}
}

// TestSQLitePragmas verifies that the performance-critical SQLite pragmas are
// active on a freshly opened database so a misconfigured build doesn't silently
// skip them.
func TestSQLitePragmas(t *testing.T) {
	dsn := "sqlite://" + filepath.Join(t.TempDir(), "pragmas.db")
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	check := func(pragma, want string) {
		t.Helper()
		var got string
		if err := st.DB().QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
			t.Errorf("PRAGMA %s: %v", pragma, err)
			return
		}
		if got != want {
			t.Errorf("PRAGMA %s = %q, want %q", pragma, got, want)
		}
	}
	check("journal_mode", "wal")
	check("foreign_keys", "1")
	check("synchronous", "1") // NORMAL = 1
}

// TestOpenWithCustomPoolSize verifies that Open accepts an explicit pool size
// without error (the value is applied internally; this test just guards the
// signature stays callable and doesn't panic).
func TestOpenWithCustomPoolSize(t *testing.T) {
	dsn := "sqlite://" + filepath.Join(t.TempDir(), "pool.db")
	st, err := store.Open(context.Background(), dsn, 50)
	if err != nil {
		t.Fatalf("Open with custom pool size: %v", err)
	}
	_ = st.Close()
}

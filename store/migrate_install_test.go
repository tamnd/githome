package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tamnd/githome/store"
)

// TestSchemaFileMatchesMigrations is the drift guard for schema.sqlite.sql: a
// database built from the consolidated schema file must have the same structure
// (tables, columns, indexes, triggers) as one built by replaying every
// migration. SQLite exposes its full structure through sqlite_master and the
// table_info pragma, so the comparison is exact rather than a code review. The
// Postgres pair is checked live wherever GITHOME_TEST_POSTGRES_DSN is set, by
// the Install/Migrate no-op test below.
func TestSchemaFileMatchesMigrations(t *testing.T) {
	ctx := context.Background()

	migrated, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "migrated.db"))
	if err != nil {
		t.Fatalf("open migrated: %v", err)
	}
	t.Cleanup(func() { _ = migrated.Close() })
	if err := migrated.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	installed, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "installed.db"))
	if err != nil {
		t.Fatalf("open installed: %v", err)
	}
	t.Cleanup(func() { _ = installed.Close() })
	if err := installed.Install(ctx); err != nil {
		t.Fatalf("Install: %v", err)
	}

	want := sqliteFingerprint(t, migrated.DB())
	got := sqliteFingerprint(t, installed.DB())
	if want != got {
		t.Fatalf("schema.sqlite.sql drifted from the migrations.\n--- migrated ---\n%s\n--- installed ---\n%s", want, got)
	}
}

// TestInstallStampsLedger checks that Install leaves the ledger fully stamped, so
// a follow-up Migrate is a no-op, and that Install refuses to run on a database
// that already has migrations applied.
func TestInstallStampsLedger(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Install(ctx); err != nil {
			t.Fatalf("Install: %v", err)
		}
		installedVersion, err := st.Version(ctx)
		if err != nil {
			t.Fatalf("Version: %v", err)
		}
		if installedVersion == 0 {
			t.Fatalf("expected a non-zero version after Install")
		}
		// A fresh Install must reach the same head a full Migrate would.
		other, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "ref.db"))
		if err != nil {
			t.Fatalf("open ref: %v", err)
		}
		t.Cleanup(func() { _ = other.Close() })
		if err := other.Migrate(ctx); err != nil {
			t.Fatalf("ref Migrate: %v", err)
		}
		if refVersion, _ := other.Version(ctx); st.Dialect() == store.DialectSQLite && refVersion != installedVersion {
			t.Fatalf("Install version %d != Migrate version %d", installedVersion, refVersion)
		}

		// Migrate after Install does nothing and does not move the version.
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate after Install: %v", err)
		}
		if v, _ := st.Version(ctx); v != installedVersion {
			t.Fatalf("version moved after Migrate: %d -> %d", installedVersion, v)
		}

		// Install on a populated database is refused.
		if err := st.Install(ctx); err == nil {
			t.Fatalf("expected Install to refuse a non-empty database")
		}
	})
}

// TestPostgresSchemaFileMatchesMigrations is the drift guard for schema.pg.sql,
// the Postgres counterpart to TestSchemaFileMatchesMigrations. A database built
// by Install (the consolidated schema file) must have the same structure as one
// built by replaying every migration. It runs only where a Postgres DSN is set.
// schema.pg.sql is maintained by hand, so this catches a migration that was
// added without folding its tables and columns into the consolidated file.
func TestPostgresSchemaFileMatchesMigrations(t *testing.T) {
	dsn := os.Getenv("GITHOME_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GITHOME_TEST_POSTGRES_DSN to run the Postgres schema drift guard")
	}
	ctx := context.Background()

	fingerprint := func(build func(*store.Store) error) string {
		t.Helper()
		st, err := store.Open(ctx, dsn)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer func() { _ = st.Close() }()
		// Reach an empty schema before building, so a reused database does not
		// taint the comparison.
		if err := st.Rollback(ctx, 1<<20); err != nil {
			t.Fatalf("reset: %v", err)
		}
		if err := build(st); err != nil {
			t.Fatalf("build: %v", err)
		}
		return pgFingerprint(t, st.DB())
	}

	want := fingerprint(func(st *store.Store) error { return st.Migrate(ctx) })
	got := fingerprint(func(st *store.Store) error { return st.Install(ctx) })
	if want != got {
		t.Fatalf("schema.pg.sql drifted from the migrations.\n--- migrated ---\n%s\n--- installed ---\n%s", want, got)
	}
}

// pgFingerprint renders a stable description of a Postgres database's structure:
// every application table's columns (type, nullability, default, and any
// generated expression) and every index, keyed and ordered by name so the
// column order Install and Migrate happen to write in does not matter. The
// schema_migrations ledger is excluded so the two build paths are compared on
// application schema alone.
func pgFingerprint(t *testing.T, db *sql.DB) string {
	t.Helper()
	var b strings.Builder
	cols := queryStrings(t, db, `SELECT table_name||'.'||column_name||' '||data_type
		||' null='||is_nullable
		||' dflt='||coalesce(column_default,'')
		||' gen='||coalesce(generation_expression,'')
		FROM information_schema.columns
		WHERE table_schema='public' AND table_name <> 'schema_migrations'
		ORDER BY 1`)
	for _, c := range cols {
		fmt.Fprintf(&b, "COLUMN %s\n", c)
	}
	idx := queryStrings(t, db, `SELECT indexname||' :: '||indexdef FROM pg_indexes
		WHERE schemaname='public' AND tablename <> 'schema_migrations' ORDER BY 1`)
	for _, i := range idx {
		fmt.Fprintf(&b, "INDEX %s\n", i)
	}
	return b.String()
}

// sqliteFingerprint renders a stable, whitespace-normalized description of a
// SQLite database's structure: every user table's columns, every named index,
// and every trigger. The schema_migrations ledger and SQLite's internal objects
// are excluded so the two build paths are compared on application schema alone.
func sqliteFingerprint(t *testing.T, db *sql.DB) string {
	t.Helper()
	ctx := context.Background()
	var b strings.Builder

	tables := queryStrings(t, db, `SELECT name FROM sqlite_master WHERE type='table'
		AND name NOT LIKE 'sqlite_%' AND name <> 'schema_migrations' ORDER BY name`)
	for _, tbl := range tables {
		fmt.Fprintf(&b, "TABLE %s\n", tbl)
		rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%q)", tbl))
		if err != nil {
			t.Fatalf("table_info(%s): %v", tbl, err)
		}
		var cols []string
		for rows.Next() {
			var cid, notnull, pk int
			var name, ctype string
			var dflt sql.NullString
			if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
				t.Fatalf("scan table_info: %v", err)
			}
			cols = append(cols, fmt.Sprintf("  %s %s notnull=%d dflt=%q pk=%d", name, ctype, notnull, dflt.String, pk))
		}
		_ = rows.Close()
		for _, c := range cols {
			b.WriteString(c)
			b.WriteString("\n")
		}
	}

	idx := queryPairs(t, db, `SELECT name, sql FROM sqlite_master WHERE type='index'
		AND sql IS NOT NULL ORDER BY name`)
	for _, p := range idx {
		fmt.Fprintf(&b, "INDEX %s :: %s\n", p[0], normalizeSQL(p[1]))
	}

	trg := queryPairs(t, db, `SELECT name, sql FROM sqlite_master WHERE type='trigger' ORDER BY name`)
	for _, p := range trg {
		fmt.Fprintf(&b, "TRIGGER %s :: %s\n", p[0], normalizeSQL(p[1]))
	}
	return b.String()
}

func normalizeSQL(s string) string { return strings.Join(strings.Fields(s), " ") }

func queryStrings(t *testing.T, db *sql.DB, q string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), q)
	if err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func queryPairs(t *testing.T, db *sql.DB, q string) [][2]string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), q)
	if err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	defer func() { _ = rows.Close() }()
	var out [][2]string
	for rows.Next() {
		var a, c string
		if err := rows.Scan(&a, &c); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, [2]string{a, c})
	}
	return out
}

// Package store is the single entry point to Githome's metadata database. It
// supports PostgreSQL (jackc/pgx/v5 via database/sql) and SQLite
// (modernc.org/sqlite, pure Go, no cgo) behind one dialect-aware Store. The same
// embedded migrations run on both.
//
// # Write-throughput ceiling
//
// SQLite serializes all writers at the database level: only one write transaction
// runs at a time. On modern NVMe storage each committed write transaction costs
// ~0.1–0.5 ms (one fsync), giving a ceiling of roughly 2–10K writes/sec for
// single-statement mutations. The multi-statement write path for an issue or pull
// request (number allocation, row insert, label/assignee associations, counter
// bump, event + job batch) sits comfortably inside that ceiling at development
// scale but tops out below the 500 RPS SLO target.
//
// Postgres lifts this ceiling: each write transaction acquires row-level locks
// (not a database-wide write lock), so unrelated writes proceed concurrently and
// the throughput scales with connection-pool depth and hardware IOPS. Githome
// targets Postgres for production at scale; SQLite serves development, tests, and
// single-user instances where the ceiling is not a bottleneck.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
	_ "modernc.org/sqlite"             // registers the "sqlite" driver
)

// ErrNotFound is returned when a row that was expected to exist does not.
var ErrNotFound = errors.New("store: not found")

// errUnknownDialect guards the dialect switches that branch on Postgres vs
// SQLite SQL; it should never surface in practice since the dialect is resolved
// once at Open from a known scheme.
var errUnknownDialect = errors.New("store: unknown dialect")

// Store wraps a *sql.DB together with its resolved dialect. It is safe for
// concurrent use; *sql.DB is a connection pool.
type Store struct {
	db      *sql.DB
	dialect Dialect
}

// Open resolves the dialect from the DSN, opens a pooled *sql.DB with the right
// driver, applies dialect-specific pool tuning, and verifies connectivity.
// pgPoolSize sets the Postgres max-open-connections limit; pass 0 (or use the
// default config value of 25) for the built-in default.
func Open(ctx context.Context, dsn string, pgPoolSize ...int) (*Store, error) {
	dialect, driver, normalized, err := parseDSN(dsn)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driver, normalized)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", dialect, err)
	}
	poolSize := 25
	if len(pgPoolSize) > 0 && pgPoolSize[0] > 0 {
		poolSize = pgPoolSize[0]
	}
	tunePool(db, dialect, poolSize)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping %s: %w", dialect, err)
	}
	return &Store{db: db, dialect: dialect}, nil
}

// DB exposes the underlying pool. Reserved for the migration runner and tests;
// application code goes through typed Store methods.
func (s *Store) DB() *sql.DB { return s.db }

// Dialect reports the resolved database dialect.
func (s *Store) Dialect() Dialect { return s.dialect }

// Ping verifies the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Close releases the connection pool.
func (s *Store) Close() error { return s.db.Close() }

// AllocDBID returns the next value of the global ID sequence shared by every
// public resource (the REST `id` / GraphQL `databaseId`). On Postgres this draws
// from a SEQUENCE; on SQLite it bumps the single-row high-water allocator.
func (s *Store) AllocDBID(ctx context.Context) (int64, error) {
	var q string
	switch s.dialect {
	case DialectPostgres:
		q = `SELECT nextval('global_id_seq')`
	case DialectSQLite:
		q = `UPDATE id_allocator SET high_water = high_water + 1 WHERE id = 1 RETURNING high_water`
	default:
		return 0, errUnknownDialect
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, q).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func tunePool(db *sql.DB, d Dialect, pgPoolSize int) {
	switch d {
	case DialectPostgres:
		db.SetMaxOpenConns(pgPoolSize)
		db.SetMaxIdleConns(pgPoolSize)
		db.SetConnMaxIdleTime(5 * time.Minute)
		db.SetConnMaxLifetime(time.Hour)
	case DialectSQLite:
		// SQLite serializes writers. Pinning the pool to a single connection
		// serializes all access through one handle and is the single most
		// important setting for avoiding "database is locked".
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		db.SetConnMaxLifetime(0)
	}
}

// parseDSN resolves the dialect, the database/sql driver name, and the
// normalized DSN. For SQLite it rewrites the DSN into modernc's file: form and
// forces the pragmas Githome relies on unless the caller already set them.
func parseDSN(dsn string) (d Dialect, driver, normalized string, err error) {
	switch {
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		return DialectPostgres, "pgx", dsn, nil

	case strings.HasPrefix(dsn, "sqlite://"), strings.HasPrefix(dsn, "file:"),
		strings.HasSuffix(splitBeforeQuery(dsn), ".db"),
		strings.HasSuffix(splitBeforeQuery(dsn), ".sqlite"):
		path := strings.TrimPrefix(strings.TrimPrefix(dsn, "sqlite://"), "file:")
		base, query, _ := strings.Cut(path, "?")
		vals, _ := url.ParseQuery(query)
		ensurePragma(vals, "journal_mode", "WAL")
		ensurePragma(vals, "busy_timeout", "5000")
		ensurePragma(vals, "foreign_keys", "ON")
		ensurePragma(vals, "synchronous", "NORMAL")
		// Negative cache_size means kilobytes; -65536 = 64 MB page cache.
		ensurePragma(vals, "cache_size", "-65536")
		// 256 MB memory-mapped I/O for read-heavy workloads.
		ensurePragma(vals, "mmap_size", "268435456")
		return DialectSQLite, "sqlite", "file:" + base + "?" + vals.Encode(), nil

	default:
		return 0, "", "", fmt.Errorf("store: unrecognized DSN scheme in %q (use postgres:// or sqlite://)", dsn)
	}
}

func splitBeforeQuery(dsn string) string {
	before, _, _ := strings.Cut(dsn, "?")
	return before
}

// ensurePragma adds _pragma=name(value) unless this pragma is already present.
func ensurePragma(v url.Values, name, value string) {
	for _, existing := range v["_pragma"] {
		if strings.HasPrefix(existing, name+"(") {
			return
		}
	}
	v.Add("_pragma", name+"("+value+")")
}

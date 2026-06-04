package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/tamnd/githome/store/migrations"
)

// advisoryKey is a stable 64-bit key for pg_advisory_lock so that only one
// process migrates a Postgres database at a time.
var advisoryKey = func() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("githome.migrations"))
	return int64(h.Sum64())
}()

type migration struct {
	version  int64
	name     string
	up       string
	down     string
	checksum string // sha256 of the up body, hex
}

// Migrate applies every pending up migration in version order. It is safe to run
// on every boot: already-applied migrations are skipped, and a checksum mismatch
// on a previously-applied version is reported rather than silently re-run.
func (s *Store) Migrate(ctx context.Context) error {
	return s.withMigrationLock(ctx, func(conn *sql.Conn) error {
		if err := s.ensureLedger(ctx, conn); err != nil {
			return err
		}
		migs, err := s.loadMigrations()
		if err != nil {
			return err
		}
		applied, err := s.loadApplied(ctx, conn)
		if err != nil {
			return err
		}
		for _, m := range migs {
			if sum, ok := applied[m.version]; ok {
				if sum != m.checksum {
					return fmt.Errorf("store: migration %04d_%s checksum drift (ledger=%s file=%s)",
						m.version, m.name, sum, m.checksum)
				}
				continue
			}
			if err := s.applyOne(ctx, conn, m, true); err != nil {
				return fmt.Errorf("store: apply %04d_%s: %w", m.version, m.name, err)
			}
		}
		return nil
	})
}

// Rollback reverts the last n applied migrations, most recent first. A zero or
// negative n is treated as 1.
func (s *Store) Rollback(ctx context.Context, n int) error {
	if n <= 0 {
		n = 1
	}
	return s.withMigrationLock(ctx, func(conn *sql.Conn) error {
		if err := s.ensureLedger(ctx, conn); err != nil {
			return err
		}
		migs, err := s.loadMigrations()
		if err != nil {
			return err
		}
		byVersion := map[int64]migration{}
		for _, m := range migs {
			byVersion[m.version] = m
		}
		applied, err := s.loadApplied(ctx, conn)
		if err != nil {
			return err
		}
		versions := make([]int64, 0, len(applied))
		for v := range applied {
			versions = append(versions, v)
		}
		sort.Slice(versions, func(i, j int) bool { return versions[i] > versions[j] })
		if n > len(versions) {
			n = len(versions)
		}
		for _, v := range versions[:n] {
			m, ok := byVersion[v]
			if !ok {
				return fmt.Errorf("store: cannot roll back version %d: migration file missing", v)
			}
			if err := s.applyOne(ctx, conn, m, false); err != nil {
				return fmt.Errorf("store: rollback %04d_%s: %w", m.version, m.name, err)
			}
		}
		return nil
	})
}

// Version reports the highest applied migration version, or 0 when none have
// been applied.
func (s *Store) Version(ctx context.Context) (int64, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()
	if err := s.ensureLedger(ctx, conn); err != nil {
		return 0, err
	}
	applied, err := s.loadApplied(ctx, conn)
	if err != nil {
		return 0, err
	}
	var latest int64
	for v := range applied {
		if v > latest {
			latest = v
		}
	}
	return latest, nil
}

func (s *Store) withMigrationLock(ctx context.Context, fn func(*sql.Conn) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	if s.dialect == DialectPostgres {
		if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryKey); err != nil {
			return fmt.Errorf("store: acquire advisory lock: %w", err)
		}
		defer func() { _, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryKey) }()
	}
	return fn(conn)
}

func (s *Store) ensureLedger(ctx context.Context, conn *sql.Conn) error {
	ddl := `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    BIGINT  NOT NULL PRIMARY KEY,
		name       TEXT    NOT NULL,
		checksum   TEXT    NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`
	if s.dialect == DialectSQLite {
		ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER NOT NULL PRIMARY KEY,
			name       TEXT    NOT NULL,
			checksum   TEXT    NOT NULL,
			applied_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`
	}
	_, err := conn.ExecContext(ctx, ddl)
	return err
}

func (s *Store) loadApplied(ctx context.Context, conn *sql.Conn) (map[int64]string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]string{}
	for rows.Next() {
		var v int64
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			return nil, err
		}
		out[v] = sum
	}
	return out, rows.Err()
}

// loadMigrations reads the embedded SQL for the active dialect, pairing each
// version's up and down bodies.
func (s *Store) loadMigrations() ([]migration, error) {
	token := "pg"
	if s.dialect == DialectSQLite {
		token = "sqlite"
	}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, err
	}
	byVersion := map[int64]*migration{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		version, label, dialect, direction, ok := parseMigrationName(name)
		if !ok || dialect != token {
			continue
		}
		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return nil, err
		}
		m := byVersion[version]
		if m == nil {
			m = &migration{version: version, name: label}
			byVersion[version] = m
		}
		switch direction {
		case "up":
			m.up = string(body)
			sum := sha256.Sum256(body)
			m.checksum = hex.EncodeToString(sum[:])
		case "down":
			m.down = string(body)
		}
	}
	out := make([]migration, 0, len(byVersion))
	for _, m := range byVersion {
		if m.up == "" {
			return nil, fmt.Errorf("store: migration %04d_%s has no up body for %s", m.version, m.name, token)
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// parseMigrationName splits "<version>_<name>.<dialect>.<direction>.sql".
func parseMigrationName(filename string) (version int64, name, dialect, direction string, ok bool) {
	trimmed := strings.TrimSuffix(filename, ".sql")
	parts := strings.Split(trimmed, ".")
	if len(parts) != 3 {
		return 0, "", "", "", false
	}
	stem, dialect, direction := parts[0], parts[1], parts[2]
	num, label, found := strings.Cut(stem, "_")
	if !found {
		return 0, "", "", "", false
	}
	v, err := strconv.ParseInt(num, 10, 64)
	if err != nil {
		return 0, "", "", "", false
	}
	return v, label, dialect, direction, true
}

func (s *Store) applyOne(ctx context.Context, conn *sql.Conn, m migration, up bool) error {
	begin := "BEGIN"
	if s.dialect == DialectSQLite {
		begin = "BEGIN EXCLUSIVE"
	}
	if _, err := conn.ExecContext(ctx, begin); err != nil {
		return err
	}
	rollback := func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") }

	body := m.up
	ledger := s.rebind(`INSERT INTO schema_migrations (version, name, checksum) VALUES (?, ?, ?)`)
	args := []any{m.version, m.name, m.checksum}
	if !up {
		if m.down == "" {
			rollback()
			return fmt.Errorf("no down migration")
		}
		body = m.down
		ledger = s.rebind(`DELETE FROM schema_migrations WHERE version = ?`)
		args = []any{m.version}
	}

	if _, err := conn.ExecContext(ctx, body); err != nil {
		rollback()
		return err
	}
	if _, err := conn.ExecContext(ctx, ledger, args...); err != nil {
		rollback()
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		rollback()
		return err
	}
	return nil
}

// rebind rewrites `?` placeholders to the dialect's form ($1.. on Postgres).
func (s *Store) rebind(query string) string {
	if s.dialect != DialectPostgres {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(query[i])
	}
	return b.String()
}

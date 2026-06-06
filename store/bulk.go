package store

import (
	"context"
	"fmt"
	"strings"
)

// This file is the high-volume bulk-load path used by the corpus seeders. The
// single-row Seed* methods in seed.go preserve timestamps and hand back the pk
// of every row, which the seeder needs for the parent tables it builds foreign
// keys against. The leaf tables (reactions, timeline events) have no children,
// so they pay for that per-row round trip with nothing: they need neither the
// returned pk nor a separate id allocation call. The methods here load them in
// chunked multi-row INSERTs over one batch-allocated id range instead, which is
// the difference between a feasible and an infeasible seed at million-row scale.

// bulkRows bounds how many rows go into one multi-row INSERT. SQLite caps bound
// parameters per statement (historically 999, 32766 on current builds); the
// widest leaf row here binds 7 columns, so 200 rows stays well under either
// limit while keeping the statement count low. Postgres allows far more, but the
// same chunk keeps one code path.
const bulkRows = 200

// allocDBIDBatch reserves n consecutive global ids in one round trip and returns
// them, so a bulk insert of n rows pays one id allocation instead of n. On
// SQLite the single-row high-water allocator advances by n at once; on Postgres
// the sequence is drawn n times in one statement.
func (t *Tx) allocDBIDBatch(ctx context.Context, n int) ([]int64, error) {
	if n <= 0 {
		return nil, nil
	}
	ids := make([]int64, 0, n)
	switch t.dialect {
	case DialectPostgres:
		rows, err := t.tx.QueryContext(ctx, `SELECT nextval('global_id_seq') FROM generate_series(1, $1)`, n)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, rows.Err()
	case DialectSQLite:
		// Advance the high water by n and take the contiguous block that ends at
		// the returned value.
		var end int64
		q := `UPDATE id_allocator SET high_water = high_water + ? WHERE id = 1 RETURNING high_water`
		if err := t.tx.QueryRowContext(ctx, t.rebind(q), n).Scan(&end); err != nil {
			return nil, err
		}
		for i := end - int64(n) + 1; i <= end; i++ {
			ids = append(ids, i)
		}
		return ids, nil
	default:
		return nil, errUnknownDialect
	}
}

// BulkLoad runs fn with the store tuned for a one-time bulk load and restores the
// serving pragmas afterward. On SQLite it trades durability for speed during the
// load (synchronous=OFF, journal_mode=MEMORY): a crash mid-load can corrupt the
// database, which is acceptable because a seed is re-runnable against a clean
// target, and the gain is large because the load stops paying a per-commit fsync.
// On Postgres it is a passthrough; its bulk speed comes from the multi-row path,
// not session pragmas. The serving pragmas (WAL, synchronous=NORMAL) are restored
// even if fn fails.
func (s *Store) BulkLoad(ctx context.Context, fn func() error) error {
	if s.dialect != DialectSQLite {
		return fn()
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA synchronous=OFF`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA journal_mode=MEMORY`); err != nil {
		return err
	}
	restore := func() {
		_, _ = s.db.ExecContext(ctx, `PRAGMA synchronous=NORMAL`)
		_, _ = s.db.ExecContext(ctx, `PRAGMA journal_mode=WAL`)
	}
	defer restore()
	return fn()
}

// SeedReactionsBulk inserts many reactions rows in chunked multi-row INSERTs over
// one batch-allocated id range, preserving each row's created_at. It is the bulk
// analog of SeedReaction for the seeder's materialized reactor-pool rows, which
// are the most numerous synthesized rows in a corpus.
func (t *Tx) SeedReactionsBulk(ctx context.Context, rows []ReactionRow) error {
	if len(rows) == 0 {
		return nil
	}
	ids, err := t.allocDBIDBatch(ctx, len(rows))
	if err != nil {
		return err
	}
	const cols = 6
	for start := 0; start < len(rows); start += bulkRows {
		end := min(start+bulkRows, len(rows))
		chunk := rows[start:end]
		var b strings.Builder
		b.WriteString(`INSERT INTO reactions (db_id, subject_type, subject_pk, user_pk, content, created_at) VALUES `)
		args := make([]any, 0, len(chunk)*cols)
		for i, r := range chunk {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString("(?, ?, ?, ?, ?, ?)")
			args = append(args, ids[start+i], r.SubjectType, r.SubjectPK, r.UserPK, r.Content, t.timeArg(r.CreatedAt))
		}
		if _, err := t.tx.ExecContext(ctx, t.rebind(b.String()), args...); err != nil {
			return fmt.Errorf("bulk reactions: %w", err)
		}
	}
	return nil
}

// SeedIssueEventsBulk inserts many issue_events rows in chunked multi-row INSERTs
// over one batch-allocated id range, preserving each row's created_at and
// rendered payload. The timeline is the largest table in an automation-heavy
// corpus, so this is the bulk path that makes seeding it feasible.
func (t *Tx) SeedIssueEventsBulk(ctx context.Context, rows []IssueEventRow) error {
	if len(rows) == 0 {
		return nil
	}
	ids, err := t.allocDBIDBatch(ctx, len(rows))
	if err != nil {
		return err
	}
	const cols = 7
	for start := 0; start < len(rows); start += bulkRows {
		end := min(start+bulkRows, len(rows))
		chunk := rows[start:end]
		var b strings.Builder
		b.WriteString(`INSERT INTO issue_events (db_id, repo_pk, issue_pk, actor_pk, event, payload, created_at) VALUES `)
		args := make([]any, 0, len(chunk)*cols)
		for i, e := range chunk {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString("(?, ?, ?, ?, ?, ?, ?)")
			payload := e.Payload
			if payload == "" {
				payload = "{}"
			}
			args = append(args, ids[start+i], e.RepoPK, e.IssuePK, argI64(e.ActorPK), e.Event, payload, t.timeArg(e.CreatedAt))
		}
		if _, err := t.tx.ExecContext(ctx, t.rebind(b.String()), args...); err != nil {
			return fmt.Errorf("bulk issue events: %w", err)
		}
	}
	return nil
}

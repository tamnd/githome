package store

import (
	"context"
	"database/sql"
	"errors"
)

// JobRow is a row of the jobs table: one unit of background work. Payload is a
// JSON document the worker for that Kind decodes. DedupeKey, when non-empty,
// makes the job idempotent across a burst: while an identical key is queued or
// running, EnqueueJob skips the insert.
type JobRow struct {
	PK          int64
	Kind        string
	Payload     string
	DedupeKey   string
	State       string
	Attempts    int
	MaxAttempts int
}

// EnqueueJob inserts a queued job and fills the server-assigned fields back onto
// j. When DedupeKey is non-empty and an active (queued or running) job already
// carries that key, the insert is skipped and deduped is true, so a flurry of
// pushes to one pull request collapses into a single recompute. The partial
// unique index backstops a race between two concurrent enqueues.
func (s *Store) EnqueueJob(ctx context.Context, j *JobRow) (deduped bool, err error) {
	if j.DedupeKey != "" {
		var one int
		q := s.rebind(`SELECT 1 FROM jobs
			WHERE dedupe_key = ? AND state IN ('queued', 'running') LIMIT 1`)
		switch err := s.db.QueryRowContext(ctx, q, j.DedupeKey).Scan(&one); {
		case err == nil:
			return true, nil
		case !errors.Is(err, sql.ErrNoRows):
			return false, err
		}
	}
	if j.Payload == "" {
		j.Payload = "{}"
	}
	var dedupe any
	if j.DedupeKey != "" {
		dedupe = j.DedupeKey
	}
	q := s.rebind(`INSERT INTO jobs (kind, payload, dedupe_key)
		VALUES (?, ?, ?)
		RETURNING pk, state, attempts, max_attempts`)
	err = s.db.QueryRowContext(ctx, q, j.Kind, j.Payload, dedupe).
		Scan(&j.PK, &j.State, &j.Attempts, &j.MaxAttempts)
	return false, err
}

// insertJob inserts a queued job inside an existing transaction without the
// dedupe pre-check. It is used by InsertEventAndJob where the caller guarantees
// DedupeKey is empty and the full dedupe SELECT/INSERT round trip is unnecessary.
func (t *Tx) insertJob(ctx context.Context, j *JobRow) error {
	if j.Payload == "" {
		j.Payload = "{}"
	}
	q := t.rebind(`INSERT INTO jobs (kind, payload, dedupe_key)
		VALUES (?, ?, ?)
		RETURNING pk, state, attempts, max_attempts`)
	return t.tx.QueryRowContext(ctx, q, j.Kind, j.Payload, nil).
		Scan(&j.PK, &j.State, &j.Attempts, &j.MaxAttempts)
}

// ClaimJob atomically takes the oldest runnable queued job, moving it to the
// running state and bumping its attempt count under the database's own clock so
// concurrent claimers never hand out the same job. It returns ErrNotFound when
// the queue holds nothing runnable. Postgres skips rows another transaction has
// locked; SQLite's single-writer transaction serializes claimers on its own.
func (s *Store) ClaimJob(ctx context.Context) (*JobRow, error) {
	var q string
	switch s.dialect {
	case DialectPostgres:
		q = `UPDATE jobs SET state = 'running', attempts = attempts + 1,
			locked_at = now(), updated_at = now()
			WHERE pk = (
				SELECT pk FROM jobs
				WHERE state = 'queued' AND run_after <= now()
				ORDER BY run_after LIMIT 1
				FOR UPDATE SKIP LOCKED)
			RETURNING pk, kind, payload, dedupe_key, state, attempts, max_attempts`
	case DialectSQLite:
		q = `UPDATE jobs SET state = 'running', attempts = attempts + 1,
			locked_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
			WHERE pk = (
				SELECT pk FROM jobs
				WHERE state = 'queued' AND run_after <= CURRENT_TIMESTAMP
				ORDER BY run_after LIMIT 1)
			RETURNING pk, kind, payload, dedupe_key, state, attempts, max_attempts`
	default:
		return nil, errUnknownDialect
	}
	var (
		j      JobRow
		dedupe sql.NullString
	)
	err := s.db.QueryRowContext(ctx, s.rebind(q)).
		Scan(&j.PK, &j.Kind, &j.Payload, &dedupe, &j.State, &j.Attempts, &j.MaxAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if dedupe.Valid {
		j.DedupeKey = dedupe.String
	}
	return &j, nil
}

// CompleteJob removes a finished job. A completed job leaves no trace; the
// dedupe key it held frees up immediately for the next enqueue.
func (s *Store) CompleteJob(ctx context.Context, pk int64) error {
	_, err := s.db.ExecContext(ctx, s.rebind(`DELETE FROM jobs WHERE pk = ?`), pk)
	return err
}

// FailJob records a handler failure. While attempts remain it requeues the job
// for a later retry after backoffSeconds, computed on the database clock so the
// run_after stays in the column's native format. Once the attempts are spent it
// parks the job in the dead state far in the future, kept for inspection rather
// than deleted.
func (s *Store) FailJob(ctx context.Context, pk int64, attempts, maxAttempts int, reason string, backoffSeconds int) error {
	dead := attempts >= maxAttempts
	var q string
	switch s.dialect {
	case DialectPostgres:
		if dead {
			q = `UPDATE jobs SET state = 'dead', locked_at = NULL, last_error = ?,
				run_after = now() + interval '100 years', updated_at = now() WHERE pk = ?`
		} else {
			q = `UPDATE jobs SET state = 'queued', locked_at = NULL, last_error = ?,
				run_after = now() + make_interval(secs => ?), updated_at = now() WHERE pk = ?`
		}
	case DialectSQLite:
		if dead {
			q = `UPDATE jobs SET state = 'dead', locked_at = NULL, last_error = ?,
				run_after = datetime(CURRENT_TIMESTAMP, '+36500 days'),
				updated_at = CURRENT_TIMESTAMP WHERE pk = ?`
		} else {
			q = `UPDATE jobs SET state = 'queued', locked_at = NULL, last_error = ?,
				run_after = datetime(CURRENT_TIMESTAMP, '+' || ? || ' seconds'),
				updated_at = CURRENT_TIMESTAMP WHERE pk = ?`
		}
	default:
		return errUnknownDialect
	}
	if dead {
		_, err := s.db.ExecContext(ctx, s.rebind(q), reason, pk)
		return err
	}
	_, err := s.db.ExecContext(ctx, s.rebind(q), reason, backoffSeconds, pk)
	return err
}

// ListJobs returns every job ordered by primary key. It backs tests and the
// worker's startup recovery scan; the per-kind claim path lands with the worker
// milestone.
func (s *Store) ListJobs(ctx context.Context) ([]JobRow, error) {
	q := s.rebind(`SELECT pk, kind, payload, dedupe_key, state, attempts, max_attempts
		FROM jobs ORDER BY pk`)
	rows, err := s.rdb.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []JobRow
	for rows.Next() {
		var (
			j      JobRow
			dedupe sql.NullString
		)
		if err := rows.Scan(&j.PK, &j.Kind, &j.Payload, &dedupe, &j.State, &j.Attempts, &j.MaxAttempts); err != nil {
			return nil, err
		}
		if dedupe.Valid {
			j.DedupeKey = dedupe.String
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

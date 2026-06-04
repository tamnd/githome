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

// ListJobs returns every job ordered by primary key. It backs tests and the
// worker's startup recovery scan; the per-kind claim path lands with the worker
// milestone.
func (s *Store) ListJobs(ctx context.Context) ([]JobRow, error) {
	q := s.rebind(`SELECT pk, kind, payload, dedupe_key, state, attempts, max_attempts
		FROM jobs ORDER BY pk`)
	rows, err := s.db.QueryContext(ctx, q)
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

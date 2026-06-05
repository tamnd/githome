package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// The event store. An event is appended once at domain time (the actor's write
// has already committed) and is never mutated except to fill in the rendered
// payload the fan-out worker computes. Reads back it as the public activity
// feed: globally, per repository, or per actor, with the visibility filter the
// Events API enforces. db_id is allocated in the insert so the node id is unique
// across every kind.

const eventColumns = `pk, db_id, event, action, actor_pk, repo_pk, issue_pk,
	payload, public, created_at`

// InsertEvent appends one event row with a freshly allocated db_id, filling the
// server-assigned fields back onto e. It opens its own transaction because the
// caller records the event after its own mutation has committed.
func (s *Store) InsertEvent(ctx context.Context, e *EventRow) error {
	return s.WithTx(ctx, func(t *Tx) error { return t.InsertEvent(ctx, e) })
}

// InsertEvent appends one event row inside an existing transaction.
func (t *Tx) InsertEvent(ctx context.Context, e *EventRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	if e.Payload == "" {
		e.Payload = "{}"
	}
	q := t.rebind(`INSERT INTO events
		(db_id, event, action, actor_pk, repo_pk, issue_pk, payload, public)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id, created_at`)
	var created nullTime
	err = t.tx.QueryRowContext(ctx, q,
		dbID, e.Event, e.Action, e.ActorPK, e.RepoPK, argI64(e.IssuePK),
		e.Payload, argBool(&e.Public),
	).Scan(&e.PK, &e.DBID, &created)
	if err != nil {
		return err
	}
	e.CreatedAt = created.Time
	return nil
}

// GetEventByPK resolves one event by primary key, the value a deliver_event job
// carries so the fan-out worker can render and store its payload.
func (s *Store) GetEventByPK(ctx context.Context, pk int64) (*EventRow, error) {
	q := s.rebind(`SELECT ` + eventColumns + ` FROM events WHERE pk = ?`)
	return scanEvent(s.db.QueryRowContext(ctx, q, pk))
}

// SetEventPayload stores the rendered Events-API document on an event row, the
// body the feed serves once the fan-out worker has built it.
func (s *Store) SetEventPayload(ctx context.Context, pk int64, payload string) error {
	q := s.rebind(`UPDATE events SET payload = ? WHERE pk = ?`)
	res, err := s.db.ExecContext(ctx, q, payload, pk)
	if err != nil {
		return err
	}
	return affectedOrNotFound(res)
}

// EventFilter selects a slice of the activity feed. RepoPK and ActorPK are the
// per-repository and per-user scopes; PublicOnly applies the visibility filter
// the unauthenticated Events API enforces (the repo is public and the event is
// public). Limit caps the page.
type EventFilter struct {
	RepoPK     *int64
	ActorPK    *int64
	PublicOnly bool
	Limit      int
}

// ListEvents returns the activity feed matching f, newest first. It joins the
// owning repository so the public filter reads the live visibility rather than
// only the flag frozen on the event at insert time.
func (s *Store) ListEvents(ctx context.Context, f EventFilter) ([]EventRow, error) {
	var (
		where []string
		args  []any
	)
	if f.RepoPK != nil {
		where = append(where, `e.repo_pk = ?`)
		args = append(args, *f.RepoPK)
	}
	if f.ActorPK != nil {
		where = append(where, `e.actor_pk = ?`)
		args = append(args, *f.ActorPK)
	}
	if f.PublicOnly {
		where = append(where, `r.private = ? AND e.public = ?`)
		args = append(args, false, true)
	}
	limit := f.Limit
	if limit <= 0 || limit > 300 {
		limit = 30
	}
	q := `SELECT ` + eventQualifiedColumns + ` FROM events e
		JOIN repositories r ON r.pk = e.repo_pk`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY e.pk DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, s.rebind(q), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []EventRow
	for rows.Next() {
		e, err := scanEventRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// eventQualifiedColumns is eventColumns prefixed for the joined ListEvents query.
const eventQualifiedColumns = `e.pk, e.db_id, e.event, e.action, e.actor_pk,
	e.repo_pk, e.issue_pk, e.payload, e.public, e.created_at`

func scanEvent(row interface{ Scan(...any) error }) (*EventRow, error) {
	e, err := scanEventRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return e, err
}

func scanEventRows(row interface{ Scan(...any) error }) (*EventRow, error) {
	var (
		e       EventRow
		issue   sql.NullInt64
		public  boolVal
		created nullTime
	)
	if err := row.Scan(&e.PK, &e.DBID, &e.Event, &e.Action, &e.ActorPK,
		&e.RepoPK, &issue, &e.Payload, &public, &created); err != nil {
		return nil, err
	}
	e.IssuePK = i64Ptr(issue)
	e.Public = public.Bool
	e.CreatedAt = created.Time
	return &e, nil
}

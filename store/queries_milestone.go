package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// MilestoneRow is a row of the milestones table. Milestones carry their own
// per-repo number, allocated from repositories.next_milestone_number, separate
// from the issue/PR number sequence. The open and closed issue counts GitHub
// reports are computed on read rather than cached, so they are not columns.
type MilestoneRow struct {
	PK          int64
	DBID        int64
	RepoPK      int64
	Number      int64
	Title       string
	Description *string
	State       string
	DueOn       *time.Time
	CreatorPK   *int64
	ClosedAt    *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const milestoneColumns = `pk, db_id, repo_pk, number, title, description, state, due_on, creator_pk, closed_at, created_at, updated_at`

// ListMilestones returns a repository's milestones filtered by state
// ("open"|"closed"|"all"), ordered by number.
func (s *Store) ListMilestones(ctx context.Context, repoPK int64, state string) ([]MilestoneRow, error) {
	where := `WHERE repo_pk = ?`
	args := []any{repoPK}
	if state == "open" || state == "closed" {
		where += ` AND state = ?`
		args = append(args, state)
	}
	q := s.rebind(`SELECT ` + milestoneColumns + ` FROM milestones ` + where + ` ORDER BY number`)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []MilestoneRow
	for rows.Next() {
		m, err := scanMilestoneRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// GetMilestoneByNumber resolves a milestone by its per-repo number.
func (s *Store) GetMilestoneByNumber(ctx context.Context, repoPK, number int64) (*MilestoneRow, error) {
	q := s.rebind(`SELECT ` + milestoneColumns + ` FROM milestones WHERE repo_pk = ? AND number = ?`)
	return scanMilestone(s.db.QueryRowContext(ctx, q, repoPK, number))
}

// GetMilestoneByPK resolves a milestone by primary key.
func (s *Store) GetMilestoneByPK(ctx context.Context, pk int64) (*MilestoneRow, error) {
	q := s.rebind(`SELECT ` + milestoneColumns + ` FROM milestones WHERE pk = ?`)
	return scanMilestone(s.db.QueryRowContext(ctx, q, pk))
}

// MilestoneIssueCounts returns the open and closed issue counts for a milestone,
// computed from the issues that point at it.
func (s *Store) MilestoneIssueCounts(ctx context.Context, milestonePK int64) (open, closed int, err error) {
	q := s.rebind(`SELECT state, COUNT(*) FROM issues
		WHERE milestone_pk = ? AND deleted_at IS NULL GROUP BY state`)
	rows, err := s.db.QueryContext(ctx, q, milestonePK)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return 0, 0, err
		}
		switch state {
		case "open":
			open = n
		case "closed":
			closed = n
		}
	}
	return open, closed, rows.Err()
}

// InsertMilestone allocates the per-repo milestone number and the global db_id in
// one transaction with the row insert.
func (s *Store) InsertMilestone(ctx context.Context, m *MilestoneRow) error {
	return s.WithTx(ctx, func(t *Tx) error {
		num, err := t.allocMilestoneNumber(ctx, m.RepoPK)
		if err != nil {
			return err
		}
		dbID, err := t.allocDBID(ctx)
		if err != nil {
			return err
		}
		if m.State == "" {
			m.State = "open"
		}
		q := t.rebind(`INSERT INTO milestones
			(db_id, repo_pk, number, title, description, state, due_on, creator_pk)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			RETURNING pk, db_id, number, created_at, updated_at`)
		var created, updated nullTime
		err = t.tx.QueryRowContext(ctx, q,
			dbID, m.RepoPK, num, m.Title, argStr(m.Description), m.State,
			argTime(m.DueOn), argI64(m.CreatorPK),
		).Scan(&m.PK, &m.DBID, &m.Number, &created, &updated)
		if err != nil {
			return err
		}
		m.CreatedAt, m.UpdatedAt = created.Time, updated.Time
		return nil
	})
}

// allocMilestoneNumber atomically hands out the next per-repo milestone number.
func (t *Tx) allocMilestoneNumber(ctx context.Context, repoPK int64) (int64, error) {
	q := t.rebind(`UPDATE repositories SET next_milestone_number = next_milestone_number + 1
		WHERE pk = ? RETURNING next_milestone_number - 1`)
	var n int64
	if err := t.tx.QueryRowContext(ctx, q, repoPK).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// UpdateMilestone writes the editable fields and the close transition (state
// flipping to closed stamps closed_at; reopening clears it).
func (s *Store) UpdateMilestone(ctx context.Context, m *MilestoneRow) error {
	q := s.rebind(`UPDATE milestones
		SET title = ?, description = ?, state = ?, due_on = ?, closed_at = ?, updated_at = ?
		WHERE pk = ? RETURNING created_at, updated_at`)
	var created, updated nullTime
	err := s.db.QueryRowContext(ctx, q,
		m.Title, argStr(m.Description), m.State, argTime(m.DueOn), argTime(m.ClosedAt), nowUTC(), m.PK,
	).Scan(&created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	m.CreatedAt, m.UpdatedAt = created.Time, updated.Time
	return nil
}

// DeleteMilestone removes a milestone; issues pointing at it have milestone_pk
// reset to NULL by the foreign key.
func (s *Store) DeleteMilestone(ctx context.Context, pk int64) error {
	q := s.rebind(`DELETE FROM milestones WHERE pk = ?`)
	res, err := s.db.ExecContext(ctx, q, pk)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanMilestone(row interface{ Scan(...any) error }) (*MilestoneRow, error) {
	m, err := scanMilestoneRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

func scanMilestoneRows(row interface{ Scan(...any) error }) (*MilestoneRow, error) {
	var (
		m                       MilestoneRow
		desc                    sql.NullString
		creator                 sql.NullInt64
		dueOn, closedAt         nullTime
		created, updated        nullTime
	)
	if err := row.Scan(&m.PK, &m.DBID, &m.RepoPK, &m.Number, &m.Title, &desc, &m.State,
		&dueOn, &creator, &closedAt, &created, &updated); err != nil {
		return nil, err
	}
	m.Description = strPtr(desc)
	m.CreatorPK = i64Ptr(creator)
	m.DueOn = dueOn.ptr()
	m.ClosedAt = closedAt.ptr()
	m.CreatedAt, m.UpdatedAt = created.Time, updated.Time
	return &m, nil
}

// argI64 binds a nullable int64: a nil pointer becomes SQL NULL.
func argI64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

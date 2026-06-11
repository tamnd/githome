package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// LabelRow is a row of the labels table: a named, colored tag scoped to one
// repository. Color is stored as a six-hex string without the leading hash, the
// form GitHub's API returns. Description is nullable.
type LabelRow struct {
	PK          int64
	DBID        int64
	RepoPK      int64
	Name        string
	Color       string
	Description *string
	IsDefault   bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const labelColumns = `pk, db_id, repo_pk, name, color, description, is_default, created_at, updated_at`

// ListLabels returns a repository's labels in name order.
func (s *Store) ListLabels(ctx context.Context, repoPK int64) ([]LabelRow, error) {
	q := s.rebind(`SELECT ` + labelColumns + ` FROM labels WHERE repo_pk = ? ORDER BY lower(name)`)
	rows, err := s.rdb.QueryContext(ctx, q, repoPK)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []LabelRow
	for rows.Next() {
		l, err := scanLabelRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// GetLabel resolves a single label by name within a repository,
// case-insensitively, matching GitHub's case-preserving but case-insensitive
// label names.
func (s *Store) GetLabel(ctx context.Context, repoPK int64, name string) (*LabelRow, error) {
	q := s.rebind(`SELECT ` + labelColumns + ` FROM labels
		WHERE repo_pk = ? AND lower(name) = lower(?)`)
	return scanLabel(s.rdb.QueryRowContext(ctx, q, repoPK, name))
}

// LabelsByNames resolves the named labels within a repository, skipping any that
// do not exist. The result preserves the order the names were given so the
// caller can render labels in request order.
func (s *Store) LabelsByNames(ctx context.Context, repoPK int64, names []string) ([]LabelRow, error) {
	found := map[string]LabelRow{}
	for _, n := range names {
		l, err := s.GetLabel(ctx, repoPK, n)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		found[strings.ToLower(n)] = *l
	}
	out := make([]LabelRow, 0, len(names))
	seen := map[int64]bool{}
	for _, n := range names {
		if l, ok := found[strings.ToLower(n)]; ok && !seen[l.PK] {
			out = append(out, l)
			seen[l.PK] = true
		}
	}
	return out, nil
}

// LabelsByIssue returns the labels attached to one issue, in name order.
func (s *Store) LabelsByIssue(ctx context.Context, issuePK int64) ([]LabelRow, error) {
	q := s.rebind(`SELECT ` + labelColumnsAliased + ` FROM labels l
		JOIN issue_labels il ON il.label_pk = l.pk
		WHERE il.issue_pk = ? ORDER BY lower(l.name)`)
	rows, err := s.rdb.QueryContext(ctx, q, issuePK)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []LabelRow
	for rows.Next() {
		l, err := scanLabelRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

const labelColumnsAliased = `l.pk, l.db_id, l.repo_pk, l.name, l.color, l.description, l.is_default, l.created_at, l.updated_at`

// InsertLabel writes a label and fills the server-assigned fields back onto l.
func (s *Store) InsertLabel(ctx context.Context, l *LabelRow) error {
	dbID, err := s.AllocDBID(ctx)
	if err != nil {
		return err
	}
	if l.Color == "" {
		l.Color = "ededed"
	}
	q := s.rebind(`INSERT INTO labels (db_id, repo_pk, name, color, description, is_default)
		VALUES (?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id, created_at, updated_at`)
	var created, updated nullTime
	err = s.db.QueryRowContext(ctx, q,
		dbID, l.RepoPK, l.Name, l.Color, argStr(l.Description), l.IsDefault,
	).Scan(&l.PK, &l.DBID, &created, &updated)
	if err != nil {
		return err
	}
	l.CreatedAt, l.UpdatedAt = created.Time, updated.Time
	return nil
}

// UpdateLabel changes a label's name, color, and description, returning the
// updated row.
func (s *Store) UpdateLabel(ctx context.Context, l *LabelRow) error {
	q := s.rebind(`UPDATE labels SET name = ?, color = ?, description = ?, updated_at = ?
		WHERE pk = ? RETURNING created_at, updated_at`)
	var created, updated nullTime
	err := s.db.QueryRowContext(ctx, q,
		l.Name, l.Color, argStr(l.Description), nowUTC(), l.PK,
	).Scan(&created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	l.CreatedAt, l.UpdatedAt = created.Time, updated.Time
	return nil
}

// DeleteLabel removes a label; the issue_labels rows cascade.
func (s *Store) DeleteLabel(ctx context.Context, pk int64) error {
	q := s.rebind(`DELETE FROM labels WHERE pk = ?`)
	res, err := s.db.ExecContext(ctx, q, pk)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetLabelByDBID resolves a single label by its public database id.
func (s *Store) GetLabelByDBID(ctx context.Context, dbID int64) (*LabelRow, error) {
	q := s.rebind(`SELECT ` + labelColumns + ` FROM labels WHERE db_id = ?`)
	return scanLabel(s.rdb.QueryRowContext(ctx, q, dbID))
}

// AddLabels attaches the given labels to an issue, ignoring any that are already
// attached. It is the additive counterpart to ReplaceLabels.
func (t *Tx) AddLabels(ctx context.Context, issuePK int64, labelPKs []int64) error {
	return t.AttachLabels(ctx, issuePK, labelPKs)
}

// RemoveLabels detaches the given labels from an issue, ignoring any that are
// not currently attached.
func (t *Tx) RemoveLabels(ctx context.Context, issuePK int64, labelPKs []int64) error {
	for _, lp := range labelPKs {
		q := t.rebind(`DELETE FROM issue_labels WHERE issue_pk = ? AND label_pk = ?`)
		if _, err := t.tx.ExecContext(ctx, q, issuePK, lp); err != nil {
			return err
		}
	}
	return nil
}

// InsertLabel writes a label inside a transaction, used to seed a repository's
// default label set as part of the repository-create unit of work.
func (t *Tx) InsertLabel(ctx context.Context, l *LabelRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	if l.Color == "" {
		l.Color = "ededed"
	}
	q := t.rebind(`INSERT INTO labels (db_id, repo_pk, name, color, description, is_default)
		VALUES (?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id, created_at, updated_at`)
	var created, updated nullTime
	err = t.tx.QueryRowContext(ctx, q,
		dbID, l.RepoPK, l.Name, l.Color, argStr(l.Description), l.IsDefault,
	).Scan(&l.PK, &l.DBID, &created, &updated)
	if err != nil {
		return err
	}
	l.CreatedAt, l.UpdatedAt = created.Time, updated.Time
	return nil
}

func scanLabel(row interface{ Scan(...any) error }) (*LabelRow, error) {
	l, err := scanLabelRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return l, err
}

func scanLabelRows(row interface{ Scan(...any) error }) (*LabelRow, error) {
	var (
		l         LabelRow
		desc      sql.NullString
		isDefault boolVal
		created   nullTime
		updated   nullTime
	)
	if err := row.Scan(&l.PK, &l.DBID, &l.RepoPK, &l.Name, &l.Color, &desc, &isDefault, &created, &updated); err != nil {
		return nil, err
	}
	l.Description = strPtr(desc)
	l.IsDefault = isDefault.Bool
	l.CreatedAt, l.UpdatedAt = created.Time, updated.Time
	return &l, nil
}

// nowUTC returns the current time in UTC for the explicit updated_at touches the
// write paths apply, so a row's timestamp advances on edit on both dialects.
func nowUTC() time.Time { return time.Now().UTC() }

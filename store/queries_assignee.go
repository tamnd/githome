package store

import (
	"context"
	"database/sql"
	"errors"
)

// ListAssigneePKs returns the user primary keys assigned to an issue, in the
// order they were assigned (the position column). The presenter resolves these
// to user rows; keeping the join read here avoids loading whole user rows when a
// caller only needs the set.
func (s *Store) ListAssigneePKs(ctx context.Context, issuePK int64) ([]int64, error) {
	q := s.rebind(`SELECT user_pk FROM assignees WHERE issue_pk = ? ORDER BY position, user_pk`)
	rows, err := s.db.QueryContext(ctx, q, issuePK)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var pk int64
		if err := rows.Scan(&pk); err != nil {
			return nil, err
		}
		out = append(out, pk)
	}
	return out, rows.Err()
}

// IsAssigned reports whether a user is currently assigned to an issue.
func (s *Store) IsAssigned(ctx context.Context, issuePK, userPK int64) (bool, error) {
	q := s.rebind(`SELECT 1 FROM assignees WHERE issue_pk = ? AND user_pk = ? LIMIT 1`)
	var one int
	switch err := s.db.QueryRowContext(ctx, q, issuePK, userPK).Scan(&one); {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, err
	}
}

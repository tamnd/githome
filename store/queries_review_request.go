package store

import (
	"context"
	"strings"
)

// The review request store. A requested reviewer is a (pull, user) pair in
// review_requests, the table behind a pull request's requested_reviewers
// field. The position column keeps the request order so the rendered list is
// stable; re-requesting an already requested reviewer is a no-op.

// ListReviewRequestPKs returns the user primary keys requested to review a
// pull request, in request order.
func (s *Store) ListReviewRequestPKs(ctx context.Context, pullPK int64) ([]int64, error) {
	q := s.rebind(`SELECT reviewer_pk FROM review_requests
		WHERE pull_pk = ? ORDER BY position, reviewer_pk`)
	rows, err := s.rdb.QueryContext(ctx, q, pullPK)
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

// ReviewRequestsByPullPKs loads the requested reviewers for the given pull
// PKs in one query, a map from pull_pk to the ordered reviewer user PKs, the
// bulk read the pulls list assembly uses.
func (s *Store) ReviewRequestsByPullPKs(ctx context.Context, pullPKs []int64) (map[int64][]int64, error) {
	if len(pullPKs) == 0 {
		return map[int64][]int64{}, nil
	}
	q := s.rebind(`SELECT pull_pk, reviewer_pk FROM review_requests
		WHERE pull_pk IN ` + inPlaceholders(len(pullPKs)) + `
		ORDER BY pull_pk, position, reviewer_pk`)
	rows, err := s.rdb.QueryContext(ctx, q, i64Args(pullPKs)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64][]int64, len(pullPKs))
	for rows.Next() {
		var pullPK, reviewerPK int64
		if err := rows.Scan(&pullPK, &reviewerPK); err != nil {
			return nil, err
		}
		out[pullPK] = append(out[pullPK], reviewerPK)
	}
	return out, rows.Err()
}

// AddReviewRequests links the given users as requested reviewers of the pull
// request, appending after the current positions and ignoring pairs that
// already exist.
func (s *Store) AddReviewRequests(ctx context.Context, pullPK int64, reviewerPKs []int64) error {
	if len(reviewerPKs) == 0 {
		return nil
	}
	var base int
	row := s.db.QueryRowContext(ctx, s.rebind(`SELECT COALESCE(MAX(position)+1, 0)
		FROM review_requests WHERE pull_pk = ?`), pullPK)
	if err := row.Scan(&base); err != nil {
		return err
	}
	rows := make([]string, len(reviewerPKs))
	args := make([]any, 0, 3*len(reviewerPKs))
	for i, rp := range reviewerPKs {
		rows[i] = "(?, ?, ?)"
		args = append(args, pullPK, rp, base+i)
	}
	q := s.rebind(`INSERT INTO review_requests (pull_pk, reviewer_pk, position) VALUES ` +
		strings.Join(rows, ", ") + ` ON CONFLICT (pull_pk, reviewer_pk) DO NOTHING`)
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

// RemoveReviewRequests unlinks the given users from the pull request's
// requested reviewers in one statement.
func (s *Store) RemoveReviewRequests(ctx context.Context, pullPK int64, reviewerPKs []int64) error {
	if len(reviewerPKs) == 0 {
		return nil
	}
	q := s.rebind(`DELETE FROM review_requests
		WHERE pull_pk = ? AND reviewer_pk IN ` + inPlaceholders(len(reviewerPKs)))
	args := append([]any{pullPK}, i64Args(reviewerPKs)...)
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

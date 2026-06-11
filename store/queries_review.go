package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// The review store. A review and its inline comments are written together inside
// one transaction (the service opens it), so the Insert forms here are
// transaction-scoped; the reads are plain Store methods. Reviews resolve by pk
// (the natural key once created), by db_id (a node id decodes to it), as a pull
// request's list, and as the one pending draft a user holds open on a pull
// request. Comments resolve by db_id, as a pull request's full list, and as a
// review's batch.

const reviewColumns = `pk, db_id, pull_pk, repo_pk, user_pk, state, body,
	commit_id, dismissed_message, submitted_at, created_at, updated_at`

// InsertReview writes a review row with a freshly allocated db_id, filling the
// server-assigned fields back onto r.
func (t *Tx) InsertReview(ctx context.Context, r *ReviewRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	q := t.rebind(`INSERT INTO pull_request_reviews
		(db_id, pull_pk, repo_pk, user_pk, state, body, commit_id, submitted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id, created_at, updated_at`)
	var created, upd nullTime
	err = t.tx.QueryRowContext(ctx, q,
		dbID, r.PullPK, r.RepoPK, r.UserPK, r.State, r.Body, r.CommitID,
		argTime(r.SubmittedAt),
	).Scan(&r.PK, &r.DBID, &created, &upd)
	if err != nil {
		return err
	}
	r.CreatedAt, r.UpdatedAt = created.Time, upd.Time
	return nil
}

// GetReviewByPK resolves a review by primary key.
func (s *Store) GetReviewByPK(ctx context.Context, pk int64) (*ReviewRow, error) {
	q := s.rebind(`SELECT ` + reviewColumns + ` FROM pull_request_reviews WHERE pk = ?`)
	return scanReview(s.rdb.QueryRowContext(ctx, q, pk))
}

// GetReviewByDBID resolves a review by its public database id, the value a
// PullRequestReview node id decodes to.
func (s *Store) GetReviewByDBID(ctx context.Context, dbID int64) (*ReviewRow, error) {
	q := s.rebind(`SELECT ` + reviewColumns + ` FROM pull_request_reviews WHERE db_id = ?`)
	return scanReview(s.rdb.QueryRowContext(ctx, q, dbID))
}

// PendingReviewFor returns the user's open pending review on a pull request, or
// ErrNotFound when they hold none. It enforces the one-pending-draft rule the
// service checks before opening a new draft.
func (s *Store) PendingReviewFor(ctx context.Context, pullPK, userPK int64) (*ReviewRow, error) {
	q := s.rebind(`SELECT ` + reviewColumns + ` FROM pull_request_reviews
		WHERE pull_pk = ? AND user_pk = ? AND state = 'PENDING'`)
	return scanReview(s.rdb.QueryRowContext(ctx, q, pullPK, userPK))
}

// DeleteReview hard-deletes a pending review by primary key. Only pending
// (PENDING state) reviews may be deleted; submitted reviews are immutable.
func (s *Store) DeleteReview(ctx context.Context, pk int64) error {
	q := s.rebind(`DELETE FROM pull_request_reviews WHERE pk = ? AND state = 'PENDING'`)
	_, err := s.db.ExecContext(ctx, q, pk)
	return err
}

// ListReviews returns a pull request's submitted reviews in chronological order.
// Pending drafts are excluded: they are private to their author until submitted.
func (s *Store) ListReviews(ctx context.Context, pullPK int64) ([]ReviewRow, error) {
	q := s.rebind(`SELECT ` + reviewColumns + ` FROM pull_request_reviews
		WHERE pull_pk = ? AND state <> 'PENDING'
		ORDER BY submitted_at, pk`)
	rows, err := s.rdb.QueryContext(ctx, q, pullPK)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ReviewRow
	for rows.Next() {
		r, err := scanReviewRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// SubmitReview stamps a pending review with its final state, body, and submit
// instant, the transition from draft to a review the pull request shows.
func (t *Tx) SubmitReview(ctx context.Context, pk int64, state, body, commitID string, submittedAt time.Time) error {
	q := t.rebind(`UPDATE pull_request_reviews SET
		state = ?, body = ?, commit_id = ?, submitted_at = ?, updated_at = ?
		WHERE pk = ?`)
	_, err := t.tx.ExecContext(ctx, q, state, body, commitID, submittedAt, nowUTC(), pk)
	return err
}

// DismissReview marks a submitted review dismissed with a reason, dropping its
// approval or change request from the decision without deleting its comments.
func (s *Store) DismissReview(ctx context.Context, pk int64, message string) error {
	q := s.rebind(`UPDATE pull_request_reviews SET
		state = 'DISMISSED', dismissed_message = ?, updated_at = ?
		WHERE pk = ?`)
	res, err := s.db.ExecContext(ctx, q, message, nowUTC(), pk)
	if err != nil {
		return err
	}
	return affectedOrNotFound(res)
}

func scanReview(row interface{ Scan(...any) error }) (*ReviewRow, error) {
	r, err := scanReviewRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func scanReviewRows(row interface{ Scan(...any) error }) (*ReviewRow, error) {
	var (
		r            ReviewRow
		dismissed    sql.NullString
		submitted    nullTime
		created, upd nullTime
	)
	if err := row.Scan(&r.PK, &r.DBID, &r.PullPK, &r.RepoPK, &r.UserPK, &r.State,
		&r.Body, &r.CommitID, &dismissed, &submitted, &created, &upd); err != nil {
		return nil, err
	}
	r.DismissedMessage = strPtr(dismissed)
	r.SubmittedAt = submitted.ptr()
	r.CreatedAt, r.UpdatedAt = created.Time, upd.Time
	return &r, nil
}

const reviewCommentColumns = `pk, db_id, review_pk, pull_pk, repo_pk, user_pk,
	path, side, line, start_line, start_side, original_line, original_start_line,
	position, original_position, commit_id, original_commit_id, in_reply_to_pk,
	diff_hunk, subject_type, body, resolved, resolved_by_pk, created_at, updated_at`

// InsertReviewComment writes one inline comment with a freshly allocated db_id,
// filling the server-assigned fields back onto c.
func (t *Tx) InsertReviewComment(ctx context.Context, c *ReviewCommentRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	if c.Side == "" {
		c.Side = "RIGHT"
	}
	if c.SubjectType == "" {
		c.SubjectType = "line"
	}
	q := t.rebind(`INSERT INTO pull_request_review_comments
		(db_id, review_pk, pull_pk, repo_pk, user_pk, path, side, line, start_line,
		 start_side, original_line, original_start_line, position, original_position,
		 commit_id, original_commit_id, in_reply_to_pk, diff_hunk, subject_type, body)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id, created_at, updated_at`)
	var created, upd nullTime
	err = t.tx.QueryRowContext(ctx, q,
		dbID, c.ReviewPK, c.PullPK, c.RepoPK, c.UserPK, c.Path, c.Side,
		argI64(c.Line), argI64(c.StartLine), argStr(c.StartSide),
		argI64(c.OriginalLine), argI64(c.OriginalStartLine),
		argI64(c.Position), argI64(c.OriginalPosition),
		c.CommitID, c.OriginalCommitID, argI64(c.InReplyToPK),
		c.DiffHunk, c.SubjectType, c.Body,
	).Scan(&c.PK, &c.DBID, &created, &upd)
	if err != nil {
		return err
	}
	c.CreatedAt, c.UpdatedAt = created.Time, upd.Time
	return nil
}

// GetReviewComment resolves an inline comment by its public database id.
func (s *Store) GetReviewComment(ctx context.Context, dbID int64) (*ReviewCommentRow, error) {
	q := s.rebind(`SELECT ` + reviewCommentColumns + ` FROM pull_request_review_comments WHERE db_id = ?`)
	return scanReviewComment(s.rdb.QueryRowContext(ctx, q, dbID))
}

// GetReviewCommentByPK resolves an inline comment by primary key.
func (s *Store) GetReviewCommentByPK(ctx context.Context, pk int64) (*ReviewCommentRow, error) {
	q := s.rebind(`SELECT ` + reviewCommentColumns + ` FROM pull_request_review_comments WHERE pk = ?`)
	return scanReviewComment(s.rdb.QueryRowContext(ctx, q, pk))
}

// ListReviewComments returns every inline comment on a pull request, oldest
// first, the set the thread grouping in the GraphQL layer folds into threads.
func (s *Store) ListReviewComments(ctx context.Context, pullPK int64) ([]ReviewCommentRow, error) {
	return s.queryReviewComments(ctx, `WHERE pull_pk = ? ORDER BY created_at, pk`, pullPK)
}

// ListReviewCommentsForReview returns the inline comments a single review owns.
func (s *Store) ListReviewCommentsForReview(ctx context.Context, reviewPK int64) ([]ReviewCommentRow, error) {
	return s.queryReviewComments(ctx, `WHERE review_pk = ? ORDER BY created_at, pk`, reviewPK)
}

func (s *Store) queryReviewComments(ctx context.Context, where string, args ...any) ([]ReviewCommentRow, error) {
	q := s.rebind(`SELECT ` + reviewCommentColumns + ` FROM pull_request_review_comments ` + where)
	rows, err := s.rdb.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ReviewCommentRow
	for rows.Next() {
		c, err := scanReviewCommentRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// UpdateReviewCommentBody changes an inline comment's body and stamps updated_at.
func (s *Store) UpdateReviewCommentBody(ctx context.Context, pk int64, body string) error {
	q := s.rebind(`UPDATE pull_request_review_comments SET body = ?, updated_at = ?
		WHERE pk = ?`)
	res, err := s.db.ExecContext(ctx, q, body, nowUTC(), pk)
	if err != nil {
		return err
	}
	return affectedOrNotFound(res)
}

// SetThreadResolved resolves or unresolves the thread a comment roots: every
// comment sharing its root (itself or its in_reply_to chain head) flips together.
// resolverPK is recorded on resolve and cleared on unresolve.
func (s *Store) SetThreadResolved(ctx context.Context, rootPK int64, resolved bool, resolverPK *int64) error {
	q := s.rebind(`UPDATE pull_request_review_comments SET
		resolved = ?, resolved_by_pk = ?, updated_at = ?
		WHERE pk = ? OR in_reply_to_pk = ?`)
	res, err := s.db.ExecContext(ctx, q, argBool(&resolved), argI64(resolverPK), nowUTC(), rootPK, rootPK)
	if err != nil {
		return err
	}
	return affectedOrNotFound(res)
}

func scanReviewComment(row interface{ Scan(...any) error }) (*ReviewCommentRow, error) {
	c, err := scanReviewCommentRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func scanReviewCommentRows(row interface{ Scan(...any) error }) (*ReviewCommentRow, error) {
	var (
		c                                             ReviewCommentRow
		line, startLine, origLine, origStartLine      sql.NullInt64
		position, origPosition, inReplyTo, resolvedBy sql.NullInt64
		startSide                                     sql.NullString
		resolved                                      boolVal
		created, upd                                  nullTime
	)
	if err := row.Scan(&c.PK, &c.DBID, &c.ReviewPK, &c.PullPK, &c.RepoPK, &c.UserPK,
		&c.Path, &c.Side, &line, &startLine, &startSide, &origLine, &origStartLine,
		&position, &origPosition, &c.CommitID, &c.OriginalCommitID, &inReplyTo,
		&c.DiffHunk, &c.SubjectType, &c.Body, &resolved, &resolvedBy,
		&created, &upd); err != nil {
		return nil, err
	}
	c.Line = i64Ptr(line)
	c.StartLine = i64Ptr(startLine)
	c.StartSide = strPtr(startSide)
	c.OriginalLine = i64Ptr(origLine)
	c.OriginalStartLine = i64Ptr(origStartLine)
	c.Position = i64Ptr(position)
	c.OriginalPosition = i64Ptr(origPosition)
	c.InReplyToPK = i64Ptr(inReplyTo)
	c.Resolved = resolved.Bool
	c.ResolvedByPK = i64Ptr(resolvedBy)
	c.CreatedAt, c.UpdatedAt = created.Time, upd.Time
	return &c, nil
}

// DeleteReviewComment removes an inline comment by primary key.
func (s *Store) DeleteReviewComment(ctx context.Context, pk int64) error {
	q := s.rebind(`DELETE FROM pull_request_review_comments WHERE pk = ?`)
	res, err := s.db.ExecContext(ctx, q, pk)
	if err != nil {
		return err
	}
	return affectedOrNotFound(res)
}

// ListAllReviewComments returns all inline comments in a repository, oldest first.
func (s *Store) ListAllReviewComments(ctx context.Context, repoPK int64) ([]ReviewCommentRow, error) {
	return s.queryReviewComments(ctx, `WHERE repo_pk = ? ORDER BY created_at, pk`, repoPK)
}


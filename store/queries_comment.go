package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CommentRow is a row of the issue_comments table: a comment on an issue or pull
// request (both live in the issues table). Soft deletes keep the row so reaction
// and event history stays intact; reads skip deleted rows.
type CommentRow struct {
	PK        int64
	DBID      int64
	IssuePK   int64
	UserPK    int64
	Body      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

const commentColumns = `pk, db_id, issue_pk, user_pk, body, created_at, updated_at`

// ListIssueComments returns an issue's comments in chronological order, one page
// at a time. A limit of zero returns the default page of 30.
func (s *Store) ListIssueComments(ctx context.Context, issuePK int64, limit, offset int) ([]CommentRow, error) {
	if limit <= 0 {
		limit = 30
	}
	q := s.rebind(`SELECT ` + commentColumns + ` FROM issue_comments
		WHERE issue_pk = ? AND deleted_at IS NULL
		ORDER BY created_at, pk LIMIT ? OFFSET ?`)
	rows, err := s.rdb.QueryContext(ctx, q, issuePK, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CommentRow
	for rows.Next() {
		c, err := scanCommentRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// ListIssueCommentsAfter returns the comments after the (createdAt, pk) seek
// key in the same chronological (created_at, pk) order ListIssueComments pages
// over, so a cursor walk costs a single index range scan per page regardless
// of how deep into the thread it is. The caller recovers the seek pair from
// the comment the cursor names.
func (s *Store) ListIssueCommentsAfter(ctx context.Context, issuePK int64, createdAt time.Time, afterPK int64, limit int) ([]CommentRow, error) {
	if limit <= 0 {
		limit = 30
	}
	// The row-value comparison is what both planners turn into an index range
	// bound, the same shape the issue keyset list uses.
	q := s.rebind(`SELECT ` + commentColumns + ` FROM issue_comments
		WHERE issue_pk = ? AND deleted_at IS NULL AND (created_at, pk) > (?, ?)
		ORDER BY created_at, pk LIMIT ?`)
	rows, err := s.rdb.QueryContext(ctx, q, issuePK, s.timeArg(createdAt), afterPK, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CommentRow
	for rows.Next() {
		c, err := scanCommentRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetComment resolves a single comment by its public database id.
func (s *Store) GetComment(ctx context.Context, dbID int64) (*CommentRow, error) {
	q := s.rebind(`SELECT ` + commentColumns + ` FROM issue_comments
		WHERE db_id = ? AND deleted_at IS NULL`)
	return scanComment(s.rdb.QueryRowContext(ctx, q, dbID))
}

// GetCommentByPK resolves a single comment by primary key.
func (s *Store) GetCommentByPK(ctx context.Context, pk int64) (*CommentRow, error) {
	q := s.rebind(`SELECT ` + commentColumns + ` FROM issue_comments
		WHERE pk = ? AND deleted_at IS NULL`)
	return scanComment(s.rdb.QueryRowContext(ctx, q, pk))
}

// UpdateComment changes a comment's body and stamps updated_at.
func (s *Store) UpdateComment(ctx context.Context, c *CommentRow) error {
	q := s.rebind(`UPDATE issue_comments SET body = ?, updated_at = ?
		WHERE pk = ? AND deleted_at IS NULL RETURNING created_at, updated_at`)
	var created, upd nullTime
	err := s.db.QueryRowContext(ctx, q, c.Body, nowUTC(), c.PK).Scan(&created, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	c.CreatedAt, c.UpdatedAt = created.Time, upd.Time
	return nil
}

// DeleteComment soft deletes a comment and decrements its issue's cached count
// in one transaction so the count and the visible rows stay consistent.
func (s *Store) DeleteComment(ctx context.Context, pk int64) error {
	return s.WithTx(ctx, func(t *Tx) error {
		q := t.rebind(`UPDATE issue_comments SET deleted_at = ?
			WHERE pk = ? AND deleted_at IS NULL RETURNING issue_pk`)
		var issuePK int64
		err := t.tx.QueryRowContext(ctx, q, nowUTC(), pk).Scan(&issuePK)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return t.BumpCommentsCount(ctx, issuePK, -1)
	})
}

// InsertComment writes a comment and bumps the issue's cached comment count in
// one transaction, filling the server-assigned fields back onto c.
func (s *Store) InsertComment(ctx context.Context, c *CommentRow) error {
	return s.WithTx(ctx, func(t *Tx) error { return t.InsertComment(ctx, c) })
}

// InsertComment is the transaction-scoped form, so a comment created as part of
// a larger unit of work shares its atomicity.
func (t *Tx) InsertComment(ctx context.Context, c *CommentRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	q := t.rebind(`INSERT INTO issue_comments (db_id, issue_pk, user_pk, body)
		VALUES (?, ?, ?, ?)
		RETURNING pk, db_id, created_at, updated_at`)
	var created, upd nullTime
	err = t.tx.QueryRowContext(ctx, q, dbID, c.IssuePK, c.UserPK, c.Body).
		Scan(&c.PK, &c.DBID, &created, &upd)
	if err != nil {
		return err
	}
	c.CreatedAt, c.UpdatedAt = created.Time, upd.Time
	return t.BumpCommentsCount(ctx, c.IssuePK, 1)
}

// BumpCommentsCount adjusts an issue's cached comment count and advances its
// updated_at, so the issue view need not aggregate comments on read.
func (t *Tx) BumpCommentsCount(ctx context.Context, issuePK int64, delta int) error {
	q := t.rebind(`UPDATE issues SET comments_count = comments_count + ?, updated_at = ?
		WHERE pk = ?`)
	_, err := t.tx.ExecContext(ctx, q, delta, nowUTC(), issuePK)
	return err
}

func scanComment(row interface{ Scan(...any) error }) (*CommentRow, error) {
	c, err := scanCommentRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func scanCommentRows(row interface{ Scan(...any) error }) (*CommentRow, error) {
	var (
		c            CommentRow
		created, upd nullTime
	)
	if err := row.Scan(&c.PK, &c.DBID, &c.IssuePK, &c.UserPK, &c.Body, &created, &upd); err != nil {
		return nil, err
	}
	c.CreatedAt, c.UpdatedAt = created.Time, upd.Time
	return &c, nil
}

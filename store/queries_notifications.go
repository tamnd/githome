package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// NotificationThreadRow is a row of the notification_threads table: one user's
// view of one issue or pull request conversation.
type NotificationThreadRow struct {
	PK         int64
	UserPK     int64
	RepoPK     int64
	IssuePK    int64
	Reason     string
	Unread     bool
	Subscribed bool
	Ignored    bool
	LastReadAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

const notificationThreadColumns = `pk, user_pk, repo_pk, issue_pk, reason, unread,
	subscribed, ignored, last_read_at, created_at, updated_at`

// UpsertNotificationThread records that something happened on an issue the user
// should hear about. A fresh thread starts unread; an existing one is bumped to
// unread with the new reason and timestamp unless the user has ignored it, in
// which case only the timestamp moves.
func (s *Store) UpsertNotificationThread(ctx context.Context, r *NotificationThreadRow) error {
	q := s.rebind(`INSERT INTO notification_threads
		(user_pk, repo_pk, issue_pk, reason, unread, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_pk, issue_pk) DO UPDATE SET
		  reason = excluded.reason,
		  updated_at = excluded.updated_at,
		  unread = CASE WHEN notification_threads.ignored
		           THEN notification_threads.unread ELSE excluded.unread END
		RETURNING pk`)
	return s.db.QueryRowContext(ctx, q,
		r.UserPK, r.RepoPK, r.IssuePK, r.Reason, true, nowUTC(),
	).Scan(&r.PK)
}

// ListNotificationThreads returns one page of a user's threads, most recently
// updated first, with the total for the same filter. A zero repoPK spans all
// repositories; all=false keeps only unread threads, GitHub's default view.
func (s *Store) ListNotificationThreads(ctx context.Context, userPK, repoPK int64, all bool, limit, offset int) ([]*NotificationThreadRow, int, error) {
	where := ` WHERE user_pk = ?`
	args := []any{userPK}
	if repoPK != 0 {
		where += ` AND repo_pk = ?`
		args = append(args, repoPK)
	}
	if !all {
		where += ` AND unread`
	}

	var total int
	if err := s.db.QueryRowContext(ctx,
		s.rebind(`SELECT COUNT(*) FROM notification_threads`+where), args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	if limit <= 0 {
		limit = 30
	}
	q := s.rebind(`SELECT ` + notificationThreadColumns +
		` FROM notification_threads` + where +
		` ORDER BY updated_at DESC, pk DESC LIMIT ? OFFSET ?`)
	rows, err := s.db.QueryContext(ctx, q, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*NotificationThreadRow
	for rows.Next() {
		t, err := scanNotificationThreadRow(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

// NotificationThreadByPK loads a single thread.
func (s *Store) NotificationThreadByPK(ctx context.Context, pk int64) (*NotificationThreadRow, error) {
	q := s.rebind(`SELECT ` + notificationThreadColumns +
		` FROM notification_threads WHERE pk = ?`)
	t, err := scanNotificationThreadRow(s.db.QueryRowContext(ctx, q, pk))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// MarkNotificationThreadRead marks one thread read and stamps last_read_at.
func (s *Store) MarkNotificationThreadRead(ctx context.Context, pk int64) error {
	q := s.rebind(`UPDATE notification_threads SET unread = ?, last_read_at = ? WHERE pk = ?`)
	res, err := s.db.ExecContext(ctx, q, false, nowUTC(), pk)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkNotificationThreadsRead marks all of a user's threads read, optionally
// scoped to one repository (zero repoPK spans all).
func (s *Store) MarkNotificationThreadsRead(ctx context.Context, userPK, repoPK int64) error {
	q := `UPDATE notification_threads SET unread = ?, last_read_at = ? WHERE user_pk = ?`
	args := []any{false, nowUTC(), userPK}
	if repoPK != 0 {
		q += ` AND repo_pk = ?`
		args = append(args, repoPK)
	}
	_, err := s.db.ExecContext(ctx, s.rebind(q), args...)
	return err
}

// SetNotificationThreadSubscription updates a thread's subscription flags.
func (s *Store) SetNotificationThreadSubscription(ctx context.Context, pk int64, subscribed, ignored bool) error {
	q := s.rebind(`UPDATE notification_threads SET subscribed = ?, ignored = ? WHERE pk = ?`)
	res, err := s.db.ExecContext(ctx, q, subscribed, ignored, pk)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteNotificationThread removes a thread, the storage side of marking a
// notification done.
func (s *Store) DeleteNotificationThread(ctx context.Context, pk int64) error {
	res, err := s.db.ExecContext(ctx, s.rebind(`DELETE FROM notification_threads WHERE pk = ?`), pk)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanNotificationThreadRow(row interface{ Scan(...any) error }) (*NotificationThreadRow, error) {
	var (
		t                          NotificationThreadRow
		lastRead, created, updated nullTime
	)
	err := row.Scan(
		&t.PK, &t.UserPK, &t.RepoPK, &t.IssuePK, &t.Reason, &t.Unread,
		&t.Subscribed, &t.Ignored, &lastRead, &created, &updated,
	)
	if err != nil {
		return nil, err
	}
	t.LastReadAt = lastRead.ptr()
	t.CreatedAt = created.Time
	t.UpdatedAt = updated.Time
	return &t, nil
}

package store

import (
	"context"
	"database/sql"
	"errors"
)

// queries_social.go holds the social graph: stars (who starred which
// repository), subscriptions (who is watching a repository), and follows (who
// follows whom). Each is a join table, so the relationship is present or absent
// rather than counted in a column that could drift; the counts are read live
// off the tables. The list queries order by the row's creation so a listing is
// stable, and the user-bearing listings join users so the caller scans a
// UserRow directly. A limit of zero or below returns the whole list, the form
// the domain layer uses before it filters by visibility and pages in memory.
// See 0034_social_graph and 2001/review/01 R01-27.

// usersColumns is userColumns qualified with the users table name. The social
// listings join users against a relationship table (stars, follows,
// repo_subscriptions) that also carries pk and created_at, so the bare column
// list would be ambiguous; this one names the side every column comes from.
const usersColumns = `users.pk, users.db_id, users.login, users.type, users.name,
	users.email, users.site_admin, users.company, users.blog, users.location,
	users.bio, users.hireable, users.twitter_username, users.public_repos,
	users.public_gists, users.followers, users.following, users.created_at,
	users.updated_at`

// limitClause renders the trailing LIMIT/OFFSET for a social listing, or the
// empty string when limit is zero or below, meaning "every row". It returns the
// bind args to append alongside.
func limitClause(limit, offset int) (string, []any) {
	if limit <= 0 {
		return "", nil
	}
	return " LIMIT ? OFFSET ?", []any{limit, offset}
}

// --- stars ---

// InsertStar records that userPK starred repoPK. A star already present is left
// as-is, so a repeated PUT is idempotent the way GitHub's is.
func (s *Store) InsertStar(ctx context.Context, userPK, repoPK int64) error {
	q := s.rebind(`INSERT INTO stars (user_pk, repo_pk) VALUES (?, ?)
		ON CONFLICT (user_pk, repo_pk) DO NOTHING`)
	_, err := s.db.ExecContext(ctx, q, userPK, repoPK)
	return err
}

// DeleteStar removes userPK's star on repoPK. Removing a star that is not there
// is not an error, matching the idempotent DELETE.
func (s *Store) DeleteStar(ctx context.Context, userPK, repoPK int64) error {
	q := s.rebind(`DELETE FROM stars WHERE user_pk = ? AND repo_pk = ?`)
	_, err := s.db.ExecContext(ctx, q, userPK, repoPK)
	return err
}

// IsStarred reports whether userPK has starred repoPK.
func (s *Store) IsStarred(ctx context.Context, userPK, repoPK int64) (bool, error) {
	q := s.rebind(`SELECT 1 FROM stars WHERE user_pk = ? AND repo_pk = ?`)
	var one int
	err := s.rdb.QueryRowContext(ctx, q, userPK, repoPK).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// StarCount reports how many users have starred repoPK, the stargazers_count.
func (s *Store) StarCount(ctx context.Context, repoPK int64) (int, error) {
	q := s.rebind(`SELECT COUNT(*) FROM stars WHERE repo_pk = ?`)
	var n int
	err := s.rdb.QueryRowContext(ctx, q, repoPK).Scan(&n)
	return n, err
}

// StargazersByRepo lists the users who starred repoPK, most recent star first,
// paged by limit and offset.
func (s *Store) StargazersByRepo(ctx context.Context, repoPK int64, limit, offset int) ([]*UserRow, error) {
	lc, la := limitClause(limit, offset)
	q := s.rebind(`SELECT ` + usersColumns + ` FROM users
		JOIN stars ON stars.user_pk = users.pk
		WHERE stars.repo_pk = ? AND users.deleted_at IS NULL
		ORDER BY stars.pk DESC` + lc)
	return s.scanUserList(ctx, q, append([]any{repoPK}, la...)...)
}

// StarredByUser lists the repositories userPK starred, most recent star first,
// paged by limit and offset. The caller filters by visibility.
func (s *Store) StarredByUser(ctx context.Context, userPK int64, limit, offset int) ([]*RepoRow, error) {
	lc, la := limitClause(limit, offset)
	q := s.rebind(`SELECT ` + repoColumns + ` FROM repositories r
		JOIN users u ON u.pk = r.owner_pk
		JOIN stars ON stars.repo_pk = r.pk
		WHERE stars.user_pk = ? AND r.deleted_at IS NULL AND u.deleted_at IS NULL
		ORDER BY stars.pk DESC` + lc)
	return s.scanRepoList(ctx, q, append([]any{userPK}, la...)...)
}

// --- subscriptions (watching) ---

// UpsertSubscription sets userPK's subscription on repoPK to the given flags,
// creating the row when absent and overwriting it when present, the way a PUT
// /repos/{o}/{r}/subscription replaces the whole subscription.
func (s *Store) UpsertSubscription(ctx context.Context, userPK, repoPK int64, subscribed, ignored bool) error {
	q := s.rebind(`INSERT INTO repo_subscriptions (user_pk, repo_pk, subscribed, ignored)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (user_pk, repo_pk) DO UPDATE SET
			subscribed = excluded.subscribed,
			ignored = excluded.ignored`)
	_, err := s.db.ExecContext(ctx, q, userPK, repoPK, subscribed, ignored)
	return err
}

// DeleteSubscription removes userPK's subscription on repoPK. Removing one that
// is absent is not an error.
func (s *Store) DeleteSubscription(ctx context.Context, userPK, repoPK int64) error {
	q := s.rebind(`DELETE FROM repo_subscriptions WHERE user_pk = ? AND repo_pk = ?`)
	_, err := s.db.ExecContext(ctx, q, userPK, repoPK)
	return err
}

// SubscriptionByRepo loads userPK's subscription on repoPK, or ErrNotFound when
// the user has set none.
func (s *Store) SubscriptionByRepo(ctx context.Context, userPK, repoPK int64) (*SubscriptionRow, error) {
	q := s.rebind(`SELECT pk, user_pk, repo_pk, subscribed, ignored, created_at
		FROM repo_subscriptions WHERE user_pk = ? AND repo_pk = ?`)
	var r SubscriptionRow
	var subscribed, ignored boolVal
	var created nullTime
	err := s.rdb.QueryRowContext(ctx, q, userPK, repoPK).
		Scan(&r.PK, &r.UserPK, &r.RepoPK, &subscribed, &ignored, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.Subscribed, r.Ignored, r.CreatedAt = subscribed.Bool, ignored.Bool, created.Time
	return &r, nil
}

// WatcherCount reports how many users are subscribed to repoPK (watching, not
// ignoring), the watchers_count GitHub renders.
func (s *Store) WatcherCount(ctx context.Context, repoPK int64) (int, error) {
	q := s.rebind(`SELECT COUNT(*) FROM repo_subscriptions
		WHERE repo_pk = ? AND subscribed = ? AND ignored = ?`)
	var n int
	err := s.rdb.QueryRowContext(ctx, q, repoPK, true, false).Scan(&n)
	return n, err
}

// WatchersByRepo lists the users watching repoPK (subscribed, not ignoring),
// paged by limit and offset.
func (s *Store) WatchersByRepo(ctx context.Context, repoPK int64, limit, offset int) ([]*UserRow, error) {
	lc, la := limitClause(limit, offset)
	q := s.rebind(`SELECT ` + usersColumns + ` FROM users
		JOIN repo_subscriptions sub ON sub.user_pk = users.pk
		WHERE sub.repo_pk = ? AND sub.subscribed = ? AND sub.ignored = ?
		  AND users.deleted_at IS NULL
		ORDER BY sub.pk DESC` + lc)
	return s.scanUserList(ctx, q, append([]any{repoPK, true, false}, la...)...)
}

// SubscriptionsByUser lists the repositories userPK is watching, paged by limit
// and offset. The caller filters by visibility.
func (s *Store) SubscriptionsByUser(ctx context.Context, userPK int64, limit, offset int) ([]*RepoRow, error) {
	lc, la := limitClause(limit, offset)
	q := s.rebind(`SELECT ` + repoColumns + ` FROM repositories r
		JOIN users u ON u.pk = r.owner_pk
		JOIN repo_subscriptions sub ON sub.repo_pk = r.pk
		WHERE sub.user_pk = ? AND sub.subscribed = ? AND sub.ignored = ?
		  AND r.deleted_at IS NULL AND u.deleted_at IS NULL
		ORDER BY sub.pk DESC` + lc)
	return s.scanRepoList(ctx, q, append([]any{userPK, true, false}, la...)...)
}

// --- follows ---

// InsertFollow records that followerPK follows targetPK. A follow already
// present is left as-is, so a repeated PUT is idempotent.
func (s *Store) InsertFollow(ctx context.Context, followerPK, targetPK int64) error {
	q := s.rebind(`INSERT INTO follows (follower_pk, target_pk) VALUES (?, ?)
		ON CONFLICT (follower_pk, target_pk) DO NOTHING`)
	_, err := s.db.ExecContext(ctx, q, followerPK, targetPK)
	return err
}

// DeleteFollow removes followerPK's follow of targetPK. Removing one that is
// absent is not an error.
func (s *Store) DeleteFollow(ctx context.Context, followerPK, targetPK int64) error {
	q := s.rebind(`DELETE FROM follows WHERE follower_pk = ? AND target_pk = ?`)
	_, err := s.db.ExecContext(ctx, q, followerPK, targetPK)
	return err
}

// IsFollowing reports whether followerPK follows targetPK.
func (s *Store) IsFollowing(ctx context.Context, followerPK, targetPK int64) (bool, error) {
	q := s.rebind(`SELECT 1 FROM follows WHERE follower_pk = ? AND target_pk = ?`)
	var one int
	err := s.rdb.QueryRowContext(ctx, q, followerPK, targetPK).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// FollowerCount reports how many users follow targetPK.
func (s *Store) FollowerCount(ctx context.Context, targetPK int64) (int, error) {
	q := s.rebind(`SELECT COUNT(*) FROM follows WHERE target_pk = ?`)
	var n int
	err := s.rdb.QueryRowContext(ctx, q, targetPK).Scan(&n)
	return n, err
}

// FollowingCount reports how many users followerPK follows.
func (s *Store) FollowingCount(ctx context.Context, followerPK int64) (int, error) {
	q := s.rebind(`SELECT COUNT(*) FROM follows WHERE follower_pk = ?`)
	var n int
	err := s.rdb.QueryRowContext(ctx, q, followerPK).Scan(&n)
	return n, err
}

// FollowersByUser lists the users following targetPK, most recent follow first,
// paged by limit and offset.
func (s *Store) FollowersByUser(ctx context.Context, targetPK int64, limit, offset int) ([]*UserRow, error) {
	lc, la := limitClause(limit, offset)
	q := s.rebind(`SELECT ` + usersColumns + ` FROM users
		JOIN follows ON follows.follower_pk = users.pk
		WHERE follows.target_pk = ? AND users.deleted_at IS NULL
		ORDER BY follows.pk DESC` + lc)
	return s.scanUserList(ctx, q, append([]any{targetPK}, la...)...)
}

// FollowingByUser lists the users followerPK follows, most recent follow first,
// paged by limit and offset.
func (s *Store) FollowingByUser(ctx context.Context, followerPK int64, limit, offset int) ([]*UserRow, error) {
	lc, la := limitClause(limit, offset)
	q := s.rebind(`SELECT ` + usersColumns + ` FROM users
		JOIN follows ON follows.target_pk = users.pk
		WHERE follows.follower_pk = ? AND users.deleted_at IS NULL
		ORDER BY follows.pk DESC` + lc)
	return s.scanUserList(ctx, q, append([]any{followerPK}, la...)...)
}

// --- shared scanners ---

// scanUserList runs a user-bearing query and scans the rows into UserRows.
func (s *Store) scanUserList(ctx context.Context, query string, args ...any) ([]*UserRow, error) {
	rows, err := s.rdb.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*UserRow
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// scanRepoList runs a repo-bearing query and scans the rows into RepoRows.
func (s *Store) scanRepoList(ctx context.Context, query string, args ...any) ([]*RepoRow, error) {
	rows, err := s.rdb.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*RepoRow
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

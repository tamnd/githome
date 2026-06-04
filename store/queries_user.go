package store

import (
	"context"
	"database/sql"
	"errors"
)

// userColumns is the shared SELECT list so UserByPK and UserByLogin scan an
// identical row shape.
const userColumns = `pk, db_id, login, type, name, email, site_admin,
	company, blog, location, bio, hireable, twitter_username,
	public_repos, public_gists, followers, following, created_at, updated_at`

// UserByPK loads a user by primary key. It returns ErrNotFound when no live row
// matches.
func (s *Store) UserByPK(ctx context.Context, pk int64) (*UserRow, error) {
	q := s.rebind(`SELECT ` + userColumns + ` FROM users WHERE pk = ? AND deleted_at IS NULL`)
	return scanUser(s.db.QueryRowContext(ctx, q, pk))
}

// UserByLogin loads a user by login, case-insensitively, matching GitHub's
// case-preserving but case-insensitive account names.
func (s *Store) UserByLogin(ctx context.Context, login string) (*UserRow, error) {
	q := s.rebind(`SELECT ` + userColumns + ` FROM users
		WHERE lower(login) = lower(?) AND deleted_at IS NULL`)
	return scanUser(s.db.QueryRowContext(ctx, q, login))
}

// InsertUser allocates the shared db_id, writes the row, and fills the
// server-assigned fields (PK, DBID, CreatedAt, UpdatedAt) back onto u.
func (s *Store) InsertUser(ctx context.Context, u *UserRow) error {
	dbID, err := s.AllocDBID(ctx)
	if err != nil {
		return err
	}
	q := s.rebind(`INSERT INTO users
		(db_id, login, type, name, email, site_admin,
		 company, blog, location, bio, hireable, twitter_username,
		 public_repos, public_gists, followers, following)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id, created_at, updated_at`)
	if u.Type == "" {
		u.Type = "User"
	}
	var created, updated nullTime
	err = s.db.QueryRowContext(ctx, q,
		dbID, u.Login, u.Type, argStr(u.Name), argStr(u.Email), u.SiteAdmin,
		argStr(u.Company), u.Blog, argStr(u.Location), argStr(u.Bio),
		argBool(u.Hireable), argStr(u.TwitterUsername),
		u.PublicRepos, u.PublicGists, u.Followers, u.Following,
	).Scan(&u.PK, &u.DBID, &created, &updated)
	if err != nil {
		return err
	}
	u.CreatedAt, u.UpdatedAt = created.Time, updated.Time
	return nil
}

// scanUser maps one users row into a UserRow, absorbing the dialect differences
// for nullable text, the boolean flags, and timestamps.
func scanUser(row interface{ Scan(...any) error }) (*UserRow, error) {
	var (
		u                                            UserRow
		name, email, company, location, bio, twitter sql.NullString
		siteAdmin, hireable                          boolVal
		created, updated                             nullTime
	)
	err := row.Scan(
		&u.PK, &u.DBID, &u.Login, &u.Type, &name, &email, &siteAdmin,
		&company, &u.Blog, &location, &bio, &hireable, &twitter,
		&u.PublicRepos, &u.PublicGists, &u.Followers, &u.Following, &created, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Name, u.Email = strPtr(name), strPtr(email)
	u.Company, u.Location, u.Bio = strPtr(company), strPtr(location), strPtr(bio)
	u.TwitterUsername = strPtr(twitter)
	u.SiteAdmin = siteAdmin.Bool
	u.Hireable = hireable.ptr()
	u.CreatedAt, u.UpdatedAt = created.Time, updated.Time
	return &u, nil
}

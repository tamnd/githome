package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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
	return scanUser(s.rdb.QueryRowContext(ctx, q, pk))
}

// UserByLogin loads a user by login, case-insensitively, matching GitHub's
// case-preserving but case-insensitive account names.
func (s *Store) UserByLogin(ctx context.Context, login string) (*UserRow, error) {
	q := s.rebind(`SELECT ` + userColumns + ` FROM users
		WHERE lower(login) = lower(?) AND deleted_at IS NULL`)
	return scanUser(s.rdb.QueryRowContext(ctx, q, login))
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

// UserLoginExists reports whether a user with the given login exists and is
// not soft-deleted. Used by the join form to check for duplicate usernames
// before attempting to insert.
func (s *Store) UserLoginExists(ctx context.Context, login string) (bool, error) {
	var n int
	q := s.rebind(`SELECT COUNT(*) FROM users WHERE lower(login) = lower(?) AND deleted_at IS NULL`)
	if err := s.rdb.QueryRowContext(ctx, q, login).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// PasswordHashFor returns the stored bcrypt hash for the given login, or ("", ErrNotFound)
// when the user does not exist. The caller compares it with bcrypt.CompareHashAndPassword.
func (s *Store) PasswordHashFor(ctx context.Context, login string) (pk int64, hash string, err error) {
	q := s.rebind(`SELECT pk, COALESCE(password_hash,'') FROM users
		WHERE lower(login) = lower(?) AND deleted_at IS NULL`)
	err = s.rdb.QueryRowContext(ctx, q, login).Scan(&pk, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", ErrNotFound
	}
	return pk, hash, err
}

// SetPasswordHash writes a new bcrypt hash for the given user pk. It is called
// on account creation and password change; it never reads the old hash.
func (s *Store) SetPasswordHash(ctx context.Context, userPK int64, hash string) error {
	q := s.rebind(`UPDATE users SET password_hash = ?, updated_at = CURRENT_TIMESTAMP WHERE pk = ?`)
	_, err := s.db.ExecContext(ctx, q, hash, userPK)
	return err
}

// InsertUserWithPassword creates a new user account with the given login, email,
// and bcrypt password hash in one transaction. It returns the new user's PK.
// Used by the web sign-up flow; fe/web/auth calls it through the PasswordStore
// interface so it never imports store directly.
func (s *Store) InsertUserWithPassword(ctx context.Context, login, email, hash string) (int64, error) {
	var pk int64
	err := s.WithTx(ctx, func(tx *Tx) error {
		dbID, err := tx.allocDBID(ctx)
		if err != nil {
			return err
		}
		q := tx.rebind(`INSERT INTO users
			(db_id, login, type, email, password_hash)
			VALUES (?, ?, 'User', ?, ?)
			RETURNING pk`)
		return tx.tx.QueryRowContext(ctx, q, dbID, login, email, hash).Scan(&pk)
	})
	return pk, err
}

// ProfileUpdate carries the mutable profile fields the settings page can write.
// A nil pointer means "leave unchanged"; a non-nil pointer replaces the value.
type ProfileUpdate struct {
	Name            *string
	Email           *string
	Bio             *string
	Location        *string
	Company         *string
	Blog            *string
	TwitterUsername *string
	Hireable        *bool
}

// UpdateProfile writes the non-nil fields from u onto the users row at userPK.
// Every non-nil field is included in the SET clause; nil fields are not touched,
// so the caller can update a single field without loading the whole row first.
func (s *Store) UpdateProfile(ctx context.Context, userPK int64, u ProfileUpdate) error {
	setClauses := []string{"updated_at = CURRENT_TIMESTAMP"}
	args := []any{}
	if u.Name != nil {
		setClauses = append(setClauses, "name = ?")
		args = append(args, *u.Name)
	}
	if u.Email != nil {
		setClauses = append(setClauses, "email = ?")
		args = append(args, *u.Email)
	}
	if u.Bio != nil {
		setClauses = append(setClauses, "bio = ?")
		args = append(args, *u.Bio)
	}
	if u.Location != nil {
		setClauses = append(setClauses, "location = ?")
		args = append(args, *u.Location)
	}
	if u.Company != nil {
		setClauses = append(setClauses, "company = ?")
		args = append(args, *u.Company)
	}
	if u.Blog != nil {
		setClauses = append(setClauses, "blog = ?")
		args = append(args, *u.Blog)
	}
	if u.TwitterUsername != nil {
		setClauses = append(setClauses, "twitter_username = ?")
		args = append(args, *u.TwitterUsername)
	}
	if u.Hireable != nil {
		setClauses = append(setClauses, "hireable = ?")
		args = append(args, *u.Hireable)
	}
	args = append(args, userPK)
	q := "UPDATE users SET " + joinClauses(setClauses) + " WHERE pk = ?"
	_, err := s.db.ExecContext(ctx, s.rebind(q), args...)
	return err
}

func joinClauses(clauses []string) string {
	var b strings.Builder
	for i, c := range clauses {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(c)
	}
	return b.String()
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

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// InsertAuthCode stores a new OAuth authorization-code row. The caller hashes
// the raw code before passing it in.
func (s *Store) InsertAuthCode(ctx context.Context, a *AuthCodeRow) error {
	q := s.rebind(`INSERT INTO oauth_auth_codes
		(code_hash, oauth_app_pk, user_pk, redirect_uri, scopes, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)
		RETURNING pk, created_at`)
	var created nullTime
	err := s.db.QueryRowContext(ctx, q,
		a.CodeHash, a.OAuthAppPK, a.UserPK, a.RedirectURI, a.Scopes, a.ExpiresAt.UTC(),
	).Scan(&a.PK, &created)
	if err != nil {
		return err
	}
	a.CreatedAt = created.Time
	return nil
}

// ConsumeAuthCode loads the auth code row by its hash and atomically marks it
// used. Returns ErrNotFound when no live, unused code matches the hash. The
// caller must verify redirect_uri and that ExpiresAt is still in the future.
func (s *Store) ConsumeAuthCode(ctx context.Context, codeHash []byte) (*AuthCodeRow, error) {
	q := s.rebind(`SELECT pk, code_hash, oauth_app_pk, user_pk, redirect_uri, scopes, used, expires_at, created_at
		FROM oauth_auth_codes WHERE code_hash = ?`)
	var (
		a       AuthCodeRow
		used    boolVal
		exp, cr nullTime
	)
	err := s.db.QueryRowContext(ctx, q, codeHash).Scan(
		&a.PK, &a.CodeHash, &a.OAuthAppPK, &a.UserPK, &a.RedirectURI, &a.Scopes, &used, &exp, &cr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.Used = used.Bool
	a.ExpiresAt = exp.Time
	a.CreatedAt = cr.Time

	if a.Used || time.Now().After(a.ExpiresAt) {
		return nil, ErrNotFound
	}
	upd := s.rebind(`UPDATE oauth_auth_codes SET used = ? WHERE pk = ?`)
	if _, err := s.db.ExecContext(ctx, upd, true, a.PK); err != nil {
		return nil, err
	}
	return &a, nil
}

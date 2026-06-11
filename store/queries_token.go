package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const tokenColumns = `pk, user_pk, oauth_app_pk, installation_pk, github_app_pk,
	grant_json, token_hash, token_prefix,
	last_eight, kind, scopes, note, expires_at, revoked_at, last_used_at, created_at`

// TokenByHash loads the token whose stored sha256 equals hash. The caller has
// already hashed the presented secret; the unique index on token_hash makes this
// a point lookup. Returns ErrNotFound when no row matches.
func (s *Store) TokenByHash(ctx context.Context, hash []byte) (*TokenRow, error) {
	q := s.rebind(`SELECT ` + tokenColumns + ` FROM tokens WHERE token_hash = ?`)
	return scanToken(s.rdb.QueryRowContext(ctx, q, hash))
}

// InsertToken writes a new credential and fills PK and CreatedAt back onto t.
func (s *Store) InsertToken(ctx context.Context, t *TokenRow) error {
	q := s.rebind(`INSERT INTO tokens
		(user_pk, oauth_app_pk, installation_pk, github_app_pk, grant_json,
		 token_hash, token_prefix, last_eight, kind, scopes, note, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, created_at`)
	var created nullTime
	err := s.db.QueryRowContext(ctx, q,
		i64Arg(t.UserPK), i64Arg(t.OAuthAppPK), i64Arg(t.InstallationPK), i64Arg(t.GitHubAppPK),
		t.GrantJSON, t.TokenHash, t.TokenPrefix,
		t.LastEight, t.Kind, t.Scopes, t.Note, argTime(t.ExpiresAt),
	).Scan(&t.PK, &created)
	if err != nil {
		return err
	}
	t.CreatedAt = created.Time
	return nil
}

// TokensForUser lists a user's live personal access tokens, newest first, for
// the settings tokens page. Revoked rows stay out of the list; the hash column
// rides along but the caller never shows it.
func (s *Store) TokensForUser(ctx context.Context, userPK int64) ([]*TokenRow, error) {
	q := s.rebind(`SELECT ` + tokenColumns + ` FROM tokens
		WHERE user_pk = ? AND kind = 'pat' AND revoked_at IS NULL
		ORDER BY created_at DESC, pk DESC`)
	rows, err := s.rdb.QueryContext(ctx, q, userPK)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*TokenRow
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteUserToken removes one of a user's personal access tokens. The user_pk
// guard means a user can only ever delete their own token; deleting someone
// else's pk is the same ErrNotFound as deleting a pk that never existed.
func (s *Store) DeleteUserToken(ctx context.Context, pk, userPK int64) error {
	q := s.rebind(`DELETE FROM tokens WHERE pk = ? AND user_pk = ? AND kind = 'pat'`)
	res, err := s.db.ExecContext(ctx, q, pk, userPK)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// BumpTokenLastUsed records the last-used timestamp for a batch of tokens in one
// transaction. The async debouncer in auth coalesces touches and calls this at
// most once every couple of seconds, so the per-row UPDATE loop is cheap.
func (s *Store) BumpTokenLastUsed(ctx context.Context, at map[int64]time.Time) error {
	if len(at) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.rebind(`UPDATE tokens SET last_used_at = ? WHERE pk = ?`)
	for pk, ts := range at {
		if _, err := tx.ExecContext(ctx, q, ts.UTC(), pk); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// scanToken maps one tokens row into a TokenRow.
func scanToken(row interface{ Scan(...any) error }) (*TokenRow, error) {
	var (
		t                              TokenRow
		userPK, appPK, instPK, ghAppPK sql.NullInt64
		grantJSON                      sql.NullString
		expires, revoked, lastUsed     nullTime
		created                        nullTime
	)
	err := row.Scan(
		&t.PK, &userPK, &appPK, &instPK, &ghAppPK,
		&grantJSON, &t.TokenHash, &t.TokenPrefix,
		&t.LastEight, &t.Kind, &t.Scopes, &t.Note, &expires, &revoked, &lastUsed, &created,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.UserPK, t.OAuthAppPK = i64Ptr(userPK), i64Ptr(appPK)
	t.InstallationPK, t.GitHubAppPK = i64Ptr(instPK), i64Ptr(ghAppPK)
	if grantJSON.Valid {
		t.GrantJSON = &grantJSON.String
	}
	t.ExpiresAt, t.RevokedAt, t.LastUsedAt = expires.ptr(), revoked.ptr(), lastUsed.ptr()
	t.CreatedAt = created.Time
	return &t, nil
}

// i64Arg binds a nullable int64 foreign key: a nil pointer becomes SQL NULL.
func i64Arg(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

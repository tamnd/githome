package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// InsertOAuthApp registers an OAuth/GitHub-App client. The installer seeds the
// first-party gh client this way; tests use it to set up a device-flow app.
func (s *Store) InsertOAuthApp(ctx context.Context, a *OAuthAppRow) error {
	q := s.rebind(`INSERT INTO oauth_apps
		(client_id, client_secret_hash, name, owner_pk, device_flow_enabled, callback_url)
		VALUES (?, ?, ?, ?, ?, ?)
		RETURNING pk, created_at`)
	var created nullTime
	err := s.db.QueryRowContext(ctx, q,
		a.ClientID, a.ClientSecretHash, a.Name, i64Arg(a.OwnerPK), a.DeviceFlowEnabled, a.CallbackURL,
	).Scan(&a.PK, &created)
	if err != nil {
		return err
	}
	a.CreatedAt = created.Time
	return nil
}

// OAuthAppByClientID loads an OAuth app by its public client_id.
func (s *Store) OAuthAppByClientID(ctx context.Context, clientID string) (*OAuthAppRow, error) {
	q := s.rebind(`SELECT pk, client_id, client_secret_hash, name, owner_pk, device_flow_enabled, callback_url, created_at
		FROM oauth_apps WHERE client_id = ?`)
	var (
		a       OAuthAppRow
		secret  []byte
		ownerPK sql.NullInt64
		dfe     boolVal
		created nullTime
	)
	err := s.rdb.QueryRowContext(ctx, q, clientID).Scan(
		&a.PK, &a.ClientID, &secret, &a.Name, &ownerPK, &dfe, &a.CallbackURL, &created,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.ClientSecretHash = secret
	a.OwnerPK = i64Ptr(ownerPK)
	a.DeviceFlowEnabled = dfe.Bool
	a.CreatedAt = created.Time
	return &a, nil
}

// InsertDeviceCode opens a new device-flow row in the pending state.
func (s *Store) InsertDeviceCode(ctx context.Context, d *DeviceCodeRow) error {
	if d.State == "" {
		d.State = "pending"
	}
	if d.IntervalSec == 0 {
		d.IntervalSec = 5
	}
	q := s.rebind(`INSERT INTO oauth_device_codes
		(device_code_hash, user_code, oauth_app_pk, scopes, state, interval_sec, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, created_at`)
	var created nullTime
	err := s.db.QueryRowContext(ctx, q,
		d.DeviceCodeHash, d.UserCode, i64Arg(d.OAuthAppPK), d.Scopes, d.State, d.IntervalSec, d.ExpiresAt.UTC(),
	).Scan(&d.PK, &created)
	if err != nil {
		return err
	}
	d.CreatedAt = created.Time
	return nil
}

// DeviceCodeByHash loads a device-flow row by sha256(device_code), the lookup
// the polling endpoint uses.
func (s *Store) DeviceCodeByHash(ctx context.Context, hash []byte) (*DeviceCodeRow, error) {
	q := s.rebind(deviceCodeSelect + ` WHERE device_code_hash = ?`)
	return scanDeviceCode(s.rdb.QueryRowContext(ctx, q, hash))
}

// DeviceCodeByUserCode loads a device-flow row by the human-typed user_code.
func (s *Store) DeviceCodeByUserCode(ctx context.Context, userCode string) (*DeviceCodeRow, error) {
	q := s.rebind(deviceCodeSelect + ` WHERE user_code = ?`)
	return scanDeviceCode(s.rdb.QueryRowContext(ctx, q, userCode))
}

// SetDeviceState moves a device-flow row to approved or denied. userPK is the
// approving user; pass 0 on denial to leave user_pk NULL.
func (s *Store) SetDeviceState(ctx context.Context, pk int64, state string, userPK int64) error {
	q := s.rebind(`UPDATE oauth_device_codes SET state = ?, user_pk = ? WHERE pk = ?`)
	var user any
	if userPK != 0 {
		user = userPK
	}
	_, err := s.db.ExecContext(ctx, q, state, user, pk)
	return err
}

// SetDeviceInterval bumps the stored poll interval used to enforce slow_down.
func (s *Store) SetDeviceInterval(ctx context.Context, pk int64, interval int) error {
	q := s.rebind(`UPDATE oauth_device_codes SET interval_sec = ? WHERE pk = ?`)
	_, err := s.db.ExecContext(ctx, q, interval, pk)
	return err
}

// SetDevicePolled records the most recent poll time for slow_down enforcement.
func (s *Store) SetDevicePolled(ctx context.Context, pk int64, at time.Time) error {
	q := s.rebind(`UPDATE oauth_device_codes SET last_polled_at = ? WHERE pk = ?`)
	_, err := s.db.ExecContext(ctx, q, at.UTC(), pk)
	return err
}

// DeleteDeviceCode removes a device-flow row once it is spent (token issued,
// denied, or expired).
func (s *Store) DeleteDeviceCode(ctx context.Context, pk int64) error {
	q := s.rebind(`DELETE FROM oauth_device_codes WHERE pk = ?`)
	_, err := s.db.ExecContext(ctx, q, pk)
	return err
}

const deviceCodeSelect = `SELECT pk, device_code_hash, user_code, oauth_app_pk, scopes,
	state, user_pk, interval_sec, last_polled_at, expires_at, created_at FROM oauth_device_codes`

func scanDeviceCode(row interface{ Scan(...any) error }) (*DeviceCodeRow, error) {
	var (
		d                DeviceCodeRow
		appPK, userPK    sql.NullInt64
		lastPolled       nullTime
		expires, created nullTime
	)
	err := row.Scan(
		&d.PK, &d.DeviceCodeHash, &d.UserCode, &appPK, &d.Scopes,
		&d.State, &userPK, &d.IntervalSec, &lastPolled, &expires, &created,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.OAuthAppPK, d.UserPK = i64Ptr(appPK), i64Ptr(userPK)
	d.LastPolledAt = lastPolled.ptr()
	d.ExpiresAt, d.CreatedAt = expires.Time, created.Time
	return &d, nil
}

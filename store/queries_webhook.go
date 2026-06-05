package store

import (
	"context"
	"database/sql"
	"errors"
)

// The webhook store. A webhook is a repository's registration of a delivery URL
// plus the events it subscribes to and an optional signing secret. A delivery is
// the recorded result of one POST. Webhooks resolve by pk, by db_id (a node id
// decodes to it), and as a repository's list; deliveries resolve by pk and as a
// webhook's recent history. The secret is stored in the clear because HMAC
// signing needs the original bytes; the presenter redacts it on the wire.

const webhookColumns = `pk, db_id, repo_pk, name, url, content_type, secret,
	insecure_ssl, active, events, last_response, created_at, updated_at`

// InsertWebhook writes a webhook row with a freshly allocated db_id, filling the
// server-assigned fields back onto w.
func (s *Store) InsertWebhook(ctx context.Context, w *WebhookRow) error {
	return s.WithTx(ctx, func(t *Tx) error {
		dbID, err := t.allocDBID(ctx)
		if err != nil {
			return err
		}
		q := t.rebind(`INSERT INTO webhooks
			(db_id, repo_pk, name, url, content_type, secret, insecure_ssl, active, events)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			RETURNING pk, db_id, created_at, updated_at`)
		var created, upd nullTime
		err = t.tx.QueryRowContext(ctx, q,
			dbID, w.RepoPK, w.Name, w.URL, w.ContentType, argStr(w.Secret),
			argBool(&w.InsecureSSL), argBool(&w.Active), w.Events,
		).Scan(&w.PK, &w.DBID, &created, &upd)
		if err != nil {
			return err
		}
		w.CreatedAt, w.UpdatedAt = created.Time, upd.Time
		return nil
	})
}

// GetWebhookByPK resolves a webhook by primary key.
func (s *Store) GetWebhookByPK(ctx context.Context, pk int64) (*WebhookRow, error) {
	q := s.rebind(`SELECT ` + webhookColumns + ` FROM webhooks WHERE pk = ?`)
	return scanWebhook(s.db.QueryRowContext(ctx, q, pk))
}

// GetWebhookForRepo resolves a webhook by its public id scoped to its
// repository, the lookup the REST CRUD endpoints use so a hook id from one
// repository never addresses another's.
func (s *Store) GetWebhookForRepo(ctx context.Context, repoPK, dbID int64) (*WebhookRow, error) {
	q := s.rebind(`SELECT ` + webhookColumns + ` FROM webhooks WHERE repo_pk = ? AND db_id = ?`)
	return scanWebhook(s.db.QueryRowContext(ctx, q, repoPK, dbID))
}

// ListWebhooks returns a repository's webhooks, oldest first.
func (s *Store) ListWebhooks(ctx context.Context, repoPK int64) ([]WebhookRow, error) {
	q := s.rebind(`SELECT ` + webhookColumns + ` FROM webhooks WHERE repo_pk = ? ORDER BY pk`)
	return s.queryWebhooks(ctx, q, repoPK)
}

// ListActiveWebhooks returns a repository's active webhooks, the candidate set
// the fan-out worker filters by subscribed event.
func (s *Store) ListActiveWebhooks(ctx context.Context, repoPK int64) ([]WebhookRow, error) {
	q := s.rebind(`SELECT ` + webhookColumns + ` FROM webhooks
		WHERE repo_pk = ? AND active = ? ORDER BY pk`)
	return s.queryWebhooks(ctx, q, repoPK, true)
}

func (s *Store) queryWebhooks(ctx context.Context, q string, args ...any) ([]WebhookRow, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []WebhookRow
	for rows.Next() {
		w, err := scanWebhookRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

// UpdateWebhook writes the editable fields of an existing webhook and stamps
// updated_at. The service loads the row, applies the patch, and calls this.
func (s *Store) UpdateWebhook(ctx context.Context, w *WebhookRow) error {
	q := s.rebind(`UPDATE webhooks SET
		name = ?, url = ?, content_type = ?, secret = ?, insecure_ssl = ?,
		active = ?, events = ?, updated_at = ?
		WHERE pk = ?`)
	res, err := s.db.ExecContext(ctx, q,
		w.Name, w.URL, w.ContentType, argStr(w.Secret),
		argBool(&w.InsecureSSL), argBool(&w.Active), w.Events, nowUTC(), w.PK)
	if err != nil {
		return err
	}
	return affectedOrNotFound(res)
}

// SetWebhookLastResponse records the JSON summary of the most recent delivery on
// the hook, the last_response the API surfaces.
func (s *Store) SetWebhookLastResponse(ctx context.Context, pk int64, summary string) error {
	q := s.rebind(`UPDATE webhooks SET last_response = ?, updated_at = ? WHERE pk = ?`)
	res, err := s.db.ExecContext(ctx, q, summary, nowUTC(), pk)
	if err != nil {
		return err
	}
	return affectedOrNotFound(res)
}

// DeleteWebhook removes a webhook by its public id and, by cascade, its
// deliveries.
func (s *Store) DeleteWebhook(ctx context.Context, repoPK, dbID int64) error {
	q := s.rebind(`DELETE FROM webhooks WHERE repo_pk = ? AND db_id = ?`)
	res, err := s.db.ExecContext(ctx, q, repoPK, dbID)
	if err != nil {
		return err
	}
	return affectedOrNotFound(res)
}

func scanWebhook(row interface{ Scan(...any) error }) (*WebhookRow, error) {
	w, err := scanWebhookRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return w, err
}

func scanWebhookRows(row interface{ Scan(...any) error }) (*WebhookRow, error) {
	var (
		w            WebhookRow
		secret, last sql.NullString
		insecure     boolVal
		active       boolVal
		created, upd nullTime
	)
	if err := row.Scan(&w.PK, &w.DBID, &w.RepoPK, &w.Name, &w.URL, &w.ContentType,
		&secret, &insecure, &active, &w.Events, &last, &created, &upd); err != nil {
		return nil, err
	}
	w.Secret = strPtr(secret)
	w.InsecureSSL = insecure.Bool
	w.Active = active.Bool
	w.LastResponse = strPtr(last)
	w.CreatedAt, w.UpdatedAt = created.Time, upd.Time
	return &w, nil
}

const deliveryColumns = `pk, db_id, webhook_pk, guid, event, action, status_code,
	request_url, request_headers, request_body, response_headers, response_body,
	duration_ms, redelivery, success, created_at`

// InsertDelivery records the result of one POST with a freshly allocated db_id,
// filling the server-assigned fields back onto d.
func (s *Store) InsertDelivery(ctx context.Context, d *WebhookDeliveryRow) error {
	return s.WithTx(ctx, func(t *Tx) error {
		dbID, err := t.allocDBID(ctx)
		if err != nil {
			return err
		}
		q := t.rebind(`INSERT INTO webhook_deliveries
			(db_id, webhook_pk, guid, event, action, status_code, request_url,
			 request_headers, request_body, response_headers, response_body,
			 duration_ms, redelivery, success)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			RETURNING pk, db_id, created_at`)
		var created nullTime
		err = t.tx.QueryRowContext(ctx, q,
			dbID, d.WebhookPK, d.GUID, d.Event, d.Action, argI64(d.StatusCode),
			d.RequestURL, d.RequestHeaders, d.RequestBody, d.ResponseHeaders,
			d.ResponseBody, d.DurationMS, argBool(&d.Redelivery), argBool(&d.Success),
		).Scan(&d.PK, &d.DBID, &created)
		if err != nil {
			return err
		}
		d.CreatedAt = created.Time
		return nil
	})
}

// GetDeliveryForWebhook resolves one delivery by its public id scoped to its
// webhook, the lookup the inspect and redeliver endpoints use.
func (s *Store) GetDeliveryForWebhook(ctx context.Context, webhookPK, dbID int64) (*WebhookDeliveryRow, error) {
	q := s.rebind(`SELECT ` + deliveryColumns + ` FROM webhook_deliveries
		WHERE webhook_pk = ? AND db_id = ?`)
	return scanDelivery(s.db.QueryRowContext(ctx, q, webhookPK, dbID))
}

// GetDeliveryByPK resolves one delivery by primary key, the value a redeliver
// job carries so the worker can replay the recorded request.
func (s *Store) GetDeliveryByPK(ctx context.Context, pk int64) (*WebhookDeliveryRow, error) {
	q := s.rebind(`SELECT ` + deliveryColumns + ` FROM webhook_deliveries WHERE pk = ?`)
	return scanDelivery(s.db.QueryRowContext(ctx, q, pk))
}

// ListDeliveries returns a webhook's recent deliveries, newest first.
func (s *Store) ListDeliveries(ctx context.Context, webhookPK int64, limit int) ([]WebhookDeliveryRow, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	q := s.rebind(`SELECT ` + deliveryColumns + ` FROM webhook_deliveries
		WHERE webhook_pk = ? ORDER BY pk DESC LIMIT ?`)
	rows, err := s.db.QueryContext(ctx, q, webhookPK, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []WebhookDeliveryRow
	for rows.Next() {
		d, err := scanDeliveryRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func scanDelivery(row interface{ Scan(...any) error }) (*WebhookDeliveryRow, error) {
	d, err := scanDeliveryRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func scanDeliveryRows(row interface{ Scan(...any) error }) (*WebhookDeliveryRow, error) {
	var (
		d           WebhookDeliveryRow
		status      sql.NullInt64
		redel, succ boolVal
		created     nullTime
	)
	if err := row.Scan(&d.PK, &d.DBID, &d.WebhookPK, &d.GUID, &d.Event, &d.Action,
		&status, &d.RequestURL, &d.RequestHeaders, &d.RequestBody,
		&d.ResponseHeaders, &d.ResponseBody, &d.DurationMS, &redel, &succ,
		&created); err != nil {
		return nil, err
	}
	d.StatusCode = i64Ptr(status)
	d.Redelivery = redel.Bool
	d.Success = succ.Bool
	d.CreatedAt = created.Time
	return &d, nil
}

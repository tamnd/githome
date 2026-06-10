package store

import (
	"context"
	"database/sql"
	"errors"
)

const githubAppColumns = `pk, db_id, owner_pk, slug, name, client_id,
	private_key_pem, permissions, events, created_at`

// GitHubAppByPK loads a GitHub App by its internal primary key.
func (s *Store) GitHubAppByPK(ctx context.Context, pk int64) (*GitHubAppRow, error) {
	q := s.rebind(`SELECT ` + githubAppColumns + ` FROM github_apps WHERE pk = ?`)
	return scanGitHubApp(s.db.QueryRowContext(ctx, q, pk))
}

// GitHubAppByClientID loads a GitHub App by its OAuth client_id.
func (s *Store) GitHubAppByClientID(ctx context.Context, clientID string) (*GitHubAppRow, error) {
	q := s.rebind(`SELECT ` + githubAppColumns + ` FROM github_apps WHERE client_id = ?`)
	return scanGitHubApp(s.db.QueryRowContext(ctx, q, clientID))
}

const installationColumns = `pk, db_id, app_pk, account_pk, repository_selection,
	permissions, events, suspended_at, created_at`

// InstallationByPK loads an installation by its internal primary key.
func (s *Store) InstallationByPK(ctx context.Context, pk int64) (*InstallationRow, error) {
	q := s.rebind(`SELECT ` + installationColumns + ` FROM installations WHERE pk = ?`)
	return scanInstallation(s.db.QueryRowContext(ctx, q, pk))
}

// InstallationsByAppPK returns all installations for an app, ordered by created_at.
func (s *Store) InstallationsByAppPK(ctx context.Context, appPK int64) ([]*InstallationRow, error) {
	q := s.rebind(`SELECT ` + installationColumns +
		` FROM installations WHERE app_pk = ? ORDER BY created_at`)
	rows, err := s.db.QueryContext(ctx, q, appPK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*InstallationRow
	for rows.Next() {
		r, err := scanInstallation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InstallationRepoPKs returns the repo PKs accessible to an installation.
func (s *Store) InstallationRepoPKs(ctx context.Context, instPK int64) ([]int64, error) {
	q := s.rebind(`SELECT repo_pk FROM installation_repositories WHERE installation_pk = ?`)
	rows, err := s.db.QueryContext(ctx, q, instPK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var pk int64
		if err := rows.Scan(&pk); err != nil {
			return nil, err
		}
		out = append(out, pk)
	}
	return out, rows.Err()
}

func scanGitHubApp(row interface{ Scan(...any) error }) (*GitHubAppRow, error) {
	var (
		a       GitHubAppRow
		created nullTime
	)
	err := row.Scan(
		&a.PK, &a.DBID, &a.OwnerPK, &a.Slug, &a.Name, &a.ClientID,
		&a.PrivateKeyPEM, &a.Permissions, &a.Events, &created,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.CreatedAt = created.Time
	return &a, nil
}

func scanInstallation(row interface{ Scan(...any) error }) (*InstallationRow, error) {
	var (
		r          InstallationRow
		suspended  nullTime
		created    nullTime
	)
	err := row.Scan(
		&r.PK, &r.DBID, &r.AppPK, &r.AccountPK, &r.RepositorySelection,
		&r.Permissions, &r.Events, &suspended, &created,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.SuspendedAt = suspended.ptr()
	r.CreatedAt = created.Time
	return &r, nil
}

package store

import (
	"context"
	"database/sql"
	"errors"
)

const githubAppColumns = `pk, db_id, owner_pk, slug, name, client_id,
	private_key_pem, permissions, events, created_at`

// InsertGitHubApp registers a GitHub App. The installer seeds the first-party
// app this way; tests use it to set up the app-auth surface. A zero Permissions
// or Events string defaults to the empty JSON object/array the columns require.
func (s *Store) InsertGitHubApp(ctx context.Context, a *GitHubAppRow) error {
	dbID, err := s.AllocDBID(ctx)
	if err != nil {
		return err
	}
	a.DBID = dbID
	if a.Permissions == "" {
		a.Permissions = "{}"
	}
	if a.Events == "" {
		a.Events = "[]"
	}
	q := s.rebind(`INSERT INTO github_apps
		(db_id, owner_pk, slug, name, client_id, private_key_pem, permissions, events)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, created_at`)
	var created nullTime
	if err := s.db.QueryRowContext(ctx, q,
		a.DBID, a.OwnerPK, a.Slug, a.Name, a.ClientID, a.PrivateKeyPEM, a.Permissions, a.Events,
	).Scan(&a.PK, &created); err != nil {
		return err
	}
	a.CreatedAt = created.Time
	return nil
}

// InsertInstallation records an app installation on an account.
func (s *Store) InsertInstallation(ctx context.Context, in *InstallationRow) error {
	dbID, err := s.AllocDBID(ctx)
	if err != nil {
		return err
	}
	in.DBID = dbID
	if in.RepositorySelection == "" {
		in.RepositorySelection = "all"
	}
	if in.Permissions == "" {
		in.Permissions = "{}"
	}
	if in.Events == "" {
		in.Events = "[]"
	}
	q := s.rebind(`INSERT INTO installations
		(db_id, app_pk, account_pk, repository_selection, permissions, events, suspended_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, created_at`)
	var created nullTime
	if err := s.db.QueryRowContext(ctx, q,
		in.DBID, in.AppPK, in.AccountPK, in.RepositorySelection, in.Permissions, in.Events, argTime(in.SuspendedAt),
	).Scan(&in.PK, &created); err != nil {
		return err
	}
	in.CreatedAt = created.Time
	return nil
}

// InsertInstallationRepo grants a "selected"-scope installation access to a
// repository.
func (s *Store) InsertInstallationRepo(ctx context.Context, instPK, repoPK int64) error {
	q := s.rebind(`INSERT INTO installation_repositories (installation_pk, repo_pk) VALUES (?, ?)`)
	_, err := s.db.ExecContext(ctx, q, instPK, repoPK)
	return err
}

// GitHubAppByPK loads a GitHub App by its internal primary key.
func (s *Store) GitHubAppByPK(ctx context.Context, pk int64) (*GitHubAppRow, error) {
	q := s.rebind(`SELECT ` + githubAppColumns + ` FROM github_apps WHERE pk = ?`)
	return scanGitHubApp(s.rdb.QueryRowContext(ctx, q, pk))
}

// GitHubAppByClientID loads a GitHub App by its OAuth client_id.
func (s *Store) GitHubAppByClientID(ctx context.Context, clientID string) (*GitHubAppRow, error) {
	q := s.rebind(`SELECT ` + githubAppColumns + ` FROM github_apps WHERE client_id = ?`)
	return scanGitHubApp(s.rdb.QueryRowContext(ctx, q, clientID))
}

const installationColumns = `pk, db_id, app_pk, account_pk, repository_selection,
	permissions, events, suspended_at, created_at`

// InstallationByPK loads an installation by its internal primary key.
func (s *Store) InstallationByPK(ctx context.Context, pk int64) (*InstallationRow, error) {
	q := s.rebind(`SELECT ` + installationColumns + ` FROM installations WHERE pk = ?`)
	return scanInstallation(s.rdb.QueryRowContext(ctx, q, pk))
}

// InstallationByDBID loads an installation by its public database id, the id
// the installation object and its access_tokens_url expose to API clients.
func (s *Store) InstallationByDBID(ctx context.Context, dbID int64) (*InstallationRow, error) {
	q := s.rebind(`SELECT ` + installationColumns + ` FROM installations WHERE db_id = ?`)
	return scanInstallation(s.rdb.QueryRowContext(ctx, q, dbID))
}

// InstallationsByAppPK returns all installations for an app, ordered by created_at.
func (s *Store) InstallationsByAppPK(ctx context.Context, appPK int64) ([]*InstallationRow, error) {
	q := s.rebind(`SELECT ` + installationColumns +
		` FROM installations WHERE app_pk = ? ORDER BY created_at`)
	rows, err := s.rdb.QueryContext(ctx, q, appPK)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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

// InstallationByAppAndAccount loads the installation of app appPK on the
// account accountPK, the lookup GET /repos/{owner}/{repo}/installation needs to
// resolve a repository's owning account back to its installation.
func (s *Store) InstallationByAppAndAccount(ctx context.Context, appPK, accountPK int64) (*InstallationRow, error) {
	q := s.rebind(`SELECT ` + installationColumns +
		` FROM installations WHERE app_pk = ? AND account_pk = ?`)
	return scanInstallation(s.rdb.QueryRowContext(ctx, q, appPK, accountPK))
}

// InstallationRepoPKs returns the repo PKs accessible to an installation.
func (s *Store) InstallationRepoPKs(ctx context.Context, instPK int64) ([]int64, error) {
	q := s.rebind(`SELECT repo_pk FROM installation_repositories WHERE installation_pk = ?`)
	rows, err := s.rdb.QueryContext(ctx, q, instPK)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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
		r         InstallationRow
		suspended nullTime
		created   nullTime
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

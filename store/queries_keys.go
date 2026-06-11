package store

import (
	"context"
	"database/sql"
	"errors"
)

const sshKeyColumns = `pk, db_id, user_pk, title, key_type, public_key,
	fingerprint, read_only, repo_pk, last_used_at, created_at`

// SSHKeysByUser returns all SSH keys (not deploy keys) for a user.
func (s *Store) SSHKeysByUser(ctx context.Context, userPK int64) ([]*SSHKeyRow, error) {
	q := s.rebind(`SELECT ` + sshKeyColumns +
		` FROM ssh_keys WHERE user_pk = ? AND repo_pk IS NULL ORDER BY created_at`)
	return scanSSHKeys(s.rdb.QueryContext(ctx, q, userPK))
}

// DeployKeysByRepo returns all deploy keys for a repository.
func (s *Store) DeployKeysByRepo(ctx context.Context, repoPK int64) ([]*SSHKeyRow, error) {
	q := s.rebind(`SELECT ` + sshKeyColumns +
		` FROM ssh_keys WHERE repo_pk = ? ORDER BY created_at`)
	return scanSSHKeys(s.rdb.QueryContext(ctx, q, repoPK))
}

// SSHKeyByPK loads an SSH key by primary key.
func (s *Store) SSHKeyByPK(ctx context.Context, pk int64) (*SSHKeyRow, error) {
	q := s.rebind(`SELECT ` + sshKeyColumns + ` FROM ssh_keys WHERE pk = ?`)
	return scanSSHKey(s.rdb.QueryRowContext(ctx, q, pk))
}

// SSHKeyByDBID loads an SSH key by its public db_id.
func (s *Store) SSHKeyByDBID(ctx context.Context, dbID int64) (*SSHKeyRow, error) {
	q := s.rebind(`SELECT ` + sshKeyColumns + ` FROM ssh_keys WHERE db_id = ?`)
	return scanSSHKey(s.rdb.QueryRowContext(ctx, q, dbID))
}

// InsertSSHKey inserts a new SSH key and fills PK, DBID, and CreatedAt back onto k.
func (s *Store) InsertSSHKey(ctx context.Context, k *SSHKeyRow) error {
	dbID, err := s.AllocDBID(ctx)
	if err != nil {
		return err
	}
	k.DBID = dbID
	q := s.rebind(`INSERT INTO ssh_keys
		(db_id, user_pk, title, key_type, public_key, fingerprint, read_only, repo_pk)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, created_at`)
	var created nullTime
	err = s.db.QueryRowContext(ctx, q,
		dbID, k.UserPK, k.Title, k.KeyType, k.PublicKey, k.Fingerprint,
		k.ReadOnly, i64Arg(k.RepoPK),
	).Scan(&k.PK, &created)
	if err != nil {
		return err
	}
	k.CreatedAt = created.Time
	return nil
}

// DeleteSSHKey deletes an SSH key by primary key.
func (s *Store) DeleteSSHKey(ctx context.Context, pk int64) error {
	q := s.rebind(`DELETE FROM ssh_keys WHERE pk = ?`)
	res, err := s.db.ExecContext(ctx, q, pk)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

const branchProtColumns = `pk, repo_pk, branch_pattern, require_pr_reviews,
	required_approving_count, dismiss_stale_reviews, require_code_owner_reviews,
	require_status_checks, require_branches_up_to_date, status_check_contexts,
	enforce_admins, restrictions_users, restrictions_teams, restrictions_enabled,
	allow_force_pushes, allow_deletions, created_at, updated_at`

// BranchProtectionByPattern loads a branch protection rule for a specific pattern.
func (s *Store) BranchProtectionByPattern(ctx context.Context, repoPK int64, pattern string) (*BranchProtectionRow, error) {
	q := s.rebind(`SELECT ` + branchProtColumns +
		` FROM branch_protections WHERE repo_pk = ? AND branch_pattern = ?`)
	return scanBranchProtection(s.rdb.QueryRowContext(ctx, q, repoPK, pattern))
}

// UpsertBranchProtection inserts or replaces a branch protection rule.
func (s *Store) UpsertBranchProtection(ctx context.Context, r *BranchProtectionRow) error {
	q := s.rebind(`INSERT INTO branch_protections
		(repo_pk, branch_pattern, require_pr_reviews, required_approving_count,
		 dismiss_stale_reviews, require_code_owner_reviews, require_status_checks,
		 require_branches_up_to_date, status_check_contexts, enforce_admins,
		 restrictions_users, restrictions_teams, restrictions_enabled,
		 allow_force_pushes, allow_deletions, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (repo_pk, branch_pattern) DO UPDATE SET
		  require_pr_reviews = excluded.require_pr_reviews,
		  required_approving_count = excluded.required_approving_count,
		  dismiss_stale_reviews = excluded.dismiss_stale_reviews,
		  require_code_owner_reviews = excluded.require_code_owner_reviews,
		  require_status_checks = excluded.require_status_checks,
		  require_branches_up_to_date = excluded.require_branches_up_to_date,
		  status_check_contexts = excluded.status_check_contexts,
		  enforce_admins = excluded.enforce_admins,
		  restrictions_users = excluded.restrictions_users,
		  restrictions_teams = excluded.restrictions_teams,
		  restrictions_enabled = excluded.restrictions_enabled,
		  allow_force_pushes = excluded.allow_force_pushes,
		  allow_deletions = excluded.allow_deletions,
		  updated_at = excluded.updated_at
		RETURNING pk, created_at, updated_at`)
	var created, updated nullTime
	err := s.db.QueryRowContext(ctx, q,
		r.RepoPK, r.BranchPattern, r.RequirePRReviews, r.RequiredApprovingCount,
		r.DismissStaleReviews, r.RequireCodeOwnerReviews, r.RequireStatusChecks,
		r.RequireBranchesUpToDate, r.StatusCheckContexts, r.EnforceAdmins,
		r.RestrictionsUsers, r.RestrictionsTeams, r.RestrictionsEnabled,
		r.AllowForcePushes, r.AllowDeletions, nowUTC(),
	).Scan(&r.PK, &created, &updated)
	if err != nil {
		return err
	}
	r.CreatedAt = created.Time
	r.UpdatedAt = updated.Time
	return nil
}

// DeleteBranchProtection removes a branch protection rule.
func (s *Store) DeleteBranchProtection(ctx context.Context, repoPK int64, pattern string) error {
	q := s.rebind(`DELETE FROM branch_protections WHERE repo_pk = ? AND branch_pattern = ?`)
	res, err := s.db.ExecContext(ctx, q, repoPK, pattern)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanSSHKeys(rows *sql.Rows, err error) ([]*SSHKeyRow, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SSHKeyRow
	for rows.Next() {
		k, err := scanSSHKeyRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func scanSSHKey(row interface{ Scan(...any) error }) (*SSHKeyRow, error) {
	k, err := scanSSHKeyRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return k, err
}

func scanSSHKeyRow(row interface{ Scan(...any) error }) (*SSHKeyRow, error) {
	var (
		k                 SSHKeyRow
		title             sql.NullString
		repoPK            sql.NullInt64
		lastUsed, created nullTime
	)
	err := row.Scan(
		&k.PK, &k.DBID, &k.UserPK, &title, &k.KeyType, &k.PublicKey,
		&k.Fingerprint, &k.ReadOnly, &repoPK, &lastUsed, &created,
	)
	if err != nil {
		return nil, err
	}
	if title.Valid {
		k.Title = &title.String
	}
	k.RepoPK = i64Ptr(repoPK)
	k.LastUsedAt = lastUsed.ptr()
	k.CreatedAt = created.Time
	return &k, nil
}

func scanBranchProtection(row interface{ Scan(...any) error }) (*BranchProtectionRow, error) {
	var (
		r                BranchProtectionRow
		created, updated nullTime
	)
	err := row.Scan(
		&r.PK, &r.RepoPK, &r.BranchPattern, &r.RequirePRReviews,
		&r.RequiredApprovingCount, &r.DismissStaleReviews, &r.RequireCodeOwnerReviews,
		&r.RequireStatusChecks, &r.RequireBranchesUpToDate, &r.StatusCheckContexts,
		&r.EnforceAdmins, &r.RestrictionsUsers, &r.RestrictionsTeams,
		&r.RestrictionsEnabled, &r.AllowForcePushes, &r.AllowDeletions,
		&created, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.CreatedAt = created.Time
	r.UpdatedAt = updated.Time
	return &r, nil
}

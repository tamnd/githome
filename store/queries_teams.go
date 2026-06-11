package store

import (
	"context"
	"database/sql"
	"errors"
)

// UpdateRepoTopics replaces the topics JSON for a repository.
func (s *Store) UpdateRepoTopics(ctx context.Context, repoPK int64, topicsJSON string) error {
	q := s.rebind(`UPDATE repositories SET topics = ?, updated_at = ?
		WHERE pk = ? AND deleted_at IS NULL`)
	_, err := s.db.ExecContext(ctx, q, topicsJSON, nowUTC(), repoPK)
	return err
}

// CollaboratorByRepo returns the permission for a user on a repo, or ErrNotFound.
func (s *Store) CollaboratorByRepo(ctx context.Context, repoPK, userPK int64) (*CollaboratorRow, error) {
	q := s.rebind(`SELECT pk, repo_pk, user_pk, permission FROM collaborators
		WHERE repo_pk = ? AND user_pk = ?`)
	var r CollaboratorRow
	err := s.rdb.QueryRowContext(ctx, q, repoPK, userPK).Scan(&r.PK, &r.RepoPK, &r.UserPK, &r.Permission)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// CollaboratorsByRepo lists every collaborator grant on a repo, oldest grant
// first, the order the collaborators listing renders.
func (s *Store) CollaboratorsByRepo(ctx context.Context, repoPK int64) ([]*CollaboratorRow, error) {
	q := s.rebind(`SELECT pk, repo_pk, user_pk, permission FROM collaborators
		WHERE repo_pk = ? ORDER BY pk`)
	rows, err := s.rdb.QueryContext(ctx, q, repoPK)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*CollaboratorRow
	for rows.Next() {
		var r CollaboratorRow
		if err := rows.Scan(&r.PK, &r.RepoPK, &r.UserPK, &r.Permission); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// UpsertCollaborator sets (or updates) a collaborator's permission on a repo.
func (s *Store) UpsertCollaborator(ctx context.Context, repoPK, userPK int64, permission string) error {
	q := s.rebind(`INSERT INTO collaborators (repo_pk, user_pk, permission)
		VALUES (?, ?, ?)
		ON CONFLICT (repo_pk, user_pk) DO UPDATE SET permission = excluded.permission`)
	_, err := s.db.ExecContext(ctx, q, repoPK, userPK, permission)
	return err
}

// DeleteCollaborator removes a collaborator from a repo.
func (s *Store) DeleteCollaborator(ctx context.Context, repoPK, userPK int64) error {
	q := s.rebind(`DELETE FROM collaborators WHERE repo_pk = ? AND user_pk = ?`)
	res, err := s.db.ExecContext(ctx, q, repoPK, userPK)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

const teamColumns = `pk, db_id, org_pk, name, slug, description, privacy, permission, created_at, updated_at`

// TeamBySlug loads a team by its org and slug.
func (s *Store) TeamBySlug(ctx context.Context, orgPK int64, slug string) (*TeamRow, error) {
	q := s.rebind(`SELECT ` + teamColumns + ` FROM teams WHERE org_pk = ? AND slug = ?`)
	return scanTeam(s.rdb.QueryRowContext(ctx, q, orgPK, slug))
}

// TeamByPK loads a team by primary key.
func (s *Store) TeamByPK(ctx context.Context, pk int64) (*TeamRow, error) {
	q := s.rebind(`SELECT ` + teamColumns + ` FROM teams WHERE pk = ?`)
	return scanTeam(s.rdb.QueryRowContext(ctx, q, pk))
}

// InsertTeam inserts a new team and fills PK, DBID, CreatedAt, UpdatedAt back onto t.
func (s *Store) InsertTeam(ctx context.Context, t *TeamRow) error {
	dbID, err := s.AllocDBID(ctx)
	if err != nil {
		return err
	}
	t.DBID = dbID
	q := s.rebind(`INSERT INTO teams (db_id, org_pk, name, slug, description, privacy, permission)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, created_at, updated_at`)
	var created, updated nullTime
	err = s.db.QueryRowContext(ctx, q,
		dbID, t.OrgPK, t.Name, t.Slug, argStr(t.Description), t.Privacy, t.Permission,
	).Scan(&t.PK, &created, &updated)
	if err != nil {
		return err
	}
	t.CreatedAt, t.UpdatedAt = created.Time, updated.Time
	return nil
}

// UpdateTeam applies partial updates to a team.
func (s *Store) UpdateTeam(ctx context.Context, pk int64, name, description, privacy, permission *string) (*TeamRow, error) {
	q := s.rebind(`UPDATE teams SET
		name       = COALESCE(?, name),
		description = COALESCE(?, description),
		privacy    = COALESCE(?, privacy),
		permission = COALESCE(?, permission),
		updated_at = ?
		WHERE pk = ?
		RETURNING ` + teamColumns)
	return scanTeam(s.db.QueryRowContext(ctx, q, name, description, privacy, permission, nowUTC(), pk))
}

// DeleteTeam removes a team.
func (s *Store) DeleteTeam(ctx context.Context, pk int64) error {
	q := s.rebind(`DELETE FROM teams WHERE pk = ?`)
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

// UpsertTeamMember adds or updates a team member's role.
func (s *Store) UpsertTeamMember(ctx context.Context, teamPK, userPK int64, role string) error {
	q := s.rebind(`INSERT INTO team_members (team_pk, user_pk, role) VALUES (?, ?, ?)
		ON CONFLICT (team_pk, user_pk) DO UPDATE SET role = excluded.role`)
	_, err := s.db.ExecContext(ctx, q, teamPK, userPK, role)
	return err
}

// TeamMemberRole returns the role of a user in a team, or ErrNotFound.
func (s *Store) TeamMemberRole(ctx context.Context, teamPK, userPK int64) (string, error) {
	q := s.rebind(`SELECT role FROM team_members WHERE team_pk = ? AND user_pk = ?`)
	var role string
	err := s.rdb.QueryRowContext(ctx, q, teamPK, userPK).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return role, err
}

// DeleteTeamMember removes a user from a team.
func (s *Store) DeleteTeamMember(ctx context.Context, teamPK, userPK int64) error {
	q := s.rebind(`DELETE FROM team_members WHERE team_pk = ? AND user_pk = ?`)
	res, err := s.db.ExecContext(ctx, q, teamPK, userPK)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpsertTeamRepo adds or updates a team's permission on a repo.
func (s *Store) UpsertTeamRepo(ctx context.Context, teamPK, repoPK int64, permission string) error {
	q := s.rebind(`INSERT INTO team_repos (team_pk, repo_pk, permission) VALUES (?, ?, ?)
		ON CONFLICT (team_pk, repo_pk) DO UPDATE SET permission = excluded.permission`)
	_, err := s.db.ExecContext(ctx, q, teamPK, repoPK, permission)
	return err
}

// TeamRepoPermission returns the permission level for a repo in a team, or ErrNotFound.
func (s *Store) TeamRepoPermission(ctx context.Context, teamPK, repoPK int64) (string, error) {
	q := s.rebind(`SELECT permission FROM team_repos WHERE team_pk = ? AND repo_pk = ?`)
	var permission string
	err := s.rdb.QueryRowContext(ctx, q, teamPK, repoPK).Scan(&permission)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return permission, err
}

// DeleteTeamRepo removes a repo from a team.
func (s *Store) DeleteTeamRepo(ctx context.Context, teamPK, repoPK int64) error {
	q := s.rebind(`DELETE FROM team_repos WHERE team_pk = ? AND repo_pk = ?`)
	res, err := s.db.ExecContext(ctx, q, teamPK, repoPK)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanTeam(row interface{ Scan(...any) error }) (*TeamRow, error) {
	var (
		t           TeamRow
		description sql.NullString
		created     nullTime
		updated     nullTime
	)
	err := row.Scan(
		&t.PK, &t.DBID, &t.OrgPK, &t.Name, &t.Slug, &description,
		&t.Privacy, &t.Permission, &created, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if description.Valid {
		t.Description = &description.String
	}
	t.CreatedAt, t.UpdatedAt = created.Time, updated.Time
	return &t, nil
}

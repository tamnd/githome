package store

import (
	"context"
	"database/sql"
	"errors"
)

// repoColumns is the shared SELECT list, qualified with the `r` alias so the
// owner-name lookup can join users without column ambiguity. The point lookups
// alias the table too, keeping one scan order across every query path.
const repoColumns = `r.pk, r.db_id, r.owner_pk, r.name, r.description, r.homepage,
	r.private, r.fork, r.default_branch, r.has_issues, r.has_projects, r.has_wiki,
	r.has_downloads, r.archived, r.disabled, r.is_template, r.open_issues_count,
	r.pushed_at, r.created_at, r.updated_at`

// RepoByOwnerName resolves a repository by its owner login (case-insensitively,
// matching GitHub's case-preserving account and repo names) and repo name. It
// returns ErrNotFound when no live row matches.
func (s *Store) RepoByOwnerName(ctx context.Context, owner, name string) (*RepoRow, error) {
	q := s.rebind(`SELECT ` + repoColumns + ` FROM repositories r
		JOIN users u ON u.pk = r.owner_pk
		WHERE lower(u.login) = lower(?) AND lower(r.name) = lower(?)
		  AND r.deleted_at IS NULL AND u.deleted_at IS NULL`)
	return scanRepo(s.db.QueryRowContext(ctx, q, owner, name))
}

// RepoByPK loads a repository by primary key.
func (s *Store) RepoByPK(ctx context.Context, pk int64) (*RepoRow, error) {
	q := s.rebind(`SELECT ` + repoColumns + ` FROM repositories r
		WHERE r.pk = ? AND r.deleted_at IS NULL`)
	return scanRepo(s.db.QueryRowContext(ctx, q, pk))
}

// RepoByDBID loads a repository by its public database id.
func (s *Store) RepoByDBID(ctx context.Context, dbID int64) (*RepoRow, error) {
	q := s.rebind(`SELECT ` + repoColumns + ` FROM repositories r
		WHERE r.db_id = ? AND r.deleted_at IS NULL`)
	return scanRepo(s.db.QueryRowContext(ctx, q, dbID))
}

// InsertRepo allocates the shared db_id, writes the row, and fills the
// server-assigned fields back onto r. The feature and state flags are left to
// their column defaults (GitHub turns issues, projects, wiki, and downloads on
// at creation; archived, disabled, and is_template start off) and read back via
// RETURNING, so a freshly inserted RepoRow reflects exactly what is stored. A
// repository-settings update path arrives with its milestone.
func (s *Store) InsertRepo(ctx context.Context, r *RepoRow) error {
	dbID, err := s.AllocDBID(ctx)
	if err != nil {
		return err
	}
	if r.DefaultBranch == "" {
		r.DefaultBranch = "main"
	}
	q := s.rebind(`INSERT INTO repositories
		(db_id, owner_pk, name, description, homepage, private, fork,
		 default_branch, open_issues_count, pushed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id, created_at, updated_at,
		          has_issues, has_projects, has_wiki, has_downloads,
		          archived, disabled, is_template`)
	var (
		created, updated                              nullTime
		hasIssues, hasProjects, hasWiki, hasDownloads boolVal
		archived, disabled, isTemplate                boolVal
	)
	err = s.db.QueryRowContext(ctx, q,
		dbID, r.OwnerPK, r.Name, argStr(r.Description), argStr(r.Homepage),
		r.Private, r.Fork, r.DefaultBranch, r.OpenIssuesCount, argTime(r.PushedAt),
	).Scan(
		&r.PK, &r.DBID, &created, &updated,
		&hasIssues, &hasProjects, &hasWiki, &hasDownloads,
		&archived, &disabled, &isTemplate,
	)
	if err != nil {
		return err
	}
	r.CreatedAt, r.UpdatedAt = created.Time, updated.Time
	r.HasIssues, r.HasProjects = hasIssues.Bool, hasProjects.Bool
	r.HasWiki, r.HasDownloads = hasWiki.Bool, hasDownloads.Bool
	r.Archived, r.Disabled, r.IsTemplate = archived.Bool, disabled.Bool, isTemplate.Bool
	return nil
}

// scanRepo maps one repositories row into a RepoRow, absorbing the dialect
// differences for nullable text, the boolean flags, and timestamps.
func scanRepo(row interface{ Scan(...any) error }) (*RepoRow, error) {
	var (
		r                                                            RepoRow
		description, homepage                                        sql.NullString
		private, fork, hasIssues, hasProjects, hasWiki, hasDownloads boolVal
		archived, disabled, isTemplate                               boolVal
		pushed, created, updated                                     nullTime
	)
	err := row.Scan(
		&r.PK, &r.DBID, &r.OwnerPK, &r.Name, &description, &homepage,
		&private, &fork, &r.DefaultBranch, &hasIssues, &hasProjects, &hasWiki,
		&hasDownloads, &archived, &disabled, &isTemplate, &r.OpenIssuesCount,
		&pushed, &created, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.Description, r.Homepage = strPtr(description), strPtr(homepage)
	r.Private, r.Fork = private.Bool, fork.Bool
	r.HasIssues, r.HasProjects = hasIssues.Bool, hasProjects.Bool
	r.HasWiki, r.HasDownloads = hasWiki.Bool, hasDownloads.Bool
	r.Archived, r.Disabled, r.IsTemplate = archived.Bool, disabled.Bool, isTemplate.Bool
	r.PushedAt = pushed.ptr()
	r.CreatedAt, r.UpdatedAt = created.Time, updated.Time
	return &r, nil
}

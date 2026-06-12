package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ReleaseRow is a row of the releases table.
type ReleaseRow struct {
	PK              int64
	DBID            int64
	RepoPK          int64
	TagName         string
	TargetCommitish string
	Name            *string
	Body            *string
	Draft           bool
	Prerelease      bool
	AuthorPK        *int64
	LockVersion     int64
	CreatedAt       time.Time
	PublishedAt     *time.Time
	UpdatedAt       time.Time
}

// ReleaseAssetRow is a row of the release_assets table.
type ReleaseAssetRow struct {
	PK            int64
	DBID          int64
	ReleasePK     int64
	ReleaseDBID   int64 // public id of the owning release, for asset urls
	Name          string
	Label         *string
	ContentType   string
	Size          int64
	DownloadCount int64
	UploaderPK    *int64
	State         string
	LockVersion   int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

const releaseColumns = `pk, db_id, repo_pk, tag_name, target_commitish, name, body,
	draft, prerelease, author_pk, lock_version, created_at, published_at, updated_at`

const releaseAssetColumns = `pk, db_id, release_pk,
	(SELECT r.db_id FROM releases r WHERE r.pk = release_assets.release_pk),
	name, label, content_type,
	size, download_count, uploader_pk, state, lock_version, created_at, updated_at`

// GetReleaseByID loads a release by its public database id within a repo.
func (s *Store) GetReleaseByID(ctx context.Context, repoPK, dbID int64) (*ReleaseRow, error) {
	q := s.rebind(`SELECT ` + releaseColumns + ` FROM releases
		WHERE repo_pk = ? AND db_id = ? AND deleted_at IS NULL`)
	return scanRelease(s.rdb.QueryRowContext(ctx, q, repoPK, dbID))
}

// GetReleaseByPK loads a release by its internal pk, the lookup the webhook
// renderer resolves a recorded release event through.
func (s *Store) GetReleaseByPK(ctx context.Context, pk int64) (*ReleaseRow, error) {
	q := s.rebind(`SELECT ` + releaseColumns + ` FROM releases
		WHERE pk = ? AND deleted_at IS NULL`)
	return scanRelease(s.db.QueryRowContext(ctx, q, pk))
}

// GetReleaseByTag loads a release by its tag name within a repository.
func (s *Store) GetReleaseByTag(ctx context.Context, repoPK int64, tag string) (*ReleaseRow, error) {
	q := s.rebind(`SELECT ` + releaseColumns + ` FROM releases
		WHERE repo_pk = ? AND tag_name = ? AND deleted_at IS NULL`)
	return scanRelease(s.rdb.QueryRowContext(ctx, q, repoPK, tag))
}

// GetLatestRelease returns the most recent non-draft, non-prerelease release
// ordered by published_at descending.
func (s *Store) GetLatestRelease(ctx context.Context, repoPK int64) (*ReleaseRow, error) {
	q := s.rebind(`SELECT ` + releaseColumns + ` FROM releases
		WHERE repo_pk = ? AND draft = ? AND prerelease = ? AND deleted_at IS NULL
		ORDER BY published_at DESC LIMIT 1`)
	return scanRelease(s.rdb.QueryRowContext(ctx, q, repoPK, false, false))
}

// ListReleases returns a page of releases for a repository, newest first.
// When includeDrafts is false, draft rows are excluded.
func (s *Store) ListReleases(ctx context.Context, repoPK int64, includeDrafts bool, limit, offset int) ([]ReleaseRow, error) {
	if limit <= 0 {
		limit = 30
	}
	var rows *sql.Rows
	var err error
	if includeDrafts {
		q := s.rebind(`SELECT ` + releaseColumns + ` FROM releases
			WHERE repo_pk = ? AND deleted_at IS NULL
			ORDER BY created_at DESC LIMIT ? OFFSET ?`)
		rows, err = s.rdb.QueryContext(ctx, q, repoPK, limit, offset)
	} else {
		q := s.rebind(`SELECT ` + releaseColumns + ` FROM releases
			WHERE repo_pk = ? AND draft = ? AND deleted_at IS NULL
			ORDER BY created_at DESC LIMIT ? OFFSET ?`)
		rows, err = s.rdb.QueryContext(ctx, q, repoPK, false, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ReleaseRow
	for rows.Next() {
		r, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// InsertRelease allocates a db_id and writes the row, filling server-assigned
// fields back onto r.
func (s *Store) InsertRelease(ctx context.Context, r *ReleaseRow) error {
	dbID, err := s.AllocDBID(ctx)
	if err != nil {
		return err
	}
	r.DBID = dbID
	q := s.rebind(`INSERT INTO releases
		(db_id, repo_pk, tag_name, target_commitish, name, body, draft, prerelease,
		 author_pk, published_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, created_at, updated_at`)
	var created, updated nullTime
	err = s.db.QueryRowContext(ctx, q,
		r.DBID, r.RepoPK, r.TagName, r.TargetCommitish,
		argStr(r.Name), argStr(r.Body),
		r.Draft, r.Prerelease,
		argNullInt64(r.AuthorPK), argTime(r.PublishedAt),
	).Scan(&r.PK, &created, &updated)
	if err != nil {
		return err
	}
	r.CreatedAt, r.UpdatedAt = created.Time, updated.Time
	return nil
}

// UpdateRelease writes editable fields back to an existing release using
// optimistic locking on lock_version.
func (s *Store) UpdateRelease(ctx context.Context, r *ReleaseRow) error {
	now := time.Now().UTC()
	q := s.rebind(`UPDATE releases
		SET tag_name = ?, target_commitish = ?, name = ?, body = ?,
		    draft = ?, prerelease = ?, published_at = ?,
		    lock_version = lock_version + 1, updated_at = ?
		WHERE pk = ? AND lock_version = ? AND deleted_at IS NULL
		RETURNING lock_version, updated_at`)
	var updated nullTime
	err := s.db.QueryRowContext(ctx, q,
		r.TagName, r.TargetCommitish, argStr(r.Name), argStr(r.Body),
		r.Draft, r.Prerelease, argTime(r.PublishedAt), now,
		r.PK, r.LockVersion,
	).Scan(&r.LockVersion, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	r.UpdatedAt = updated.Time
	return nil
}

// DeleteRelease soft-deletes a release row.
func (s *Store) DeleteRelease(ctx context.Context, pk int64) error {
	q := s.rebind(`UPDATE releases SET deleted_at = ? WHERE pk = ? AND deleted_at IS NULL`)
	res, err := s.db.ExecContext(ctx, q, time.Now().UTC(), pk)
	if err != nil {
		return err
	}
	return affectedOrNotFound(res)
}

// ListReleaseAssets returns all non-deleted assets for a release.
func (s *Store) ListReleaseAssets(ctx context.Context, releasePK int64) ([]ReleaseAssetRow, error) {
	q := s.rebind(`SELECT ` + releaseAssetColumns + ` FROM release_assets
		WHERE release_pk = ? AND deleted_at IS NULL ORDER BY created_at`)
	rows, err := s.rdb.QueryContext(ctx, q, releasePK)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ReleaseAssetRow
	for rows.Next() {
		a, err := scanReleaseAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// GetReleaseAssetByID loads a release asset by its public database id.
func (s *Store) GetReleaseAssetByID(ctx context.Context, dbID int64) (*ReleaseAssetRow, error) {
	q := s.rebind(`SELECT ` + releaseAssetColumns + ` FROM release_assets
		WHERE db_id = ? AND deleted_at IS NULL`)
	return scanReleaseAsset(s.rdb.QueryRowContext(ctx, q, dbID))
}

// InsertReleaseAsset allocates a db_id and writes the row.
func (s *Store) InsertReleaseAsset(ctx context.Context, a *ReleaseAssetRow) error {
	dbID, err := s.AllocDBID(ctx)
	if err != nil {
		return err
	}
	a.DBID = dbID
	q := s.rebind(`INSERT INTO release_assets
		(db_id, release_pk, name, label, content_type, size, uploader_pk, state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, created_at, updated_at`)
	var created, updated nullTime
	err = s.db.QueryRowContext(ctx, q,
		a.DBID, a.ReleasePK, a.Name, argStr(a.Label),
		a.ContentType, a.Size, argNullInt64(a.UploaderPK), a.State,
	).Scan(&a.PK, &created, &updated)
	if err != nil {
		return err
	}
	a.CreatedAt, a.UpdatedAt = created.Time, updated.Time
	return nil
}

// UpdateReleaseAsset writes editable fields (name, label, state) back to an
// existing asset using optimistic locking.
func (s *Store) UpdateReleaseAsset(ctx context.Context, a *ReleaseAssetRow) error {
	now := time.Now().UTC()
	q := s.rebind(`UPDATE release_assets
		SET name = ?, label = ?, state = ?,
		    lock_version = lock_version + 1, updated_at = ?
		WHERE pk = ? AND lock_version = ? AND deleted_at IS NULL
		RETURNING lock_version, updated_at`)
	var updated nullTime
	err := s.db.QueryRowContext(ctx, q,
		a.Name, argStr(a.Label), a.State, now,
		a.PK, a.LockVersion,
	).Scan(&a.LockVersion, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	a.UpdatedAt = updated.Time
	return nil
}

// IncrementAssetDownloadCount bumps the download counter for an asset.
func (s *Store) IncrementAssetDownloadCount(ctx context.Context, pk int64) error {
	q := s.rebind(`UPDATE release_assets SET download_count = download_count + 1 WHERE pk = ? AND deleted_at IS NULL`)
	_, err := s.db.ExecContext(ctx, q, pk)
	return err
}

// DeleteReleaseAsset soft-deletes a release asset.
func (s *Store) DeleteReleaseAsset(ctx context.Context, pk int64) error {
	q := s.rebind(`UPDATE release_assets SET deleted_at = ? WHERE pk = ? AND deleted_at IS NULL`)
	res, err := s.db.ExecContext(ctx, q, time.Now().UTC(), pk)
	if err != nil {
		return err
	}
	return affectedOrNotFound(res)
}

// argNullInt64 binds a nullable int64 pointer as SQL NULL when nil.
func argNullInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// --- scanners ---------------------------------------------------------------

type releaseScanner interface{ Scan(dest ...any) error }

func scanRelease(row releaseScanner) (*ReleaseRow, error) {
	var r ReleaseRow
	var name, body sql.NullString
	var authorPK sql.NullInt64
	var created, updated, published nullTime
	var draft, prerelease boolVal
	err := row.Scan(
		&r.PK, &r.DBID, &r.RepoPK, &r.TagName, &r.TargetCommitish,
		&name, &body, &draft, &prerelease, &authorPK,
		&r.LockVersion, &created, &published, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.Name = strPtr(name)
	r.Body = strPtr(body)
	r.Draft, r.Prerelease = draft.Bool, prerelease.Bool
	r.AuthorPK = i64Ptr(authorPK)
	r.CreatedAt, r.UpdatedAt = created.Time, updated.Time
	r.PublishedAt = published.ptr()
	return &r, nil
}

type assetScanner interface{ Scan(dest ...any) error }

func scanReleaseAsset(row assetScanner) (*ReleaseAssetRow, error) {
	var a ReleaseAssetRow
	var label sql.NullString
	var uploaderPK sql.NullInt64
	var created, updated nullTime
	err := row.Scan(
		&a.PK, &a.DBID, &a.ReleasePK, &a.ReleaseDBID, &a.Name, &label,
		&a.ContentType, &a.Size, &a.DownloadCount, &uploaderPK,
		&a.State, &a.LockVersion, &created, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.Label = strPtr(label)
	a.UploaderPK = i64Ptr(uploaderPK)
	a.CreatedAt, a.UpdatedAt = created.Time, updated.Time
	return &a, nil
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// InsertGist stores a new gist with its files. It sets PK, CreatedAt, and
// UpdatedAt on g; each file's PK and GistPK are set on success.
func (s *Store) InsertGist(ctx context.Context, g *GistRow) error {
	q := s.rebind(`INSERT INTO gists (gist_id, owner_pk, description, public)
		VALUES (?, ?, ?, ?) RETURNING pk, created_at, updated_at`)
	var cr, up nullTime
	err := s.db.QueryRowContext(ctx, q,
		g.GistID, g.OwnerPK, g.Description, g.Public,
	).Scan(&g.PK, &cr, &up)
	if err != nil {
		return err
	}
	g.CreatedAt = cr.Time
	g.UpdatedAt = up.Time

	for i := range g.Files {
		fq := s.rebind(`INSERT INTO gist_files (gist_pk, filename, content)
			VALUES (?, ?, ?) RETURNING pk`)
		err := s.db.QueryRowContext(ctx, fq,
			g.PK, g.Files[i].Filename, g.Files[i].Content,
		).Scan(&g.Files[i].PK)
		if err != nil {
			return err
		}
		g.Files[i].GistPK = g.PK
	}
	return nil
}

// GetGistByID fetches a gist and its files by its hex gist_id.
func (s *Store) GetGistByID(ctx context.Context, gistID string) (*GistRow, error) {
	q := s.rebind(`SELECT pk, gist_id, owner_pk, description, public, created_at, updated_at
		FROM gists WHERE gist_id = ?`)
	g, err := scanGist(s.db.QueryRowContext(ctx, q, gistID))
	if err != nil {
		return nil, err
	}
	if err := s.loadGistFiles(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}

// ListGistsByOwner returns up to perPage gists owned by ownerPK, offset by page.
func (s *Store) ListGistsByOwner(ctx context.Context, ownerPK int64, page, perPage int) ([]*GistRow, int, error) {
	countQ := s.rebind(`SELECT COUNT(*) FROM gists WHERE owner_pk = ?`)
	var total int
	if err := s.db.QueryRowContext(ctx, countQ, ownerPK).Scan(&total); err != nil {
		return nil, 0, err
	}

	q := s.rebind(`SELECT pk, gist_id, owner_pk, description, public, created_at, updated_at
		FROM gists WHERE owner_pk = ? ORDER BY updated_at DESC LIMIT ? OFFSET ?`)
	rows, err := s.db.QueryContext(ctx, q, ownerPK, perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	return s.scanGists(ctx, rows, total)
}

// ListPublicGists returns the most recently updated public gists.
func (s *Store) ListPublicGists(ctx context.Context, page, perPage int) ([]*GistRow, int, error) {
	countQ := `SELECT COUNT(*) FROM gists WHERE public = 1`
	var total int
	if err := s.db.QueryRowContext(ctx, countQ).Scan(&total); err != nil {
		return nil, 0, err
	}

	q := s.rebind(`SELECT pk, gist_id, owner_pk, description, public, created_at, updated_at
		FROM gists WHERE public = 1 ORDER BY updated_at DESC LIMIT ? OFFSET ?`)
	rows, err := s.db.QueryContext(ctx, q, perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	return s.scanGists(ctx, rows, total)
}

// ListStarredGists returns gists starred by userPK.
func (s *Store) ListStarredGists(ctx context.Context, userPK int64, page, perPage int) ([]*GistRow, int, error) {
	countQ := s.rebind(`SELECT COUNT(*) FROM gist_stars WHERE user_pk = ?`)
	var total int
	if err := s.db.QueryRowContext(ctx, countQ, userPK).Scan(&total); err != nil {
		return nil, 0, err
	}

	q := s.rebind(`SELECT g.pk, g.gist_id, g.owner_pk, g.description, g.public, g.created_at, g.updated_at
		FROM gists g JOIN gist_stars gs ON gs.gist_pk = g.pk
		WHERE gs.user_pk = ? ORDER BY g.updated_at DESC LIMIT ? OFFSET ?`)
	rows, err := s.db.QueryContext(ctx, q, userPK, perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	return s.scanGists(ctx, rows, total)
}

// UpdateGist updates description and/or files. Pass nil description to leave
// unchanged. Files map: nil value deletes the file, non-nil updates/creates it.
func (s *Store) UpdateGist(ctx context.Context, gistPK int64, description *string, files map[string]*string) error {
	now := time.Now().UTC()
	if description != nil {
		uq := s.rebind(`UPDATE gists SET description = ?, updated_at = ? WHERE pk = ?`)
		if _, err := s.db.ExecContext(ctx, uq, *description, now, gistPK); err != nil {
			return err
		}
	} else {
		uq := s.rebind(`UPDATE gists SET updated_at = ? WHERE pk = ?`)
		if _, err := s.db.ExecContext(ctx, uq, now, gistPK); err != nil {
			return err
		}
	}

	for filename, content := range files {
		if content == nil {
			dq := s.rebind(`DELETE FROM gist_files WHERE gist_pk = ? AND filename = ?`)
			if _, err := s.db.ExecContext(ctx, dq, gistPK, filename); err != nil {
				return err
			}
			continue
		}
		uq := s.rebind(`INSERT INTO gist_files (gist_pk, filename, content) VALUES (?, ?, ?)
			ON CONFLICT(gist_pk, filename) DO UPDATE SET content = excluded.content`)
		if _, err := s.db.ExecContext(ctx, uq, gistPK, filename, *content); err != nil {
			return err
		}
	}
	return nil
}

// DeleteGist removes a gist and its files (cascaded by FK).
func (s *Store) DeleteGist(ctx context.Context, gistID string) error {
	q := s.rebind(`DELETE FROM gists WHERE gist_id = ?`)
	_, err := s.db.ExecContext(ctx, q, gistID)
	return err
}

// StarGist records that userPK starred gistPK. Idempotent.
func (s *Store) StarGist(ctx context.Context, gistPK, userPK int64) error {
	q := s.rebind(`INSERT OR IGNORE INTO gist_stars (gist_pk, user_pk) VALUES (?, ?)`)
	_, err := s.db.ExecContext(ctx, q, gistPK, userPK)
	return err
}

// UnstarGist removes the star. Idempotent.
func (s *Store) UnstarGist(ctx context.Context, gistPK, userPK int64) error {
	q := s.rebind(`DELETE FROM gist_stars WHERE gist_pk = ? AND user_pk = ?`)
	_, err := s.db.ExecContext(ctx, q, gistPK, userPK)
	return err
}

// IsGistStarred reports whether userPK has starred gistPK.
func (s *Store) IsGistStarred(ctx context.Context, gistPK, userPK int64) (bool, error) {
	q := s.rebind(`SELECT 1 FROM gist_stars WHERE gist_pk = ? AND user_pk = ?`)
	var one int
	err := s.db.QueryRowContext(ctx, q, gistPK, userPK).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// InsertGistComment adds a comment to a gist.
func (s *Store) InsertGistComment(ctx context.Context, c *GistCommentRow) error {
	q := s.rebind(`INSERT INTO gist_comments (gist_pk, user_pk, body)
		VALUES (?, ?, ?) RETURNING pk, created_at, updated_at`)
	var cr, up nullTime
	err := s.db.QueryRowContext(ctx, q, c.GistPK, c.UserPK, c.Body).Scan(&c.PK, &cr, &up)
	if err != nil {
		return err
	}
	c.CreatedAt = cr.Time
	c.UpdatedAt = up.Time
	return nil
}

// ListGistComments returns all comments for a gist.
func (s *Store) ListGistComments(ctx context.Context, gistPK int64) ([]GistCommentRow, error) {
	q := s.rebind(`SELECT pk, gist_pk, user_pk, body, created_at, updated_at
		FROM gist_comments WHERE gist_pk = ? ORDER BY created_at ASC`)
	rows, err := s.db.QueryContext(ctx, q, gistPK)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []GistCommentRow
	for rows.Next() {
		var c GistCommentRow
		var cr, up nullTime
		if err := rows.Scan(&c.PK, &c.GistPK, &c.UserPK, &c.Body, &cr, &up); err != nil {
			return nil, err
		}
		c.CreatedAt = cr.Time
		c.UpdatedAt = up.Time
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanGist(row *sql.Row) (*GistRow, error) {
	var g GistRow
	var pub boolVal
	var cr, up nullTime
	err := row.Scan(&g.PK, &g.GistID, &g.OwnerPK, &g.Description, &pub, &cr, &up)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	g.Public = pub.Bool
	g.CreatedAt = cr.Time
	g.UpdatedAt = up.Time
	return &g, nil
}

func (s *Store) loadGistFiles(ctx context.Context, g *GistRow) error {
	q := s.rebind(`SELECT pk, gist_pk, filename, content FROM gist_files WHERE gist_pk = ? ORDER BY filename ASC`)
	rows, err := s.db.QueryContext(ctx, q, g.PK)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var f GistFileRow
		if err := rows.Scan(&f.PK, &f.GistPK, &f.Filename, &f.Content); err != nil {
			return err
		}
		g.Files = append(g.Files, f)
	}
	return rows.Err()
}

func (s *Store) scanGists(ctx context.Context, rows *sql.Rows, total int) ([]*GistRow, int, error) {
	var out []*GistRow
	for rows.Next() {
		var g GistRow
		var pub boolVal
		var cr, up nullTime
		if err := rows.Scan(&g.PK, &g.GistID, &g.OwnerPK, &g.Description, &pub, &cr, &up); err != nil {
			return nil, 0, err
		}
		g.Public = pub.Bool
		g.CreatedAt = cr.Time
		g.UpdatedAt = up.Time
		out = append(out, &g)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for _, g := range out {
		if err := s.loadGistFiles(ctx, g); err != nil {
			return nil, 0, err
		}
	}
	return out, total, nil
}

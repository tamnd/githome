package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ReactionRow is a row of the reactions table. Reactions are polymorphic: a
// (SubjectType, SubjectPK) pair points at an issue, a comment, or later a
// review. Content is one of GitHub's eight reaction names (+1, -1, laugh,
// confused, heart, hooray, rocket, eyes).
type ReactionRow struct {
	PK          int64
	DBID        int64
	SubjectType string
	SubjectPK   int64
	UserPK      int64
	Content     string
	CreatedAt   time.Time
}

// ReactionContents is the set of reaction names GitHub accepts, in the order its
// rollup reports them.
var ReactionContents = []string{"+1", "-1", "laugh", "confused", "heart", "hooray", "rocket", "eyes"}

// ListReactions returns every reaction on a subject, oldest first.
func (s *Store) ListReactions(ctx context.Context, subjectType string, subjectPK int64) ([]ReactionRow, error) {
	q := s.rebind(`SELECT pk, db_id, subject_type, subject_pk, user_pk, content, created_at
		FROM reactions WHERE subject_type = ? AND subject_pk = ? ORDER BY created_at, pk`)
	rows, err := s.db.QueryContext(ctx, q, subjectType, subjectPK)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ReactionRow
	for rows.Next() {
		var (
			r       ReactionRow
			created nullTime
		)
		if err := rows.Scan(&r.PK, &r.DBID, &r.SubjectType, &r.SubjectPK, &r.UserPK, &r.Content, &created); err != nil {
			return nil, err
		}
		r.CreatedAt = created.Time
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReactionRollup is the per-content count GitHub embeds on reactable objects,
// plus the total.
type ReactionRollup struct {
	TotalCount int
	Counts     map[string]int
}

// ReactionRollupFor returns the reaction counts for a subject keyed by content.
func (s *Store) ReactionRollupFor(ctx context.Context, subjectType string, subjectPK int64) (ReactionRollup, error) {
	q := s.rebind(`SELECT content, COUNT(*) FROM reactions
		WHERE subject_type = ? AND subject_pk = ? GROUP BY content`)
	rows, err := s.db.QueryContext(ctx, q, subjectType, subjectPK)
	if err != nil {
		return ReactionRollup{}, err
	}
	defer func() { _ = rows.Close() }()
	out := ReactionRollup{Counts: map[string]int{}}
	for rows.Next() {
		var content string
		var n int
		if err := rows.Scan(&content, &n); err != nil {
			return ReactionRollup{}, err
		}
		out.Counts[content] = n
		out.TotalCount += n
	}
	return out, rows.Err()
}

// InsertReaction adds a reaction, returning created=false when the user already
// reacted with that content on that subject (GitHub responds 200 with the
// existing reaction rather than creating a duplicate). The unique index
// backstops a race.
func (s *Store) InsertReaction(ctx context.Context, r *ReactionRow) (created bool, err error) {
	q := s.rebind(`SELECT pk, db_id, created_at FROM reactions
		WHERE subject_type = ? AND subject_pk = ? AND user_pk = ? AND content = ?`)
	var existingCreated nullTime
	switch err := s.db.QueryRowContext(ctx, q, r.SubjectType, r.SubjectPK, r.UserPK, r.Content).
		Scan(&r.PK, &r.DBID, &existingCreated); {
	case err == nil:
		r.CreatedAt = existingCreated.Time
		return false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return false, err
	}
	dbID, err := s.AllocDBID(ctx)
	if err != nil {
		return false, err
	}
	ins := s.rebind(`INSERT INTO reactions (db_id, subject_type, subject_pk, user_pk, content)
		VALUES (?, ?, ?, ?, ?)
		RETURNING pk, db_id, created_at`)
	var newCreated nullTime
	err = s.db.QueryRowContext(ctx, ins, dbID, r.SubjectType, r.SubjectPK, r.UserPK, r.Content).
		Scan(&r.PK, &r.DBID, &newCreated)
	if err != nil {
		return false, err
	}
	r.CreatedAt = newCreated.Time
	return true, nil
}

// DeleteReaction removes a reaction by its public database id, scoped to its
// subject so a caller cannot delete a reaction belonging to another object.
func (s *Store) DeleteReaction(ctx context.Context, subjectType string, subjectPK, dbID int64) error {
	q := s.rebind(`DELETE FROM reactions
		WHERE db_id = ? AND subject_type = ? AND subject_pk = ?`)
	res, err := s.db.ExecContext(ctx, q, dbID, subjectType, subjectPK)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

package store

import (
	"context"
	"database/sql"
	"strings"
)

// inPlaceholders returns a SQL IN clause fragment with n question marks:
// (?,?,?). When n is zero it returns (NULL) so the query matches nothing
// rather than being syntactically invalid.
func inPlaceholders(n int) string {
	if n == 0 {
		return "(NULL)"
	}
	return "(" + strings.Repeat("?,", n-1) + "?)"
}

// i64Args converts a []int64 into []any for QueryContext.
func i64Args(pks []int64) []any {
	a := make([]any, len(pks))
	for i, pk := range pks {
		a[i] = pk
	}
	return a
}

// UsersByPKs loads users by primary key in one round trip. PKs that have no
// live row are silently absent from the returned map.
func (s *Store) UsersByPKs(ctx context.Context, pks []int64) (map[int64]*UserRow, error) {
	if len(pks) == 0 {
		return map[int64]*UserRow{}, nil
	}
	q := s.rebind(`SELECT ` + userColumns + ` FROM users
		WHERE pk IN ` + inPlaceholders(len(pks)) + ` AND deleted_at IS NULL`)
	rows, err := s.rdb.QueryContext(ctx, q, i64Args(pks)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64]*UserRow, len(pks))
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out[u.PK] = u
	}
	return out, rows.Err()
}

// LabelsByIssuePKs loads all labels for the given issue PKs in one query.
// Returns a map from issue_pk to the ordered slice of labels for that issue.
func (s *Store) LabelsByIssuePKs(ctx context.Context, issuePKs []int64) (map[int64][]LabelRow, error) {
	if len(issuePKs) == 0 {
		return map[int64][]LabelRow{}, nil
	}
	q := s.rebind(`SELECT il.issue_pk, ` + labelColumnsAliased + `
		FROM labels l
		JOIN issue_labels il ON il.label_pk = l.pk
		WHERE il.issue_pk IN ` + inPlaceholders(len(issuePKs)) + `
		ORDER BY il.issue_pk, lower(l.name)`)
	rows, err := s.rdb.QueryContext(ctx, q, i64Args(issuePKs)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64][]LabelRow, len(issuePKs))
	for rows.Next() {
		var (
			issuePK   int64
			l         LabelRow
			desc      sql.NullString
			isDefault boolVal
			created   nullTime
			updated   nullTime
		)
		if err := rows.Scan(&issuePK, &l.PK, &l.DBID, &l.RepoPK, &l.Name, &l.Color, &desc, &isDefault, &created, &updated); err != nil {
			return nil, err
		}
		l.Description = strPtr(desc)
		l.IsDefault = isDefault.Bool
		l.CreatedAt, l.UpdatedAt = created.Time, updated.Time
		out[issuePK] = append(out[issuePK], l)
	}
	return out, rows.Err()
}

// AssigneesByIssuePKs loads all assignees for the given issue PKs in one query.
// Returns a map from issue_pk to the ordered slice of assignee user PKs.
func (s *Store) AssigneesByIssuePKs(ctx context.Context, issuePKs []int64) (map[int64][]int64, error) {
	if len(issuePKs) == 0 {
		return map[int64][]int64{}, nil
	}
	q := s.rebind(`SELECT issue_pk, user_pk FROM assignees
		WHERE issue_pk IN ` + inPlaceholders(len(issuePKs)) + `
		ORDER BY issue_pk, position, user_pk`)
	rows, err := s.rdb.QueryContext(ctx, q, i64Args(issuePKs)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64][]int64, len(issuePKs))
	for rows.Next() {
		var issuePK, userPK int64
		if err := rows.Scan(&issuePK, &userPK); err != nil {
			return nil, err
		}
		out[issuePK] = append(out[issuePK], userPK)
	}
	return out, rows.Err()
}

// ReactionRollupsBySubjectPKs loads reaction counts for multiple subjects of
// the same type in one query. Returns a map from subject_pk to its rollup.
func (s *Store) ReactionRollupsBySubjectPKs(ctx context.Context, subjectType string, subjectPKs []int64) (map[int64]ReactionRollup, error) {
	if len(subjectPKs) == 0 {
		return map[int64]ReactionRollup{}, nil
	}
	args := append([]any{subjectType}, i64Args(subjectPKs)...)
	q := s.rebind(`SELECT subject_pk, content, COUNT(*) FROM reactions
		WHERE subject_type = ? AND subject_pk IN ` + inPlaceholders(len(subjectPKs)) + `
		GROUP BY subject_pk, content`)
	rows, err := s.rdb.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64]ReactionRollup, len(subjectPKs))
	for rows.Next() {
		var subjectPK int64
		var content string
		var n int
		if err := rows.Scan(&subjectPK, &content, &n); err != nil {
			return nil, err
		}
		r := out[subjectPK]
		if r.Counts == nil {
			r.Counts = map[string]int{}
		}
		r.Counts[content] = n
		r.TotalCount += n
		out[subjectPK] = r
	}
	return out, rows.Err()
}

// MilestonesByPKs loads milestones by primary key in one round trip.
// PKs with no matching row are silently absent from the returned map.
func (s *Store) MilestonesByPKs(ctx context.Context, pks []int64) (map[int64]*MilestoneRow, error) {
	if len(pks) == 0 {
		return map[int64]*MilestoneRow{}, nil
	}
	q := s.rebind(`SELECT ` + milestoneColumns + ` FROM milestones
		WHERE pk IN ` + inPlaceholders(len(pks)))
	rows, err := s.rdb.QueryContext(ctx, q, i64Args(pks)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64]*MilestoneRow, len(pks))
	for rows.Next() {
		m, err := scanMilestoneRows(rows)
		if err != nil {
			return nil, err
		}
		out[m.PK] = m
	}
	return out, rows.Err()
}

// IssuesByPKs loads issues by primary key in one round trip.
// PKs with no live row are silently absent from the returned map.
func (s *Store) IssuesByPKs(ctx context.Context, pks []int64) (map[int64]*IssueRow, error) {
	if len(pks) == 0 {
		return map[int64]*IssueRow{}, nil
	}
	q := s.rebind(`SELECT ` + issueColumns + ` FROM issues
		WHERE pk IN ` + inPlaceholders(len(pks)) + ` AND deleted_at IS NULL`)
	rows, err := s.rdb.QueryContext(ctx, q, i64Args(pks)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64]*IssueRow, len(pks))
	for rows.Next() {
		iss, err := scanIssueRows(rows)
		if err != nil {
			return nil, err
		}
		out[iss.PK] = iss
	}
	return out, rows.Err()
}

// ListCheckRunsForSuites loads all check runs for any of the given suite PKs in
// one query. Returns a map from suite_pk to its ordered slice of check runs.
func (s *Store) ListCheckRunsForSuites(ctx context.Context, suitePKs []int64) (map[int64][]CheckRunRow, error) {
	if len(suitePKs) == 0 {
		return map[int64][]CheckRunRow{}, nil
	}
	q := s.rebind(`SELECT ` + checkRunColumns + ` FROM check_runs
		WHERE suite_pk IN ` + inPlaceholders(len(suitePKs)) + ` ORDER BY suite_pk, pk`)
	rows, err := s.rdb.QueryContext(ctx, q, i64Args(suitePKs)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64][]CheckRunRow, len(suitePKs))
	for rows.Next() {
		r, err := scanCheckRunRows(rows)
		if err != nil {
			return nil, err
		}
		out[r.SuitePK] = append(out[r.SuitePK], *r)
	}
	return out, rows.Err()
}

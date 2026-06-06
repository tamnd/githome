package store

import (
	"context"
	"strings"
)

// buildFTSMatch converts search terms to an FTS5 MATCH expression. Each term
// is double-quoted so it is treated as a literal phrase token rather than a
// prefix or boolean operator. Terms are space-separated, which FTS5 interprets
// as implicit AND so all terms must appear in the document.
func buildFTSMatch(terms []string) string {
	quoted := make([]string, len(terms))
	for i, t := range terms {
		quoted[i] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
	}
	return strings.Join(quoted, " ")
}

// issueSearchColumns is issueColumns qualified with the `i` alias, since the
// search query joins issues to repositories (for visibility and the repo:
// qualifier) and to users (for the owner login behind user:/org:). The scan
// order matches issueColumns so scanIssueRows reads it unchanged.
const issueSearchColumns = `i.pk, i.db_id, i.repo_pk, i.number, i.is_pull, i.title, i.body,
	i.user_pk, i.state, i.state_reason, i.milestone_pk, i.locked, i.active_lock_reason,
	i.comments_count, i.closed_at, i.closed_by_pk, i.lock_version, i.created_at, i.updated_at`

// IssueSearch is a resolved cross-repository issue query. The domain fills it
// from the parsed qualifiers: logins and repo names are already resolved to the
// internal pks here, so the store only filters. ViewerPK gates visibility: a
// row is returned only when its repository is public or owned by the viewer, so
// search never leaks a private repository. A nil pointer or empty slice means
// the corresponding filter is not applied; the term slice is ANDed, each term
// matched case-insensitively against the selected fields.
type IssueSearch struct {
	ViewerPK    int64
	Terms       []string
	MatchTitle  bool
	MatchBody   bool
	IsPull      *bool
	State       string // "", "open", "closed"
	AuthorPK    *int64
	AssigneePK  *int64
	Labels      []string
	RepoPKs     []int64
	OwnerPKs    []int64
	MilestonePK *int64
	Sort        string // "created" | "updated" | "comments"; "" is created
	Order       string // "asc" | "desc"; "" is desc
	Limit       int
	Offset      int
}

func (q IssueSearch) where() (string, []any) {
	var b strings.Builder
	args := []any{}
	b.WriteString(` WHERE i.deleted_at IS NULL AND r.deleted_at IS NULL`)
	b.WriteString(` AND (r.private = ? OR r.owner_pk = ?)`)
	args = append(args, false, q.ViewerPK)

	if q.IsPull != nil {
		b.WriteString(` AND i.is_pull = ?`)
		args = append(args, *q.IsPull)
	}
	switch q.State {
	case "open":
		b.WriteString(` AND i.state = 'open'`)
	case "closed":
		b.WriteString(` AND i.state = 'closed'`)
	}
	if q.AuthorPK != nil {
		b.WriteString(` AND i.user_pk = ?`)
		args = append(args, *q.AuthorPK)
	}
	if q.AssigneePK != nil {
		b.WriteString(` AND EXISTS (SELECT 1 FROM assignees a
			WHERE a.issue_pk = i.pk AND a.user_pk = ?)`)
		args = append(args, *q.AssigneePK)
	}
	if frag, fargs := inClause("i.repo_pk", q.RepoPKs); frag != "" {
		b.WriteString(frag)
		args = append(args, fargs...)
	}
	if frag, fargs := inClause("r.owner_pk", q.OwnerPKs); frag != "" {
		b.WriteString(frag)
		args = append(args, fargs...)
	}
	if q.MilestonePK != nil {
		b.WriteString(` AND i.milestone_pk = ?`)
		args = append(args, *q.MilestonePK)
	}
	for _, name := range q.Labels {
		b.WriteString(` AND EXISTS (SELECT 1 FROM issue_labels il
			JOIN labels l ON l.pk = il.label_pk
			WHERE il.issue_pk = i.pk AND lower(l.name) = lower(?))`)
		args = append(args, name)
	}
	return b.String(), args
}

func (q IssueSearch) orderBy() string {
	col := "i.created_at"
	switch q.Sort {
	case "updated":
		col = "i.updated_at"
	case "comments":
		col = "i.comments_count"
	}
	dir := "DESC"
	if strings.EqualFold(q.Order, "asc") {
		dir = "ASC"
	}
	// db_id is the stable tie-breaker so equal sort keys order deterministically
	// across repositories, where number alone would collide.
	return ` ORDER BY ` + col + ` ` + dir + `, i.db_id ` + dir
}

// issueTermClause returns the SQL fragment and args that filter issues by the
// query's Terms field. For title+body searches (the common case) it uses the
// dialect's FTS index; for title-only or body-only it falls back to LIKE so
// the more specific in-field constraint is preserved.
func (s *Store) issueTermClause(q IssueSearch) (string, []any) {
	if len(q.Terms) == 0 {
		return "", nil
	}
	titleOnly := q.MatchTitle && !q.MatchBody
	bodyOnly := q.MatchBody && !q.MatchTitle
	if !titleOnly && !bodyOnly {
		// Both fields (or neither, treated as both): use FTS.
		switch s.dialect {
		case DialectSQLite:
			return ` AND i.pk IN (SELECT rowid FROM issues_fts WHERE issues_fts MATCH ?)`,
				[]any{buildFTSMatch(q.Terms)}
		case DialectPostgres:
			return ` AND i.search_vector @@ plainto_tsquery('simple', ?)`,
				[]any{strings.Join(q.Terms, " ")}
		}
	}
	// Title-only, body-only, or unknown dialect: LIKE.
	var b strings.Builder
	var args []any
	for _, t := range q.Terms {
		like := "%" + strings.ToLower(t) + "%"
		switch {
		case titleOnly:
			b.WriteString(` AND lower(i.title) LIKE ?`)
			args = append(args, like)
		case bodyOnly:
			b.WriteString(` AND lower(COALESCE(i.body, '')) LIKE ?`)
			args = append(args, like)
		default:
			b.WriteString(` AND (lower(i.title) LIKE ? OR lower(COALESCE(i.body, '')) LIKE ?)`)
			args = append(args, like, like)
		}
	}
	return b.String(), args
}

// SearchIssues returns the page of issues and pull requests matching the query,
// joined across every repository the viewer may see.
func (s *Store) SearchIssues(ctx context.Context, q IssueSearch) ([]IssueRow, error) {
	where, args := q.where()
	termSQL, termArgs := s.issueTermClause(q)
	where += termSQL
	args = append(args, termArgs...)
	limit := q.Limit
	if limit <= 0 {
		limit = 30
	}
	args = append(args, limit, q.Offset)
	sql := s.rebind(`SELECT ` + issueSearchColumns + ` FROM issues i
		JOIN repositories r ON r.pk = i.repo_pk` + where + q.orderBy() + ` LIMIT ? OFFSET ?`)
	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []IssueRow
	for rows.Next() {
		iss, err := scanIssueRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *iss)
	}
	return out, rows.Err()
}

// CountSearchIssues counts every issue matching the query, ignoring its page
// window, for the search envelope's total_count.
func (s *Store) CountSearchIssues(ctx context.Context, q IssueSearch) (int, error) {
	where, args := q.where()
	termSQL, termArgs := s.issueTermClause(q)
	where += termSQL
	args = append(args, termArgs...)
	sql := s.rebind(`SELECT COUNT(*) FROM issues i
		JOIN repositories r ON r.pk = i.repo_pk` + where)
	var n int
	if err := s.db.QueryRowContext(ctx, sql, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// RepoSearch is a resolved cross-repository repository query. As with
// IssueSearch the domain resolves user:/org: to owner pks before building it,
// and ViewerPK gates visibility to public repositories and the viewer's own.
type RepoSearch struct {
	ViewerPK int64
	Terms    []string
	OwnerPKs []int64
	Fork     *bool
	Sort     string // "updated" | "created"; "" is created
	Order    string // "asc" | "desc"; "" is desc
	Limit    int
	Offset   int
}

func (q RepoSearch) where() (string, []any) {
	var b strings.Builder
	args := []any{}
	b.WriteString(` WHERE r.deleted_at IS NULL`)
	b.WriteString(` AND (r.private = ? OR r.owner_pk = ?)`)
	args = append(args, false, q.ViewerPK)

	if frag, fargs := inClause("r.owner_pk", q.OwnerPKs); frag != "" {
		b.WriteString(frag)
		args = append(args, fargs...)
	}
	if q.Fork != nil {
		b.WriteString(` AND r.fork = ?`)
		args = append(args, *q.Fork)
	}
	return b.String(), args
}

func (q RepoSearch) orderBy() string {
	col := "r.created_at"
	switch q.Sort {
	case "updated":
		col = "r.updated_at"
	case "pushed":
		col = "r.pushed_at"
	}
	dir := "DESC"
	if strings.EqualFold(q.Order, "asc") {
		dir = "ASC"
	}
	return ` ORDER BY ` + col + ` ` + dir + `, r.db_id ` + dir
}

// repoTermClause returns the SQL fragment and args for the repository term
// filter. Always uses FTS for both name+description (the only case); LIKE
// fallback is retained for safety on unknown dialects.
func (s *Store) repoTermClause(q RepoSearch) (string, []any) {
	if len(q.Terms) == 0 {
		return "", nil
	}
	switch s.dialect {
	case DialectSQLite:
		return ` AND r.pk IN (SELECT rowid FROM repos_fts WHERE repos_fts MATCH ?)`,
			[]any{buildFTSMatch(q.Terms)}
	case DialectPostgres:
		return ` AND r.search_vector @@ plainto_tsquery('simple', ?)`,
			[]any{strings.Join(q.Terms, " ")}
	default:
		var b strings.Builder
		var args []any
		for _, t := range q.Terms {
			like := "%" + strings.ToLower(t) + "%"
			b.WriteString(` AND (lower(r.name) LIKE ? OR lower(COALESCE(r.description, '')) LIKE ?)`)
			args = append(args, like, like)
		}
		return b.String(), args
	}
}

// SearchRepositories returns the page of repositories matching the query.
func (s *Store) SearchRepositories(ctx context.Context, q RepoSearch) ([]RepoRow, error) {
	where, args := q.where()
	termSQL, termArgs := s.repoTermClause(q)
	where += termSQL
	args = append(args, termArgs...)
	limit := q.Limit
	if limit <= 0 {
		limit = 30
	}
	args = append(args, limit, q.Offset)
	sql := s.rebind(`SELECT ` + repoColumns + ` FROM repositories r` + where + q.orderBy() + ` LIMIT ? OFFSET ?`)
	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RepoRow
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// CountSearchRepositories counts every repository matching the query for the
// envelope's total_count.
func (s *Store) CountSearchRepositories(ctx context.Context, q RepoSearch) (int, error) {
	where, args := q.where()
	termSQL, termArgs := s.repoTermClause(q)
	where += termSQL
	args = append(args, termArgs...)
	sql := s.rebind(`SELECT COUNT(*) FROM repositories r` + where)
	var n int
	if err := s.db.QueryRowContext(ctx, sql, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// VisibleRepoPKs lists the internal pks of every repository the viewer may see
// among the given owners, deleted rows excluded. Code search uses it to scope a
// git tree walk to the repositories a user:/org: qualifier selects. An empty
// owners slice lists every visible repository.
func (s *Store) VisibleRepoPKs(ctx context.Context, viewerPK int64, ownerPKs []int64) ([]int64, error) {
	var b strings.Builder
	b.WriteString(`SELECT r.pk FROM repositories r
		WHERE r.deleted_at IS NULL AND (r.private = ? OR r.owner_pk = ?)`)
	args := []any{false, viewerPK}
	if frag, fargs := inClause("r.owner_pk", ownerPKs); frag != "" {
		b.WriteString(frag)
		args = append(args, fargs...)
	}
	b.WriteString(` ORDER BY r.db_id`)
	rows, err := s.db.QueryContext(ctx, s.rebind(b.String()), args...)
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

// inClause builds an " AND col IN (?, ?, ...)" fragment and its args for a
// non-empty id set, returning empty strings when the set is empty so the caller
// adds no predicate.
func inClause(col string, ids []int64) (string, []any) {
	if len(ids) == 0 {
		return "", nil
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	return ` AND ` + col + ` IN (` + strings.Join(ph, ", ") + `)`, args
}

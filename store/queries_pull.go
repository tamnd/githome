package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// The pull request store. A pull request is an issue row plus the extension row
// here, so the create path writes both inside one transaction (InsertPull, run
// after the issue InsertIssue), and the read paths join the two. The lookups
// resolve a pull request three ways the service needs: by issue pk (the natural
// key, since the issue holds the number), by db_id (a node id decodes to it),
// and as a repository page. The mergeability worker writes back through
// SetMergeability, and the merge handler through MarkMerged.

const pullColumns = `pk, db_id, issue_pk, repo_pk, base_ref, base_sha, head_ref,
	head_sha, head_repo_pk, draft, maintainer_can_modify, merged, merged_at,
	merged_by_pk, merge_commit_sha, mergeable, mergeable_state, rebaseable,
	additions, deletions, changed_files, commits_count, mergeability_checked_at,
	created_at, updated_at`

// GetPullByIssuePK resolves the pull request extension of an issue row.
func (s *Store) GetPullByIssuePK(ctx context.Context, issuePK int64) (*PullRow, error) {
	q := s.rebind(`SELECT ` + pullColumns + ` FROM pull_requests WHERE issue_pk = ?`)
	return scanPull(s.rdb.QueryRowContext(ctx, q, issuePK))
}

// GetPullByDBID resolves a pull request by its public database id, the value a
// PullRequest node id decodes to.
func (s *Store) GetPullByDBID(ctx context.Context, dbID int64) (*PullRow, error) {
	q := s.rebind(`SELECT ` + pullColumns + ` FROM pull_requests WHERE db_id = ?`)
	return scanPull(s.rdb.QueryRowContext(ctx, q, dbID))
}

// PullFilter narrows the pull list queries. State is "open", "closed", or
// "all" (the empty string lists open ones). HeadRef and BaseRef match the
// branch names on either end; HeadOwner additionally pins the head to a
// repository owned by that login, the "owner:branch" head form. Sort picks
// created (the default, served by the number index), updated, popularity
// (comment count), or long-running (creation age); Direction is asc or desc,
// defaulting to desc.
type PullFilter struct {
	State     string
	HeadOwner string
	HeadRef   string
	BaseRef   string
	Sort      string
	Direction string
}

// pullListWhere builds the WHERE tail of the pull list queries plus the args
// that follow the leading repo_pk argument the caller binds first.
func pullListWhere(f PullFilter) (string, []any) {
	var args []any
	where := ` WHERE i.repo_pk = ? AND i.deleted_at IS NULL`
	switch f.State {
	case "", "open":
		where += ` AND i.state = 'open'`
	case "closed":
		where += ` AND i.state = 'closed'`
	case "all":
		// no state predicate
	}
	if f.HeadRef != "" {
		where += ` AND pr.head_ref = ?`
		args = append(args, f.HeadRef)
	}
	if f.HeadOwner != "" {
		// A same-repository pull request leaves head_repo_pk NULL, so the
		// owner check falls back to the owner of the repository being listed.
		where += ` AND (pr.head_repo_pk IN (SELECT r.pk FROM repositories r
			JOIN users u ON u.pk = r.owner_pk WHERE u.login = ?)
			OR (pr.head_repo_pk IS NULL AND EXISTS (SELECT 1 FROM repositories r2
			JOIN users u2 ON u2.pk = r2.owner_pk WHERE r2.pk = i.repo_pk AND u2.login = ?)))`
		args = append(args, f.HeadOwner, f.HeadOwner)
	}
	if f.BaseRef != "" {
		where += ` AND pr.base_ref = ?`
		args = append(args, f.BaseRef)
	}
	return where, args
}

// pullListOrder maps the sort and direction to the ORDER BY clause. The
// default creation order rides the issues (repo_pk, number) unique index with
// no sort step; the named sorts order on the issue column that backs them,
// with the number as the tiebreak. long-running approximates GitHub's order by
// creation age; the activity-window narrowing is not modeled.
func pullListOrder(f PullFilter) string {
	dir := " DESC"
	if strings.EqualFold(f.Direction, "asc") {
		dir = " ASC"
	}
	switch f.Sort {
	case "updated":
		return ` ORDER BY i.updated_at` + dir + `, i.number` + dir
	case "popularity":
		return ` ORDER BY i.comments_count` + dir + `, i.number` + dir
	case "long-running":
		return ` ORDER BY i.created_at` + dir + `, i.number` + dir
	default:
		return ` ORDER BY i.number` + dir
	}
}

// ListPulls returns a repository's pull requests matching the filter, paged.
// It joins the issues table so the state filter and the orderings read from
// the row that owns them. The repository filter goes on i.repo_pk (identical
// to pr.repo_pk by construction): that way the issues (repo_pk, number)
// unique index serves both the filter and the default ORDER BY, with no sort
// step.
func (s *Store) ListPulls(ctx context.Context, repoPK int64, f PullFilter, limit, offset int) ([]PullRow, error) {
	if limit <= 0 {
		limit = 30
	}
	where, extra := pullListWhere(f)
	q := s.rebind(`SELECT ` + pullPrefixed + ` FROM pull_requests pr
		JOIN issues i ON i.pk = pr.issue_pk` + where +
		pullListOrder(f) + ` LIMIT ? OFFSET ?`)
	args := append(append([]any{repoPK}, extra...), limit, offset)
	rows, err := s.rdb.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PullRow
	for rows.Next() {
		p, err := scanPullRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// ListPullsPage serves a keyset-paginated pull-request page and reports whether
// a further page exists, without a COUNT. The list orders by number descending,
// so a cursor seeks number < cursor.Number, served by the issues
// (repo_pk, number) unique index in one step regardless of page depth; the
// repository filter sits on i.repo_pk so the planner can use that index. It fetches one row beyond
// the page and uses its presence as the has-next signal, so a list request on a
// repo with hundreds of thousands of pulls costs the page, not a full count plus
// a deep OFFSET scan. A nil cursor starts from the highest number. The filter's
// Sort and Direction are ignored: the seek key is the number order, and the
// handler only routes default-ordered walks here.
func (s *Store) ListPullsPage(ctx context.Context, repoPK int64, f PullFilter, cursor *PullCursor, limit int) ([]PullRow, bool, error) {
	if limit <= 0 {
		limit = 30
	}
	where, extra := pullListWhere(f)
	args := append([]any{repoPK}, extra...)
	if cursor != nil {
		where += ` AND i.number < ?`
		args = append(args, cursor.Number)
	}
	args = append(args, limit+1)
	q := s.rebind(`SELECT ` + pullPrefixed + ` FROM pull_requests pr
		JOIN issues i ON i.pk = pr.issue_pk` + where + `
		ORDER BY i.number DESC LIMIT ?`)
	rows, err := s.rdb.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	var out []PullRow
	for rows.Next() {
		p, err := scanPullRows(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

// CountPulls counts a repository's pull requests matching the filter.
func (s *Store) CountPulls(ctx context.Context, repoPK int64, f PullFilter) (int, error) {
	where, extra := pullListWhere(f)
	q := s.rebind(`SELECT COUNT(*) FROM pull_requests pr
		JOIN issues i ON i.pk = pr.issue_pk` + where)
	var n int
	args := append([]any{repoPK}, extra...)
	if err := s.rdb.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// PullListVersion returns the count and the latest updated_at marker of the
// pull requests matching the filter. It is the version seed for the
// pulls list ETag, the same shape as IssueListVersion. The marker covers both
// the issue row and the pull row: head pushes and mergeability recomputes
// bump pull_requests.updated_at without touching the issue, and both rows
// feed the rendered body. Markers are raw column text in one timestamp
// layout, so the larger of the two compares lexicographically.
func (s *Store) PullListVersion(ctx context.Context, repoPK int64, f PullFilter) (int, string, error) {
	where, extra := pullListWhere(f)
	q := s.rebind(`SELECT COUNT(*),
			COALESCE(MAX(i.updated_at), ''), COALESCE(MAX(pr.updated_at), '')
		FROM pull_requests pr
		JOIN issues i ON i.pk = pr.issue_pk` + where)
	var (
		n                   int
		issMarker, prMarker string
	)
	args := append([]any{repoPK}, extra...)
	if err := s.rdb.QueryRowContext(ctx, q, args...).Scan(&n, &issMarker, &prMarker); err != nil {
		return 0, "", err
	}
	marker := issMarker
	if prMarker > marker {
		marker = prMarker
	}
	return n, marker, nil
}

// OpenPullsByHeadRef returns the open pull requests in a repository whose head
// branch is the given short ref name, the set the post-receive sink refreshes
// and re-checks when that branch moves.
func (s *Store) OpenPullsByHeadRef(ctx context.Context, repoPK int64, headRef string) ([]PullRow, error) {
	q := s.rebind(`SELECT ` + pullColumns + ` FROM pull_requests
		WHERE repo_pk = ? AND head_ref = ? AND merged = ?`)
	rows, err := s.rdb.QueryContext(ctx, q, repoPK, headRef, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PullRow
	for rows.Next() {
		p, err := scanPullRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// OpenPullsByBaseRef returns the open pull requests in a repository that target
// the given base branch, the set whose behind count and mergeability the
// post-receive sink re-checks when that branch moves.
func (s *Store) OpenPullsByBaseRef(ctx context.Context, repoPK int64, baseRef string) ([]PullRow, error) {
	q := s.rebind(`SELECT ` + pullColumns + ` FROM pull_requests
		WHERE repo_pk = ? AND base_ref = ? AND merged = ?`)
	rows, err := s.rdb.QueryContext(ctx, q, repoPK, baseRef, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PullRow
	for rows.Next() {
		p, err := scanPullRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// OpenPullsByHeadSHA returns the open pull requests in a repository whose head
// currently points at the given sha, the set a status or check report against
// that sha refreshes the rollup of.
func (s *Store) OpenPullsByHeadSHA(ctx context.Context, repoPK int64, headSHA string) ([]PullRow, error) {
	q := s.rebind(`SELECT ` + pullColumns + ` FROM pull_requests
		WHERE repo_pk = ? AND head_sha = ? AND merged = ?`)
	rows, err := s.rdb.QueryContext(ctx, q, repoPK, headSHA, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PullRow
	for rows.Next() {
		p, err := scanPullRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// PullNumberByPK resolves the issue number that backs a pull request, addressed
// by the pull extension's pk. The standalone review-comment lookup needs it to
// build the comment's pull request urls without the number in the request path.
func (s *Store) PullNumberByPK(ctx context.Context, pullPK int64) (int64, error) {
	q := s.rebind(`SELECT i.number FROM pull_requests pr
		JOIN issues i ON i.pk = pr.issue_pk WHERE pr.pk = ?`)
	var number int64
	if err := s.rdb.QueryRowContext(ctx, q, pullPK).Scan(&number); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return number, nil
}

// pullPrefixed is pullColumns with a pr. prefix for the joined list queries.
const pullPrefixed = `pr.pk, pr.db_id, pr.issue_pk, pr.repo_pk, pr.base_ref,
	pr.base_sha, pr.head_ref, pr.head_sha, pr.head_repo_pk, pr.draft,
	pr.maintainer_can_modify, pr.merged, pr.merged_at, pr.merged_by_pk,
	pr.merge_commit_sha, pr.mergeable, pr.mergeable_state, pr.rebaseable,
	pr.additions, pr.deletions, pr.changed_files, pr.commits_count,
	pr.mergeability_checked_at, pr.created_at, pr.updated_at`

// InsertPull writes the pull request extension row with a freshly allocated
// db_id, filling the server-assigned fields back onto p. It runs inside the same
// transaction as the issue insert it extends, so a pull request and its issue
// row commit together.
func (t *Tx) InsertPull(ctx context.Context, p *PullRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	if p.MergeableState == "" {
		p.MergeableState = "unknown"
	}
	q := t.rebind(`INSERT INTO pull_requests
		(db_id, issue_pk, repo_pk, base_ref, base_sha, head_ref, head_sha,
		 head_repo_pk, draft, maintainer_can_modify, mergeable_state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id, created_at, updated_at`)
	var created, upd nullTime
	err = t.tx.QueryRowContext(ctx, q,
		dbID, p.IssuePK, p.RepoPK, p.BaseRef, p.BaseSHA, p.HeadRef, p.HeadSHA,
		argI64(p.HeadRepoPK), p.Draft, p.MaintainerCanModify, p.MergeableState,
	).Scan(&p.PK, &p.DBID, &created, &upd)
	if err != nil {
		return err
	}
	p.CreatedAt, p.UpdatedAt = created.Time, upd.Time
	return nil
}

// SetMergeability writes the derived merge state the worker computed: the
// tri-state mergeable, the mergeable_state string, the rebaseable flag, the diff
// stats, and the staleness stamp. A nil mergeable writes SQL NULL, the
// not-yet-computed value the read path surfaces as UNKNOWN.
func (s *Store) SetMergeability(ctx context.Context, issuePK int64, mergeable *bool, state string, rebaseable *bool, additions, deletions, changedFiles, commits int, checkedAt time.Time) error {
	q := s.rebind(`UPDATE pull_requests SET
		mergeable = ?, mergeable_state = ?, rebaseable = ?,
		additions = ?, deletions = ?, changed_files = ?, commits_count = ?,
		mergeability_checked_at = ?, updated_at = ?
		WHERE issue_pk = ?`)
	_, err := s.db.ExecContext(ctx, q,
		argBool(mergeable), state, argBool(rebaseable),
		additions, deletions, changedFiles, commits,
		checkedAt, checkedAt, issuePK)
	return err
}

// UpdatePullDraft flips the draft flag on a pull request.
func (s *Store) UpdatePullDraft(ctx context.Context, pullPK int64, draft bool) error {
	q := s.rebind(`UPDATE pull_requests SET draft = ?, updated_at = ? WHERE pk = ?`)
	res, err := s.db.ExecContext(ctx, q, draft, nowUTC(), pullPK)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdatePullMeta updates the editable metadata of a pull request row: the base
// branch, draft flag, and maintainer-can-modify flag. The merge state stays
// intact; callers that change the base branch must enqueue a recompute
// separately.
func (t *Tx) UpdatePullMeta(ctx context.Context, pullPK int64, baseRef string, draft, maintainerCanModify bool) error {
	q := t.rebind(`UPDATE pull_requests SET base_ref = ?, draft = ?, maintainer_can_modify = ?, updated_at = ? WHERE pk = ?`)
	_, err := t.tx.ExecContext(ctx, q, baseRef, draft, maintainerCanModify, nowUTC(), pullPK)
	return err
}

// UpdatePullHead repoints a pull request's head sha after a push to its head
// branch and clears the mergeability stamp so the next read treats the cached
// merge state as stale.
func (t *Tx) UpdatePullHead(ctx context.Context, pullPK int64, headSHA string) error {
	q := t.rebind(`UPDATE pull_requests SET
		head_sha = ?, mergeable = NULL, mergeable_state = 'unknown',
		mergeability_checked_at = NULL, updated_at = ?
		WHERE pk = ?`)
	_, err := t.tx.ExecContext(ctx, q, headSHA, nowUTC(), pullPK)
	return err
}

// MarkMerged records a successful merge: the merged flag, the instant, the
// merger, and the merge commit sha. It runs in the same transaction as the
// issue close so a merged pull request is also a closed issue.
func (t *Tx) MarkMerged(ctx context.Context, pullPK int64, mergerPK int64, mergeCommitSHA string, mergedAt time.Time) error {
	q := t.rebind(`UPDATE pull_requests SET
		merged = ?, merged_at = ?, merged_by_pk = ?, merge_commit_sha = ?,
		mergeable_state = 'unknown', updated_at = ?
		WHERE pk = ?`)
	_, err := t.tx.ExecContext(ctx, q, true, mergedAt, mergerPK, mergeCommitSHA, nowUTC(), pullPK)
	return err
}

func scanPull(row interface{ Scan(...any) error }) (*PullRow, error) {
	p, err := scanPullRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

func scanPullRows(row interface{ Scan(...any) error }) (*PullRow, error) {
	var (
		p              PullRow
		headRepoPK     sql.NullInt64
		mergedAt       nullTime
		mergedByPK     sql.NullInt64
		mergeCommitSHA sql.NullString
		mergeable      boolVal
		rebaseable     boolVal
		draft, merged  boolVal
		maintainer     boolVal
		checkedAt      nullTime
		created, upd   nullTime
	)
	if err := row.Scan(&p.PK, &p.DBID, &p.IssuePK, &p.RepoPK, &p.BaseRef, &p.BaseSHA,
		&p.HeadRef, &p.HeadSHA, &headRepoPK, &draft, &maintainer, &merged, &mergedAt,
		&mergedByPK, &mergeCommitSHA, &mergeable, &p.MergeableState, &rebaseable,
		&p.Additions, &p.Deletions, &p.ChangedFiles, &p.CommitsCount, &checkedAt,
		&created, &upd); err != nil {
		return nil, err
	}
	p.HeadRepoPK = i64Ptr(headRepoPK)
	p.Draft = draft.Bool
	p.MaintainerCanModify = maintainer.Bool
	p.Merged = merged.Bool
	p.MergedAt = mergedAt.ptr()
	p.MergedByPK = i64Ptr(mergedByPK)
	p.MergeCommitSHA = strPtr(mergeCommitSHA)
	p.Mergeable = mergeable.ptr()
	p.Rebaseable = rebaseable.ptr()
	p.MergeabilityCheckedAt = checkedAt.ptr()
	p.CreatedAt, p.UpdatedAt = created.Time, upd.Time
	return &p, nil
}

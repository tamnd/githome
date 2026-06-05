package store

import (
	"context"
	"database/sql"
	"errors"
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
	return scanPull(s.db.QueryRowContext(ctx, q, issuePK))
}

// GetPullByDBID resolves a pull request by its public database id, the value a
// PullRequest node id decodes to.
func (s *Store) GetPullByDBID(ctx context.Context, dbID int64) (*PullRow, error) {
	q := s.rebind(`SELECT ` + pullColumns + ` FROM pull_requests WHERE db_id = ?`)
	return scanPull(s.db.QueryRowContext(ctx, q, dbID))
}

// ListPulls returns a repository's pull requests, newest number first, paged.
// state is "open", "closed", or "all"; the empty string lists open ones. It
// joins the issues table so the state filter and the number ordering read from
// the row that owns them.
func (s *Store) ListPulls(ctx context.Context, repoPK int64, state string, limit, offset int) ([]PullRow, error) {
	if limit <= 0 {
		limit = 30
	}
	where := ` WHERE pr.repo_pk = ? AND i.deleted_at IS NULL`
	switch state {
	case "", "open":
		where += ` AND i.state = 'open'`
	case "closed":
		where += ` AND i.state = 'closed'`
	case "all":
		// no state predicate
	}
	q := s.rebind(`SELECT ` + pullPrefixed + ` FROM pull_requests pr
		JOIN issues i ON i.pk = pr.issue_pk` + where + `
		ORDER BY i.number DESC LIMIT ? OFFSET ?`)
	rows, err := s.db.QueryContext(ctx, q, repoPK, limit, offset)
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

// CountPulls counts a repository's pull requests matching the state filter.
func (s *Store) CountPulls(ctx context.Context, repoPK int64, state string) (int, error) {
	where := ` WHERE pr.repo_pk = ? AND i.deleted_at IS NULL`
	switch state {
	case "", "open":
		where += ` AND i.state = 'open'`
	case "closed":
		where += ` AND i.state = 'closed'`
	case "all":
	}
	q := s.rebind(`SELECT COUNT(*) FROM pull_requests pr
		JOIN issues i ON i.pk = pr.issue_pk` + where)
	var n int
	if err := s.db.QueryRowContext(ctx, q, repoPK).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// OpenPullsByHeadRef returns the open pull requests in a repository whose head
// branch is the given short ref name, the set the post-receive sink refreshes
// and re-checks when that branch moves.
func (s *Store) OpenPullsByHeadRef(ctx context.Context, repoPK int64, headRef string) ([]PullRow, error) {
	q := s.rebind(`SELECT ` + pullColumns + ` FROM pull_requests
		WHERE repo_pk = ? AND head_ref = ? AND merged = ?`)
	rows, err := s.db.QueryContext(ctx, q, repoPK, headRef, false)
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
	rows, err := s.db.QueryContext(ctx, q, repoPK, baseRef, false)
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
	rows, err := s.db.QueryContext(ctx, q, repoPK, headSHA, false)
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
	if err := s.db.QueryRowContext(ctx, q, pullPK).Scan(&number); err != nil {
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

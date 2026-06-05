package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// IssueRow is a row of the issues table. The table is shared by issues and pull
// requests (IsPull tells them apart) since GitHub numbers them from one
// sequence per repository, but M4 only writes IsPull=false rows; the pull
// request milestone fills in the rest. StateReason carries GitHub's
// completed/reopened/not_planned qualifier and is nil for open issues that have
// never been closed.
type IssueRow struct {
	PK               int64
	DBID             int64
	RepoPK           int64
	Number           int64
	IsPull           bool
	Title            string
	Body             *string
	UserPK           int64
	State            string
	StateReason      *string
	MilestonePK      *int64
	Locked           bool
	ActiveLockReason *string
	CommentsCount    int
	ClosedAt         *time.Time
	ClosedByPK       *int64
	LockVersion      int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

const issueColumns = `pk, db_id, repo_pk, number, is_pull, title, body, user_pk, state,
	state_reason, milestone_pk, locked, active_lock_reason, comments_count,
	closed_at, closed_by_pk, lock_version, created_at, updated_at`

// IssueFilter narrows a repository's issue list the way GitHub's list endpoint
// does. The zero value lists open issues, newest first, excluding pull requests.
// Empty slice/zero fields mean "do not filter on this".
type IssueFilter struct {
	State        string   // "open" | "closed" | "all"; "" means "open"
	Labels       []string // issue must carry every named label
	CreatorPK    *int64
	AssigneePK   *int64 // 0-valued pointer not used; nil means unfiltered
	MilestonePK  *int64
	IncludePulls bool   // when false, is_pull rows are excluded
	Sort         string // "created" | "updated" | "comments"; "" means "created"
	Direction    string // "asc" | "desc"; "" means "desc"
	Limit        int    // 0 means the default page of 30
	Offset       int
}

// GetIssueByNumber resolves an issue by its per-repo number, skipping soft
// deleted rows.
func (s *Store) GetIssueByNumber(ctx context.Context, repoPK, number int64) (*IssueRow, error) {
	q := s.rebind(`SELECT ` + issueColumns + ` FROM issues
		WHERE repo_pk = ? AND number = ? AND deleted_at IS NULL`)
	return scanIssue(s.db.QueryRowContext(ctx, q, repoPK, number))
}

// GetIssueByDBID resolves an issue by its public database id.
func (s *Store) GetIssueByDBID(ctx context.Context, dbID int64) (*IssueRow, error) {
	q := s.rebind(`SELECT ` + issueColumns + ` FROM issues
		WHERE db_id = ? AND deleted_at IS NULL`)
	return scanIssue(s.db.QueryRowContext(ctx, q, dbID))
}

// GetIssueByPK resolves an issue by primary key.
func (s *Store) GetIssueByPK(ctx context.Context, pk int64) (*IssueRow, error) {
	q := s.rebind(`SELECT ` + issueColumns + ` FROM issues
		WHERE pk = ? AND deleted_at IS NULL`)
	return scanIssue(s.db.QueryRowContext(ctx, q, pk))
}

// issueListQuery builds the shared WHERE/ORDER tail for ListIssues and
// CountIssues so the filtered set and its count never drift. It returns the SQL
// fragment after "FROM issues i" and the bound args, without the SELECT head or
// the LIMIT/OFFSET tail.
func (f IssueFilter) where() (string, []any) {
	var b strings.Builder
	args := []any{}
	b.WriteString(` WHERE i.repo_pk = ? AND i.deleted_at IS NULL`)

	switch f.State {
	case "", "open":
		b.WriteString(` AND i.state = 'open'`)
	case "closed":
		b.WriteString(` AND i.state = 'closed'`)
	case "all":
		// no state predicate
	}
	if !f.IncludePulls {
		b.WriteString(` AND i.is_pull = ?`)
		args = append(args, false)
	}
	if f.CreatorPK != nil {
		b.WriteString(` AND i.user_pk = ?`)
		args = append(args, *f.CreatorPK)
	}
	if f.MilestonePK != nil {
		b.WriteString(` AND i.milestone_pk = ?`)
		args = append(args, *f.MilestonePK)
	}
	if f.AssigneePK != nil {
		b.WriteString(` AND EXISTS (SELECT 1 FROM assignees a
			WHERE a.issue_pk = i.pk AND a.user_pk = ?)`)
		args = append(args, *f.AssigneePK)
	}
	for _, name := range f.Labels {
		b.WriteString(` AND EXISTS (SELECT 1 FROM issue_labels il
			JOIN labels l ON l.pk = il.label_pk
			WHERE il.issue_pk = i.pk AND lower(l.name) = lower(?))`)
		args = append(args, name)
	}
	return b.String(), args
}

func (f IssueFilter) orderBy() string {
	col := "i.created_at"
	switch f.Sort {
	case "updated":
		col = "i.updated_at"
	case "comments":
		col = "i.comments_count"
	}
	dir := "DESC"
	if strings.EqualFold(f.Direction, "asc") {
		dir = "ASC"
	}
	// number is the deterministic tie-breaker so equal timestamps order stably.
	return ` ORDER BY ` + col + ` ` + dir + `, i.number ` + dir
}

// ListIssues returns a repository's issues matching the filter, one page at a
// time.
func (s *Store) ListIssues(ctx context.Context, repoPK int64, f IssueFilter) ([]IssueRow, error) {
	where, args := f.where()
	limit := f.Limit
	if limit <= 0 {
		limit = 30
	}
	full := append([]any{repoPK}, args...)
	full = append(full, limit, f.Offset)
	q := s.rebind(`SELECT ` + issueColumns + ` FROM issues i` + where + f.orderBy() + ` LIMIT ? OFFSET ?`)
	rows, err := s.db.QueryContext(ctx, q, full...)
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

// CountIssues counts the issues matching the filter, ignoring its page window.
func (s *Store) CountIssues(ctx context.Context, repoPK int64, f IssueFilter) (int, error) {
	where, args := f.where()
	full := append([]any{repoPK}, args...)
	q := s.rebind(`SELECT COUNT(*) FROM issues i` + where)
	var n int
	if err := s.db.QueryRowContext(ctx, q, full...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func scanIssue(row interface{ Scan(...any) error }) (*IssueRow, error) {
	iss, err := scanIssueRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return iss, err
}

func scanIssueRows(row interface{ Scan(...any) error }) (*IssueRow, error) {
	var (
		iss          IssueRow
		body         sql.NullString
		stateReason  sql.NullString
		milestonePK  sql.NullInt64
		lockReason   sql.NullString
		closedByPK   sql.NullInt64
		locked, pull boolVal
		closedAt     nullTime
		created, upd nullTime
	)
	if err := row.Scan(&iss.PK, &iss.DBID, &iss.RepoPK, &iss.Number, &pull, &iss.Title, &body,
		&iss.UserPK, &iss.State, &stateReason, &milestonePK, &locked, &lockReason,
		&iss.CommentsCount, &closedAt, &closedByPK, &iss.LockVersion, &created, &upd); err != nil {
		return nil, err
	}
	iss.IsPull = pull.Bool
	iss.Body = strPtr(body)
	iss.StateReason = strPtr(stateReason)
	iss.MilestonePK = i64Ptr(milestonePK)
	iss.Locked = locked.Bool
	iss.ActiveLockReason = strPtr(lockReason)
	iss.ClosedAt = closedAt.ptr()
	iss.ClosedByPK = i64Ptr(closedByPK)
	iss.CreatedAt, iss.UpdatedAt = created.Time, upd.Time
	return &iss, nil
}

// --- Tx write paths: an issue create allocates a number, inserts the row, and
// attaches labels and assignees as one atomic unit. ---

// AllocIssueNumber atomically hands out the next per-repo issue/PR number from
// the shared counter, so an issue and a pull request never collide.
func (t *Tx) AllocIssueNumber(ctx context.Context, repoPK int64) (int64, error) {
	q := t.rebind(`UPDATE repositories SET next_issue_number = next_issue_number + 1
		WHERE pk = ? RETURNING next_issue_number - 1`)
	var n int64
	if err := t.tx.QueryRowContext(ctx, q, repoPK).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// InsertIssue writes the issue row with a freshly allocated db_id and the number
// the caller already allocated, filling the server-assigned fields back onto iss.
func (t *Tx) InsertIssue(ctx context.Context, iss *IssueRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	if iss.State == "" {
		iss.State = "open"
	}
	q := t.rebind(`INSERT INTO issues
		(db_id, repo_pk, number, is_pull, title, body, user_pk, state, milestone_pk)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id, comments_count, lock_version, created_at, updated_at`)
	var created, upd nullTime
	err = t.tx.QueryRowContext(ctx, q,
		dbID, iss.RepoPK, iss.Number, iss.IsPull, iss.Title, argStr(iss.Body),
		iss.UserPK, iss.State, argI64(iss.MilestonePK),
	).Scan(&iss.PK, &iss.DBID, &iss.CommentsCount, &iss.LockVersion, &created, &upd)
	if err != nil {
		return err
	}
	iss.CreatedAt, iss.UpdatedAt = created.Time, upd.Time
	return nil
}

// UpdateIssue writes the editable fields under an optimistic lock: the row is
// updated only if its lock_version still matches the one read, and lock_version
// is bumped. A no-row result means a concurrent writer moved first; the caller
// re-reads and retries. The close transition (state, closed_at, closed_by_pk,
// state_reason) is written here too.
func (t *Tx) UpdateIssue(ctx context.Context, iss *IssueRow) error {
	q := t.rebind(`UPDATE issues SET
		title = ?, body = ?, state = ?, state_reason = ?, milestone_pk = ?,
		locked = ?, active_lock_reason = ?, closed_at = ?, closed_by_pk = ?,
		lock_version = lock_version + 1, updated_at = ?
		WHERE pk = ? AND lock_version = ?
		RETURNING lock_version, updated_at`)
	var upd nullTime
	var newVersion int64
	err := t.tx.QueryRowContext(ctx, q,
		iss.Title, argStr(iss.Body), iss.State, argStr(iss.StateReason), argI64(iss.MilestonePK),
		iss.Locked, argStr(iss.ActiveLockReason), argTime(iss.ClosedAt), argI64(iss.ClosedByPK),
		nowUTC(), iss.PK, iss.LockVersion,
	).Scan(&newVersion, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrOptimisticLock
	}
	if err != nil {
		return err
	}
	iss.LockVersion = newVersion
	iss.UpdatedAt = upd.Time
	return nil
}

// ErrOptimisticLock signals that a row's version moved between read and write,
// so the caller should re-read and retry the edit.
var ErrOptimisticLock = errors.New("store: optimistic lock conflict")

// AttachLabels links the given labels to the issue, ignoring any already linked.
func (t *Tx) AttachLabels(ctx context.Context, issuePK int64, labelPKs []int64) error {
	for _, lp := range labelPKs {
		q := t.rebind(`INSERT INTO issue_labels (issue_pk, label_pk) VALUES (?, ?)
			ON CONFLICT (issue_pk, label_pk) DO NOTHING`)
		if _, err := t.tx.ExecContext(ctx, q, issuePK, lp); err != nil {
			return err
		}
	}
	return nil
}

// ReplaceLabels sets an issue's labels to exactly the given set.
func (t *Tx) ReplaceLabels(ctx context.Context, issuePK int64, labelPKs []int64) error {
	if _, err := t.tx.ExecContext(ctx, t.rebind(`DELETE FROM issue_labels WHERE issue_pk = ?`), issuePK); err != nil {
		return err
	}
	return t.AttachLabels(ctx, issuePK, labelPKs)
}

// DetachLabel removes one label from an issue.
func (t *Tx) DetachLabel(ctx context.Context, issuePK, labelPK int64) error {
	q := t.rebind(`DELETE FROM issue_labels WHERE issue_pk = ? AND label_pk = ?`)
	_, err := t.tx.ExecContext(ctx, q, issuePK, labelPK)
	return err
}

// AddAssignees links the given users to the issue, preserving request order in
// the position column and ignoring users already assigned.
func (t *Tx) AddAssignees(ctx context.Context, issuePK int64, userPKs []int64) error {
	var base int
	row := t.tx.QueryRowContext(ctx, t.rebind(`SELECT COALESCE(MAX(position)+1, 0) FROM assignees WHERE issue_pk = ?`), issuePK)
	if err := row.Scan(&base); err != nil {
		return err
	}
	for i, up := range userPKs {
		q := t.rebind(`INSERT INTO assignees (issue_pk, user_pk, position) VALUES (?, ?, ?)
			ON CONFLICT (issue_pk, user_pk) DO NOTHING`)
		if _, err := t.tx.ExecContext(ctx, q, issuePK, up, base+i); err != nil {
			return err
		}
	}
	return nil
}

// RemoveAssignees unlinks the given users from the issue.
func (t *Tx) RemoveAssignees(ctx context.Context, issuePK int64, userPKs []int64) error {
	for _, up := range userPKs {
		q := t.rebind(`DELETE FROM assignees WHERE issue_pk = ? AND user_pk = ?`)
		if _, err := t.tx.ExecContext(ctx, q, issuePK, up); err != nil {
			return err
		}
	}
	return nil
}

// ReplaceAssignees sets an issue's assignees to exactly the given set.
func (t *Tx) ReplaceAssignees(ctx context.Context, issuePK int64, userPKs []int64) error {
	if _, err := t.tx.ExecContext(ctx, t.rebind(`DELETE FROM assignees WHERE issue_pk = ?`), issuePK); err != nil {
		return err
	}
	return t.AddAssignees(ctx, issuePK, userPKs)
}

// InsertIssueEvent appends a timeline event (closed, reopened, labeled, ...) to
// an issue's history. Payload is a JSON document the event renderer decodes.
func (t *Tx) InsertIssueEvent(ctx context.Context, e *IssueEventRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	if e.Payload == "" {
		e.Payload = "{}"
	}
	q := t.rebind(`INSERT INTO issue_events (db_id, repo_pk, issue_pk, actor_pk, event, payload)
		VALUES (?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id, created_at`)
	var created nullTime
	err = t.tx.QueryRowContext(ctx, q,
		dbID, e.RepoPK, e.IssuePK, argI64(e.ActorPK), e.Event, e.Payload,
	).Scan(&e.PK, &e.DBID, &created)
	if err != nil {
		return err
	}
	e.CreatedAt = created.Time
	return nil
}

// IssueEventRow is a row of the issue_events timeline log.
type IssueEventRow struct {
	PK        int64
	DBID      int64
	RepoPK    int64
	IssuePK   int64
	ActorPK   *int64
	Event     string
	Payload   string
	CreatedAt time.Time
}

// ListIssueEvents returns an issue's timeline in chronological order.
func (s *Store) ListIssueEvents(ctx context.Context, issuePK int64) ([]IssueEventRow, error) {
	q := s.rebind(`SELECT pk, db_id, repo_pk, issue_pk, actor_pk, event, payload, created_at
		FROM issue_events WHERE issue_pk = ? ORDER BY created_at, pk`)
	rows, err := s.db.QueryContext(ctx, q, issuePK)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []IssueEventRow
	for rows.Next() {
		var (
			e       IssueEventRow
			actor   sql.NullInt64
			created nullTime
		)
		if err := rows.Scan(&e.PK, &e.DBID, &e.RepoPK, &e.IssuePK, &actor, &e.Event, &e.Payload, &created); err != nil {
			return nil, err
		}
		e.ActorPK = i64Ptr(actor)
		e.CreatedAt = created.Time
		out = append(out, e)
	}
	return out, rows.Err()
}

// TouchIssue bumps an issue's updated_at without touching the optimistic lock,
// used when a related row (a comment, a reaction) changes and GitHub advances
// the issue's updated timestamp.
func (t *Tx) TouchIssue(ctx context.Context, issuePK int64) error {
	q := t.rebind(`UPDATE issues SET updated_at = ? WHERE pk = ?`)
	_, err := t.tx.ExecContext(ctx, q, nowUTC(), issuePK)
	return err
}

// AdjustOpenIssuesCount bumps the repositories.open_issues_count cache when an
// issue opens or closes so the repository view need not aggregate on read.
func (t *Tx) AdjustOpenIssuesCount(ctx context.Context, repoPK int64, delta int) error {
	q := t.rebind(`UPDATE repositories SET open_issues_count = open_issues_count + ? WHERE pk = ?`)
	_, err := t.tx.ExecContext(ctx, q, delta, repoPK)
	return err
}

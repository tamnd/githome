package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// The checks store. Commit statuses and check runs are the two independent
// signals reported against a head sha; both are written by their report
// endpoints and read back per sha to form the combined status and the rollup. A
// check run lives inside a check suite keyed by (repo, head_sha, app), so the
// report path resolves or creates the suite first. The pull_request_check_state
// row is the denormalized snapshot the recompute worker upserts.

const commitStatusColumns = `pk, db_id, repo_pk, sha, state, context, target_url,
	description, creator_pk, created_at, updated_at`

// InsertCommitStatus appends a status report for a sha under a context. Each
// report is its own row; the latest per context wins when combining, matching
// GitHub's append-and-supersede model.
func (s *Store) InsertCommitStatus(ctx context.Context, st *CommitStatusRow) error {
	return s.WithTx(ctx, func(t *Tx) error {
		dbID, err := t.allocDBID(ctx)
		if err != nil {
			return err
		}
		if st.Context == "" {
			st.Context = "default"
		}
		q := t.rebind(`INSERT INTO commit_statuses
			(db_id, repo_pk, sha, state, context, target_url, description, creator_pk)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			RETURNING pk, db_id, created_at, updated_at`)
		var created, upd nullTime
		err = t.tx.QueryRowContext(ctx, q,
			dbID, st.RepoPK, st.SHA, st.State, st.Context,
			argStr(st.TargetURL), argStr(st.Description), argI64(st.CreatorPK),
		).Scan(&st.PK, &st.DBID, &created, &upd)
		if err != nil {
			return err
		}
		st.CreatedAt, st.UpdatedAt = created.Time, upd.Time
		return nil
	})
}

// ListCommitStatuses returns every status reported for a sha, newest first, the
// raw list the statuses endpoint pages and the combined-state algorithm folds.
func (s *Store) ListCommitStatuses(ctx context.Context, repoPK int64, sha string) ([]CommitStatusRow, error) {
	q := s.rebind(`SELECT ` + commitStatusColumns + ` FROM commit_statuses
		WHERE repo_pk = ? AND sha = ? ORDER BY created_at DESC, pk DESC`)
	rows, err := s.rdb.QueryContext(ctx, q, repoPK, sha)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CommitStatusRow
	for rows.Next() {
		st, err := scanCommitStatusRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *st)
	}
	return out, rows.Err()
}

func scanCommitStatusRows(row interface{ Scan(...any) error }) (*CommitStatusRow, error) {
	var (
		st           CommitStatusRow
		targetURL    sql.NullString
		description  sql.NullString
		creatorPK    sql.NullInt64
		created, upd nullTime
	)
	if err := row.Scan(&st.PK, &st.DBID, &st.RepoPK, &st.SHA, &st.State, &st.Context,
		&targetURL, &description, &creatorPK, &created, &upd); err != nil {
		return nil, err
	}
	st.TargetURL = strPtr(targetURL)
	st.Description = strPtr(description)
	st.CreatorPK = i64Ptr(creatorPK)
	st.CreatedAt, st.UpdatedAt = created.Time, upd.Time
	return &st, nil
}

const checkSuiteColumns = `pk, db_id, repo_pk, head_sha, app_slug, status,
	conclusion, created_at, updated_at`

// EnsureCheckSuite returns the suite for (repo, head_sha, app), creating it on
// first report. The unique key makes the create idempotent under the same head.
func (s *Store) EnsureCheckSuite(ctx context.Context, repoPK int64, headSHA, appSlug string) (*CheckSuiteRow, error) {
	if appSlug == "" {
		appSlug = "githome"
	}
	got, err := s.getCheckSuite(ctx, repoPK, headSHA, appSlug)
	if err == nil || !errors.Is(err, ErrNotFound) {
		return got, err
	}
	suite := &CheckSuiteRow{RepoPK: repoPK, HeadSHA: headSHA, AppSlug: appSlug, Status: "queued"}
	err = s.WithTx(ctx, func(t *Tx) error {
		dbID, aerr := t.allocDBID(ctx)
		if aerr != nil {
			return aerr
		}
		q := t.rebind(`INSERT INTO check_suites (db_id, repo_pk, head_sha, app_slug, status)
			VALUES (?, ?, ?, ?, ?)
			RETURNING pk, db_id, created_at, updated_at`)
		var created, upd nullTime
		return t.tx.QueryRowContext(ctx, q, dbID, repoPK, headSHA, appSlug, "queued").
			Scan(&suite.PK, &suite.DBID, &created, &upd)
	})
	if err != nil {
		return nil, err
	}
	return suite, nil
}

func (s *Store) getCheckSuite(ctx context.Context, repoPK int64, headSHA, appSlug string) (*CheckSuiteRow, error) {
	q := s.rebind(`SELECT ` + checkSuiteColumns + ` FROM check_suites
		WHERE repo_pk = ? AND head_sha = ? AND app_slug = ?`)
	return scanCheckSuite(s.rdb.QueryRowContext(ctx, q, repoPK, headSHA, appSlug))
}

// ListCheckSuites returns every suite reported against a head sha.
func (s *Store) ListCheckSuites(ctx context.Context, repoPK int64, headSHA string) ([]CheckSuiteRow, error) {
	q := s.rebind(`SELECT ` + checkSuiteColumns + ` FROM check_suites
		WHERE repo_pk = ? AND head_sha = ? ORDER BY pk`)
	rows, err := s.rdb.QueryContext(ctx, q, repoPK, headSHA)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CheckSuiteRow
	for rows.Next() {
		suite, err := scanCheckSuiteRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *suite)
	}
	return out, rows.Err()
}

// GetCheckSuiteByDBID resolves a check suite by its public database id, the
// lookup GET /repos/{owner}/{repo}/check-suites/{id} performs.
func (s *Store) GetCheckSuiteByDBID(ctx context.Context, dbID int64) (*CheckSuiteRow, error) {
	q := s.rebind(`SELECT ` + checkSuiteColumns + ` FROM check_suites WHERE db_id = ?`)
	return scanCheckSuite(s.rdb.QueryRowContext(ctx, q, dbID))
}

// SetCheckSuiteState rolls a suite's status and conclusion forward, the summary
// the recompute derives from the suite's runs.
func (s *Store) SetCheckSuiteState(ctx context.Context, pk int64, status string, conclusion *string) error {
	q := s.rebind(`UPDATE check_suites SET status = ?, conclusion = ?, updated_at = ?
		WHERE pk = ?`)
	res, err := s.db.ExecContext(ctx, q, status, argStr(conclusion), nowUTC(), pk)
	if err != nil {
		return err
	}
	return affectedOrNotFound(res)
}

func scanCheckSuite(row interface{ Scan(...any) error }) (*CheckSuiteRow, error) {
	suite, err := scanCheckSuiteRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return suite, err
}

func scanCheckSuiteRows(row interface{ Scan(...any) error }) (*CheckSuiteRow, error) {
	var (
		suite        CheckSuiteRow
		conclusion   sql.NullString
		created, upd nullTime
	)
	if err := row.Scan(&suite.PK, &suite.DBID, &suite.RepoPK, &suite.HeadSHA,
		&suite.AppSlug, &suite.Status, &conclusion, &created, &upd); err != nil {
		return nil, err
	}
	suite.Conclusion = strPtr(conclusion)
	suite.CreatedAt, suite.UpdatedAt = created.Time, upd.Time
	return &suite, nil
}

const checkRunColumns = `pk, db_id, suite_pk,
	(SELECT cs.db_id FROM check_suites cs WHERE cs.pk = check_runs.suite_pk),
	repo_pk, head_sha, name, status,
	conclusion, details_url, external_id, output_title, output_summary,
	output_text, started_at, completed_at, actions, annotations_count,
	created_at, updated_at`

// InsertCheckRun writes a check run with a freshly allocated db_id.
func (s *Store) InsertCheckRun(ctx context.Context, r *CheckRunRow) error {
	return s.WithTx(ctx, func(t *Tx) error {
		dbID, err := t.allocDBID(ctx)
		if err != nil {
			return err
		}
		if r.Status == "" {
			r.Status = "queued"
		}
		q := t.rebind(`INSERT INTO check_runs
			(db_id, suite_pk, repo_pk, head_sha, name, status, conclusion,
			 details_url, external_id, output_title, output_summary, output_text,
			 started_at, completed_at, actions)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			RETURNING pk, db_id, created_at, updated_at`)
		var created, upd nullTime
		err = t.tx.QueryRowContext(ctx, q,
			dbID, r.SuitePK, r.RepoPK, r.HeadSHA, r.Name, r.Status,
			argStr(r.Conclusion), argStr(r.DetailsURL), argStr(r.ExternalID),
			argStr(r.OutputTitle), argStr(r.OutputSummary), argStr(r.OutputText),
			argTime(r.StartedAt), argTime(r.CompletedAt), argStr(r.ActionsJSON),
		).Scan(&r.PK, &r.DBID, &created, &upd)
		if err != nil {
			return err
		}
		r.CreatedAt, r.UpdatedAt = created.Time, upd.Time
		return nil
	})
}

// UpdateCheckRun rewrites a run's mutable fields, the transition a re-report or a
// run finishing performs.
func (s *Store) UpdateCheckRun(ctx context.Context, r *CheckRunRow) error {
	q := s.rebind(`UPDATE check_runs SET
		status = ?, conclusion = ?, details_url = ?, external_id = ?,
		output_title = ?, output_summary = ?, output_text = ?,
		started_at = ?, completed_at = ?, actions = ?, updated_at = ?
		WHERE pk = ?`)
	res, err := s.db.ExecContext(ctx, q,
		r.Status, argStr(r.Conclusion), argStr(r.DetailsURL), argStr(r.ExternalID),
		argStr(r.OutputTitle), argStr(r.OutputSummary), argStr(r.OutputText),
		argTime(r.StartedAt), argTime(r.CompletedAt), argStr(r.ActionsJSON), nowUTC(), r.PK)
	if err != nil {
		return err
	}
	return affectedOrNotFound(res)
}

// GetCheckRun resolves a check run by its public database id.
func (s *Store) GetCheckRun(ctx context.Context, dbID int64) (*CheckRunRow, error) {
	q := s.rebind(`SELECT ` + checkRunColumns + ` FROM check_runs WHERE db_id = ?`)
	return scanCheckRun(s.rdb.QueryRowContext(ctx, q, dbID))
}

// ListCheckRunsForRef returns every check run reported against a head sha, the
// list the rollup folds and the check-runs endpoint pages.
func (s *Store) ListCheckRunsForRef(ctx context.Context, repoPK int64, headSHA string) ([]CheckRunRow, error) {
	return s.queryCheckRuns(ctx, `WHERE repo_pk = ? AND head_sha = ? ORDER BY pk`, repoPK, headSHA)
}

// ListCheckRunsForSuite returns the runs inside one suite.
func (s *Store) ListCheckRunsForSuite(ctx context.Context, suitePK int64) ([]CheckRunRow, error) {
	return s.queryCheckRuns(ctx, `WHERE suite_pk = ? ORDER BY pk`, suitePK)
}

func (s *Store) queryCheckRuns(ctx context.Context, where string, args ...any) ([]CheckRunRow, error) {
	q := s.rebind(`SELECT ` + checkRunColumns + ` FROM check_runs ` + where)
	rows, err := s.rdb.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CheckRunRow
	for rows.Next() {
		r, err := scanCheckRunRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func scanCheckRun(row interface{ Scan(...any) error }) (*CheckRunRow, error) {
	r, err := scanCheckRunRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func scanCheckRunRows(row interface{ Scan(...any) error }) (*CheckRunRow, error) {
	var (
		r                                      CheckRunRow
		conclusion, detailsURL, externalID     sql.NullString
		outputTitle, outputSummary, outputText sql.NullString
		actions                                sql.NullString
		startedAt, completedAt, created, upd   nullTime
	)
	if err := row.Scan(&r.PK, &r.DBID, &r.SuitePK, &r.SuiteDBID, &r.RepoPK, &r.HeadSHA, &r.Name,
		&r.Status, &conclusion, &detailsURL, &externalID, &outputTitle,
		&outputSummary, &outputText, &startedAt, &completedAt, &actions,
		&r.AnnotationsCount, &created, &upd); err != nil {
		return nil, err
	}
	r.Conclusion = strPtr(conclusion)
	r.DetailsURL = strPtr(detailsURL)
	r.ExternalID = strPtr(externalID)
	r.OutputTitle = strPtr(outputTitle)
	r.OutputSummary = strPtr(outputSummary)
	r.OutputText = strPtr(outputText)
	r.ActionsJSON = strPtr(actions)
	r.StartedAt = startedAt.ptr()
	r.CompletedAt = completedAt.ptr()
	r.CreatedAt, r.UpdatedAt = created.Time, upd.Time
	return &r, nil
}

// InsertCheckRunAnnotations appends a batch of annotations to a check run and
// refreshes the run's denormalized count, all inside one transaction.
// Annotations accumulate across updates; GitHub never replaces earlier ones.
func (s *Store) InsertCheckRunAnnotations(ctx context.Context, checkRunPK int64, anns []CheckRunAnnotationRow) error {
	if len(anns) == 0 {
		return nil
	}
	return s.WithTx(ctx, func(t *Tx) error {
		q := t.rebind(`INSERT INTO check_run_annotations
			(check_run_pk, path, start_line, end_line, start_column, end_column,
			 annotation_level, message, title, raw_details)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		for i := range anns {
			a := &anns[i]
			if _, err := t.tx.ExecContext(ctx, q,
				checkRunPK, a.Path, a.StartLine, a.EndLine,
				argI64(a.StartColumn), argI64(a.EndColumn),
				a.AnnotationLevel, a.Message, argStr(a.Title), argStr(a.RawDetails),
			); err != nil {
				return err
			}
		}
		count := t.rebind(`UPDATE check_runs SET annotations_count =
			(SELECT COUNT(*) FROM check_run_annotations WHERE check_run_pk = ?)
			WHERE pk = ?`)
		_, err := t.tx.ExecContext(ctx, count, checkRunPK, checkRunPK)
		return err
	})
}

// ListCheckRunAnnotations returns a check run's annotations in insertion order,
// the body of the annotations endpoint.
func (s *Store) ListCheckRunAnnotations(ctx context.Context, checkRunPK int64) ([]CheckRunAnnotationRow, error) {
	q := s.rebind(`SELECT pk, check_run_pk, path, start_line, end_line,
		start_column, end_column, annotation_level, message, title, raw_details,
		created_at
		FROM check_run_annotations WHERE check_run_pk = ? ORDER BY pk`)
	rows, err := s.rdb.QueryContext(ctx, q, checkRunPK)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CheckRunAnnotationRow
	for rows.Next() {
		var (
			a                 CheckRunAnnotationRow
			startCol, endCol  sql.NullInt64
			title, rawDetails sql.NullString
			created           nullTime
		)
		if err := rows.Scan(&a.PK, &a.CheckRunPK, &a.Path, &a.StartLine, &a.EndLine,
			&startCol, &endCol, &a.AnnotationLevel, &a.Message, &title, &rawDetails,
			&created); err != nil {
			return nil, err
		}
		a.StartColumn = i64Ptr(startCol)
		a.EndColumn = i64Ptr(endCol)
		a.Title = strPtr(title)
		a.RawDetails = strPtr(rawDetails)
		a.CreatedAt = created.Time
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetPullCheckState returns a pull request's cached review decision and rollup
// snapshot, or ErrNotFound before the recompute worker has written one.
func (s *Store) GetPullCheckState(ctx context.Context, pullPK int64) (*PullCheckStateRow, error) {
	q := s.rebind(`SELECT pull_pk, review_decision, rollup_state, updated_at
		FROM pull_request_check_state WHERE pull_pk = ?`)
	var (
		row      PullCheckStateRow
		decision sql.NullString
		upd      nullTime
	)
	err := s.rdb.QueryRowContext(ctx, q, pullPK).Scan(&row.PullPK, &decision, &row.RollupState, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	row.ReviewDecision = strPtr(decision)
	row.UpdatedAt = upd.Time
	return &row, nil
}

// UpsertPullCheckState writes a pull request's derived review decision and rollup
// state, the snapshot the recompute worker refreshes on review and status change.
func (s *Store) UpsertPullCheckState(ctx context.Context, pullPK int64, decision *string, rollup string, at time.Time) error {
	q := s.rebind(`INSERT INTO pull_request_check_state
		(pull_pk, review_decision, rollup_state, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (pull_pk) DO UPDATE SET
			review_decision = excluded.review_decision,
			rollup_state = excluded.rollup_state,
			updated_at = excluded.updated_at`)
	_, err := s.db.ExecContext(ctx, q, pullPK, argStr(decision), rollup, at)
	return err
}

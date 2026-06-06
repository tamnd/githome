package store

import (
	"context"
	"time"
)

// This file is the bulk-seed write path. It is used by the corpus seeders (the
// benchmark seeder and the real-world ingest pipeline), never by the served
// binary. Unlike the domain write methods in queries_*.go, the Seed* methods
// here take created_at and updated_at from the caller and write them verbatim,
// because a corpus must preserve the real timestamps it was exported with:
// ordering, since filters, and ETag derivation all depend on them. The db_id is
// still allocated from the shared sequence in insertion order, so two seed runs
// over the same input produce the same ids deterministically.
//
// Every Seed* method runs on a Tx so a whole corpus loads as one transaction (or
// a small number of them) and either lands whole or rolls back. The reads stay
// in queries_*.go; this file only writes.

// timeArg formats a timestamp for a bound parameter inside a transaction,
// mirroring Store.timeArg so the seed path stores the same on-disk form the
// server's writers and the keyset seek compare against.
func (t *Tx) timeArg(tm time.Time) any {
	if t.dialect == DialectSQLite {
		return tm.UTC().Format(sqliteTimeFmt)
	}
	return tm
}

// SeedUser inserts one users row preserving its timestamps, filling PK and DBID
// back onto u. The profile columns keep their defaults; a corpus carries
// identity, not full profiles.
func (t *Tx) SeedUser(ctx context.Context, u *UserRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	if u.Type == "" {
		u.Type = "User"
	}
	q := t.rebind(`INSERT INTO users (db_id, login, type, name, email, site_admin, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id`)
	if err := t.tx.QueryRowContext(ctx, q,
		dbID, u.Login, u.Type, argStr(u.Name), argStr(u.Email), u.SiteAdmin,
		t.timeArg(u.CreatedAt), t.timeArg(u.UpdatedAt),
	).Scan(&u.PK, &u.DBID); err != nil {
		return err
	}
	return nil
}

// SeedRepo inserts one repositories row preserving its timestamps and pushed_at,
// filling PK and DBID back onto r. next_issue_number keeps its default; the
// caller sets it past the seeded numbers with SetNextIssueNumber once the issues
// are in.
func (t *Tx) SeedRepo(ctx context.Context, r *RepoRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	if r.DefaultBranch == "" {
		r.DefaultBranch = "main"
	}
	q := t.rebind(`INSERT INTO repositories
		(db_id, owner_pk, name, description, private, fork, default_branch, pushed_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id`)
	var pushed any
	if r.PushedAt != nil {
		pushed = t.timeArg(*r.PushedAt)
	}
	return t.tx.QueryRowContext(ctx, q,
		dbID, r.OwnerPK, r.Name, argStr(r.Description), r.Private, r.Fork,
		r.DefaultBranch, pushed, t.timeArg(r.CreatedAt), t.timeArg(r.UpdatedAt),
	).Scan(&r.PK, &r.DBID)
}

// SeedLabel inserts one labels row preserving its timestamps, filling PK and
// DBID. The caller dedupes labels per repository before calling.
func (t *Tx) SeedLabel(ctx context.Context, l *LabelRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	if l.Color == "" {
		l.Color = "ededed"
	}
	q := t.rebind(`INSERT INTO labels (db_id, repo_pk, name, color, description, is_default, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id`)
	return t.tx.QueryRowContext(ctx, q,
		dbID, l.RepoPK, l.Name, l.Color, argStr(l.Description), l.IsDefault,
		t.timeArg(l.CreatedAt), t.timeArg(l.UpdatedAt),
	).Scan(&l.PK, &l.DBID)
}

// SeedMilestone inserts one milestones row with the number preserved from the
// corpus (not allocated), filling PK and DBID.
func (t *Tx) SeedMilestone(ctx context.Context, m *MilestoneRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	if m.State == "" {
		m.State = "open"
	}
	q := t.rebind(`INSERT INTO milestones
		(db_id, repo_pk, number, title, description, state, due_on, creator_pk, closed_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id`)
	var due, closed any
	if m.DueOn != nil {
		due = t.timeArg(*m.DueOn)
	}
	if m.ClosedAt != nil {
		closed = t.timeArg(*m.ClosedAt)
	}
	return t.tx.QueryRowContext(ctx, q,
		dbID, m.RepoPK, m.Number, m.Title, argStr(m.Description), m.State,
		due, argI64(m.CreatorPK), closed, t.timeArg(m.CreatedAt), t.timeArg(m.UpdatedAt),
	).Scan(&m.PK, &m.DBID)
}

// SeedIssue inserts one issues row with the per-repo number preserved from the
// corpus so URLs match real GitHub paths, all state fields and timestamps
// written verbatim. It fills PK and DBID. The table is shared by issues and pull
// requests; IsPull tells them apart.
func (t *Tx) SeedIssue(ctx context.Context, iss *IssueRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	if iss.State == "" {
		iss.State = "open"
	}
	q := t.rebind(`INSERT INTO issues
		(db_id, repo_pk, number, is_pull, title, body, user_pk, state, state_reason,
		 milestone_pk, locked, active_lock_reason, comments_count, closed_at, closed_by_pk,
		 created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id`)
	var closed any
	if iss.ClosedAt != nil {
		closed = t.timeArg(*iss.ClosedAt)
	}
	return t.tx.QueryRowContext(ctx, q,
		dbID, iss.RepoPK, iss.Number, iss.IsPull, iss.Title, argStr(iss.Body), iss.UserPK,
		iss.State, argStr(iss.StateReason), argI64(iss.MilestonePK), iss.Locked,
		argStr(iss.ActiveLockReason), iss.CommentsCount, closed, argI64(iss.ClosedByPK),
		t.timeArg(iss.CreatedAt), t.timeArg(iss.UpdatedAt),
	).Scan(&iss.PK, &iss.DBID)
}

// SeedComment inserts one issue_comments row preserving its timestamps.
func (t *Tx) SeedComment(ctx context.Context, c *CommentRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	q := t.rebind(`INSERT INTO issue_comments (db_id, issue_pk, user_pk, body, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id`)
	return t.tx.QueryRowContext(ctx, q,
		dbID, c.IssuePK, c.UserPK, c.Body, t.timeArg(c.CreatedAt), t.timeArg(c.UpdatedAt),
	).Scan(&c.PK, &c.DBID)
}

// SeedReaction inserts one reactions row preserving its created_at. The caller
// supplies the reactor user pk; the corpus carries counts, not per-user rows, so
// the seeder materializes them against a bounded reactor pool.
func (t *Tx) SeedReaction(ctx context.Context, r *ReactionRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	q := t.rebind(`INSERT INTO reactions (db_id, subject_type, subject_pk, user_pk, content, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id`)
	return t.tx.QueryRowContext(ctx, q,
		dbID, r.SubjectType, r.SubjectPK, r.UserPK, r.Content, t.timeArg(r.CreatedAt),
	).Scan(&r.PK, &r.DBID)
}

// SeedPull inserts one pull_requests row joined to its issue, with the merge
// state and diff stats written verbatim. mergeable is left NULL (the
// not-yet-computed contract) unless the corpus carried a value.
func (t *Tx) SeedPull(ctx context.Context, p *PullRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	if p.MergeableState == "" {
		p.MergeableState = "unknown"
	}
	q := t.rebind(`INSERT INTO pull_requests
		(db_id, issue_pk, repo_pk, base_ref, base_sha, head_ref, head_sha, head_repo_pk,
		 draft, maintainer_can_modify, merged, merged_at, merged_by_pk, merge_commit_sha,
		 mergeable, mergeable_state, additions, deletions, changed_files, commits_count,
		 created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id`)
	var mergedAt any
	if p.MergedAt != nil {
		mergedAt = t.timeArg(*p.MergedAt)
	}
	return t.tx.QueryRowContext(ctx, q,
		dbID, p.IssuePK, p.RepoPK, p.BaseRef, p.BaseSHA, p.HeadRef, p.HeadSHA, argI64(p.HeadRepoPK),
		p.Draft, p.MaintainerCanModify, p.Merged, mergedAt, argI64(p.MergedByPK), argStr(p.MergeCommitSHA),
		argBool(p.Mergeable), p.MergeableState, p.Additions, p.Deletions, p.ChangedFiles, p.CommitsCount,
		t.timeArg(p.CreatedAt), t.timeArg(p.UpdatedAt),
	).Scan(&p.PK, &p.DBID)
}

// SeedReview inserts one pull_request_reviews row preserving submitted_at and the
// timestamps.
func (t *Tx) SeedReview(ctx context.Context, r *ReviewRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	q := t.rebind(`INSERT INTO pull_request_reviews
		(db_id, pull_pk, repo_pk, user_pk, state, body, commit_id, submitted_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id`)
	var submitted any
	if r.SubmittedAt != nil {
		submitted = t.timeArg(*r.SubmittedAt)
	}
	return t.tx.QueryRowContext(ctx, q,
		dbID, r.PullPK, r.RepoPK, r.UserPK, r.State, r.Body, r.CommitID, submitted,
		t.timeArg(r.CreatedAt), t.timeArg(r.UpdatedAt),
	).Scan(&r.PK, &r.DBID)
}

// SeedReviewComment inserts one pull_request_review_comments row preserving the
// diff anchor and the reply linkage so threaded conversations reassemble.
func (t *Tx) SeedReviewComment(ctx context.Context, c *ReviewCommentRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	if c.Side == "" {
		c.Side = "RIGHT"
	}
	if c.SubjectType == "" {
		c.SubjectType = "line"
	}
	q := t.rebind(`INSERT INTO pull_request_review_comments
		(db_id, review_pk, pull_pk, repo_pk, user_pk, path, side, line, in_reply_to_pk,
		 diff_hunk, subject_type, body, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id`)
	return t.tx.QueryRowContext(ctx, q,
		dbID, c.ReviewPK, c.PullPK, c.RepoPK, c.UserPK, c.Path, c.Side, argI64(c.Line),
		argI64(c.InReplyToPK), c.DiffHunk, c.SubjectType, c.Body,
		t.timeArg(c.CreatedAt), t.timeArg(c.UpdatedAt),
	).Scan(&c.PK, &c.DBID)
}

// SeedCommitStatus inserts one commit_statuses row preserving its timestamps.
func (t *Tx) SeedCommitStatus(ctx context.Context, st *CommitStatusRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	if st.Context == "" {
		st.Context = "default"
	}
	q := t.rebind(`INSERT INTO commit_statuses
		(db_id, repo_pk, sha, state, context, target_url, description, creator_pk, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id`)
	return t.tx.QueryRowContext(ctx, q,
		dbID, st.RepoPK, st.SHA, st.State, st.Context, argStr(st.TargetURL), argStr(st.Description),
		argI64(st.CreatorPK), t.timeArg(st.CreatedAt), t.timeArg(st.UpdatedAt),
	).Scan(&st.PK, &st.DBID)
}

// SeedIssueEvent inserts one issue_events timeline row preserving its created_at
// and the rendered payload, filling PK and DBID. This is the largest table in a
// real-world corpus, so the timeline and events-feed reads run against realistic
// volume.
func (t *Tx) SeedIssueEvent(ctx context.Context, e *IssueEventRow) error {
	dbID, err := t.allocDBID(ctx)
	if err != nil {
		return err
	}
	if e.Payload == "" {
		e.Payload = "{}"
	}
	q := t.rebind(`INSERT INTO issue_events (db_id, repo_pk, issue_pk, actor_pk, event, payload, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		RETURNING pk, db_id`)
	return t.tx.QueryRowContext(ctx, q,
		dbID, e.RepoPK, e.IssuePK, argI64(e.ActorPK), e.Event, e.Payload, t.timeArg(e.CreatedAt),
	).Scan(&e.PK, &e.DBID)
}

// SetNextIssueNumber moves a repository's number allocator past the highest
// seeded number so the first live issue created after a seed does not collide
// with a preserved corpus number.
func (s *Store) SetNextIssueNumber(ctx context.Context, repoPK, next int64) error {
	q := s.rebind(`UPDATE repositories SET next_issue_number = ? WHERE pk = ?`)
	_, err := s.db.ExecContext(ctx, q, next, repoPK)
	return err
}

// RecomputeIssueCommentCounts rebuilds issues.comments_count from the live
// issue_comments rows, so the denormalized counter the dataset shipped is
// replaced by one the corpus is internally consistent with.
func (s *Store) RecomputeIssueCommentCounts(ctx context.Context, repoPK int64) error {
	q := s.rebind(`UPDATE issues SET comments_count = (
		SELECT COUNT(*) FROM issue_comments c
		WHERE c.issue_pk = issues.pk AND c.deleted_at IS NULL
	) WHERE repo_pk = ?`)
	_, err := s.db.ExecContext(ctx, q, repoPK)
	return err
}

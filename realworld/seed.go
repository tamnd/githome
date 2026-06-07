package realworld

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tamnd/githome/store"
)

// userEpoch is the created_at stamped on the synthetic users a corpus needs but
// the public dataset does not carry a profile timestamp for (every login, and
// the reactor pool). A corpus carries real timestamps for issues, comments, and
// events; user accounts predate all of them, so a fixed early instant is both
// honest (these are synthesized rows) and harmless to any ordering the reads do.
var userEpoch = time.Date(2008, 1, 1, 0, 0, 0, 0, time.UTC)

// SeedResult reports what a corpus seed wrote, so the caller can fold it into
// the manifest as the measured artifact.
type SeedResult struct {
	RepoPK  int64
	Rows    map[string]int
	Dropped []DropNote
}

// SeedCorpus writes one corpus into a store through the bulk-seed write path,
// preserving every per-repo number and timestamp. The whole repository loads in
// one transaction so it lands whole or rolls back. The caller migrates the store
// first (or passes a migrated one); SeedCorpus does not migrate, so a multi-repo
// seed shares one schema.
//
// Determinism: the tables are seeded in a fixed order (issues by number,
// comments and reviews by id, and so on), so the db_id sequence advances the
// same way on every run, and reactions are materialized against the bounded
// reactor pool with a fixed assignment, so two seeds of the same corpus produce
// identical databases.
func SeedCorpus(ctx context.Context, st *store.Store, c *Corpus, reactor ReactorPool) (*SeedResult, error) {
	s := newSeeder(ctx, c, reactor)

	// The whole-repo insert runs under the bulk-load pragmas: on SQLite that
	// trades per-commit durability for throughput during the load, which is the
	// right trade for a re-runnable seed and is what makes a million-row corpus
	// land in feasible time. BulkLoad restores the serving pragmas afterward.
	err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *store.Tx) error {
			s.tx = tx
			return s.run()
		})
	})
	if err != nil {
		return nil, err
	}
	if err := s.finish(st); err != nil {
		return nil, err
	}
	return s.res, nil
}

// SeedSnapshot seeds one repo's corpus straight from its on-disk snapshot,
// streaming each table from disk and releasing it before the next, so the seeder
// never holds the whole corpus in memory at once. Peak memory is one table plus
// the foreign-key resolution maps, not the sum of every table's rows, which is
// what lets a multi-hundred-thousand-row repo seed without loading gigabytes of
// bodies into RAM. It is the scale counterpart to SeedCorpus, which takes an
// already-materialized corpus and is the path the in-memory and pseudonymized
// flows use.
func SeedSnapshot(ctx context.Context, st *store.Store, root string, ref RepoRef, reactor ReactorPool) (*SeedResult, error) {
	// The repo.json carries the pinned git side the manifest entry may not.
	repo := ref
	if stored, err := readJSON[RepoRef](filepath.Join(repoDir(root, ref), fileRepo)); err == nil {
		repo = stored
	}
	s := newSeeder(ctx, &Corpus{Repo: repo}, reactor)

	err := st.BulkLoad(ctx, func() error {
		return st.WithTx(ctx, func(tx *store.Tx) error {
			s.tx = tx
			return s.runStreaming(root, ref)
		})
	})
	if err != nil {
		return nil, err
	}
	if err := s.finish(st); err != nil {
		return nil, err
	}
	return s.res, nil
}

// newSeeder builds a seeder with empty resolution maps. The caller attaches the
// transaction (s.tx) inside BulkLoad before calling run or runStreaming.
func newSeeder(ctx context.Context, c *Corpus, reactor ReactorPool) *seeder {
	if reactor.Size <= 0 {
		reactor = DefaultReactorPool
	}
	return &seeder{
		ctx: ctx, c: c, reactor: reactor, res: &SeedResult{Rows: map[string]int{}},
		userPK: map[string]int64{}, labelPK: map[string]int64{},
		milestonePK: map[int64]int64{}, issuePK: map[int64]int64{},
		issueStamp: map[int64][2]time.Time{},
		pullPK:     map[int64]int64{}, reviewPK: map[int64]int64{},
		reviewCommentPK: map[int64]int64{},
	}
}

// finish runs the post-load store maintenance both seed paths share: advance the
// number allocator past the highest seeded number so the first live issue does
// not collide with a preserved one, and recompute the cached comment counts.
func (s *seeder) finish(st *store.Store) error {
	if err := st.SetNextIssueNumber(s.ctx, s.res.RepoPK, s.maxIssueNumber+1); err != nil {
		return err
	}
	return st.RecomputeIssueCommentCounts(s.ctx, s.res.RepoPK)
}

// seeder holds the resolution maps for one repository's load.
type seeder struct {
	tx      *store.Tx
	ctx     context.Context
	c       *Corpus
	reactor ReactorPool
	res     *SeedResult

	userPK          map[string]int64
	reactorPKs      []int64
	labelPK         map[string]int64       // lower(name) -> pk
	milestonePK     map[int64]int64        // number -> pk
	issuePK         map[int64]int64        // issue number -> pk
	issueStamp      map[int64][2]time.Time // issue number -> {created, updated}
	pullPK          map[int64]int64        // pr number -> pull_requests.pk
	reviewPK        map[int64]int64        // review id -> pk
	reviewCommentPK map[int64]int64        // review-comment id -> pk
	repoPK          int64
	maxIssueNumber  int64 // highest issue number seen, for the post-load allocator reset

	// reactionBuf accumulates the materialized reactor-pool rows as issues and
	// comments are seeded, so they load through the chunked bulk path in one
	// flush instead of a round trip apiece. Reactions are leaf rows (no children
	// reference their pk), which is what makes the bulk insert safe here.
	reactionBuf []store.ReactionRow
}

func (s *seeder) run() error {
	if err := s.seedUsers(); err != nil {
		return err
	}
	if err := s.seedReactorPool(); err != nil {
		return err
	}
	if err := s.seedRepo(); err != nil {
		return err
	}
	if err := s.seedLabels(); err != nil {
		return err
	}
	if err := s.seedMilestones(); err != nil {
		return err
	}
	if err := s.seedIssues(); err != nil {
		return err
	}
	if err := s.seedPulls(); err != nil {
		return err
	}
	if err := s.seedComments(); err != nil {
		return err
	}
	if err := s.seedReviews(); err != nil {
		return err
	}
	if err := s.seedReviewComments(); err != nil {
		return err
	}
	if err := s.seedTimeline(); err != nil {
		return err
	}
	if err := s.seedStatuses(); err != nil {
		return err
	}
	// Reactions buffered while seeding issues and comments flush last, in one
	// chunked bulk insert over a single batch-allocated id range.
	return s.flushReactions()
}

// runStreaming seeds straight from a snapshot directory, loading one table into
// the corpus at a time and releasing it before the next so peak memory is one
// table plus the resolution maps, not the whole corpus. The phase order matches
// run; each table feeds exactly one consecutive group of phases, so loading and
// releasing it around that group preserves the same deterministic db_id order.
func (s *seeder) runStreaming(root string, ref RepoRef) error {
	dir := repoDir(root, ref)

	// Users come first and need every login named anywhere, so collect the
	// distinct set in a streaming prepass that holds only the login strings, not
	// the rows they came from.
	logins, err := streamLogins(dir, s.c.Repo.Owner)
	if err != nil {
		return err
	}
	if err := s.seedUsersFrom(logins); err != nil {
		return err
	}
	if err := s.seedReactorPool(); err != nil {
		return err
	}
	if err := s.seedRepo(); err != nil {
		return err
	}

	// Issues feed labels, milestones, and issues; load once for the three.
	if s.c.Issues, err = readJSONL[Issue](filepath.Join(dir, fileIssues)); err != nil {
		return err
	}
	if err := s.seedLabels(); err != nil {
		return err
	}
	if err := s.seedMilestones(); err != nil {
		return err
	}
	if err := s.seedIssues(); err != nil {
		return err
	}
	s.c.Issues = nil

	for _, phase := range []struct {
		load func() error
		seed func() error
	}{
		{func() (err error) {
			s.c.PullRequests, err = readJSONL[PullRequest](filepath.Join(dir, filePulls))
			return
		}, s.seedPulls},
		{func() (err error) { s.c.Comments, err = readJSONL[Comment](filepath.Join(dir, fileComments)); return }, s.seedComments},
		{func() (err error) { s.c.Reviews, err = readJSONL[Review](filepath.Join(dir, fileReviews)); return }, s.seedReviews},
		{func() (err error) {
			s.c.ReviewComments, err = readJSONL[ReviewComment](filepath.Join(dir, fileReviewComments))
			return
		}, s.seedReviewComments},
		{func() (err error) {
			s.c.TimelineEvents, err = readJSONL[TimelineEvent](filepath.Join(dir, fileTimeline))
			return
		}, s.seedTimeline},
		{func() (err error) {
			s.c.CommitStatuses, err = readJSONL[CommitStatus](filepath.Join(dir, fileStatuses))
			return
		}, s.seedStatuses},
	} {
		if err := phase.load(); err != nil {
			return err
		}
		if err := phase.seed(); err != nil {
			return err
		}
		// Release this table before loading the next so they never coexist.
		s.c.PullRequests, s.c.Comments, s.c.Reviews = nil, nil, nil
		s.c.ReviewComments, s.c.TimelineEvents, s.c.CommitStatuses = nil, nil, nil
	}

	return s.flushReactions()
}

func (s *seeder) seedUsers() error { return s.seedUsersFrom(s.c.Logins()) }

// seedUsersFrom seeds the user table from an already-collected, ordered login
// set. Both seed paths funnel through here: the in-memory path passes
// Corpus.Logins, the streaming path passes the prepass result.
func (s *seeder) seedUsersFrom(logins []string) error {
	for _, login := range logins {
		u := &store.UserRow{Login: login, Type: "User", CreatedAt: userEpoch, UpdatedAt: userEpoch}
		if isBot(login) {
			u.Type = "Bot"
		}
		if err := s.tx.SeedUser(s.ctx, u); err != nil {
			return fmt.Errorf("seed user %q: %w", login, err)
		}
		s.userPK[login] = u.PK
	}
	s.res.Rows["users"] = len(s.userPK)
	return nil
}

func (s *seeder) seedReactorPool() error {
	s.reactorPKs = make([]int64, 0, s.reactor.Size)
	for i := 0; i < s.reactor.Size; i++ {
		u := &store.UserRow{Login: fmt.Sprintf("reactor-%03d", i), Type: "User", CreatedAt: userEpoch, UpdatedAt: userEpoch}
		if err := s.tx.SeedUser(s.ctx, u); err != nil {
			return fmt.Errorf("seed reactor %d: %w", i, err)
		}
		s.reactorPKs = append(s.reactorPKs, u.PK)
	}
	return nil
}

func (s *seeder) seedRepo() error {
	r := &store.RepoRow{
		OwnerPK:       s.userPK[s.c.Repo.Owner],
		Name:          s.c.Repo.Name,
		DefaultBranch: s.c.Repo.DefaultBranch,
		CreatedAt:     userEpoch,
		UpdatedAt:     userEpoch,
	}
	if err := s.tx.SeedRepo(s.ctx, r); err != nil {
		return fmt.Errorf("seed repo: %w", err)
	}
	s.repoPK = r.PK
	s.res.RepoPK = r.PK
	return nil
}

func (s *seeder) seedLabels() error {
	// Collect distinct labels per repo in first-seen issue-number order so the
	// db_id allocation is deterministic.
	order := slices.Clone(s.c.Issues)
	slices.SortFunc(order, func(a, b Issue) int { return int(a.Number - b.Number) })
	type lab struct {
		name, color, desc string
	}
	var distinct []lab
	seen := map[string]bool{}
	for _, iss := range order {
		for _, l := range iss.Labels {
			k := strings.ToLower(l.Name)
			if seen[k] {
				continue
			}
			seen[k] = true
			distinct = append(distinct, lab{l.Name, l.Color, l.Description})
		}
	}
	for _, l := range distinct {
		row := &store.LabelRow{RepoPK: s.repoPK, Name: l.name, Color: l.color, CreatedAt: userEpoch, UpdatedAt: userEpoch}
		if l.desc != "" {
			row.Description = &l.desc
		}
		if err := s.tx.SeedLabel(s.ctx, row); err != nil {
			return fmt.Errorf("seed label %q: %w", l.name, err)
		}
		s.labelPK[strings.ToLower(l.name)] = row.PK
	}
	s.res.Rows["labels"] = len(s.labelPK)
	return nil
}

func (s *seeder) seedMilestones() error {
	order := slices.Clone(s.c.Issues)
	slices.SortFunc(order, func(a, b Issue) int { return int(a.Number - b.Number) })
	for _, iss := range order {
		if iss.MilestoneNumber == 0 || s.milestonePK[iss.MilestoneNumber] != 0 {
			continue
		}
		m := &store.MilestoneRow{
			RepoPK: s.repoPK, Number: iss.MilestoneNumber, Title: iss.MilestoneTitle,
			State: "open", CreatedAt: userEpoch, UpdatedAt: userEpoch,
		}
		if err := s.tx.SeedMilestone(s.ctx, m); err != nil {
			return fmt.Errorf("seed milestone %d: %w", iss.MilestoneNumber, err)
		}
		s.milestonePK[iss.MilestoneNumber] = m.PK
	}
	s.res.Rows["milestones"] = len(s.milestonePK)
	return nil
}

func (s *seeder) seedIssues() error {
	order := slices.Clone(s.c.Issues)
	slices.SortFunc(order, func(a, b Issue) int { return int(a.Number - b.Number) })
	for _, iss := range order {
		userPK, ok := s.userPK[iss.Author]
		if !ok {
			return fmt.Errorf("issue %d: unknown author %q", iss.Number, iss.Author)
		}
		row := &store.IssueRow{
			RepoPK: s.repoPK, Number: iss.Number, IsPull: iss.IsPullRequest,
			Title: iss.Title, UserPK: userPK, State: iss.State, Locked: iss.Locked,
			CreatedAt: iss.CreatedAt, UpdatedAt: iss.UpdatedAt, ClosedAt: iss.ClosedAt,
		}
		if iss.Body != "" {
			row.Body = &iss.Body
		}
		if iss.StateReason != "" {
			row.StateReason = &iss.StateReason
		}
		if iss.LockReason != "" {
			row.ActiveLockReason = &iss.LockReason
		}
		if mp, ok := s.milestonePK[iss.MilestoneNumber]; ok && iss.MilestoneNumber != 0 {
			row.MilestonePK = &mp
		}
		if err := s.tx.SeedIssue(s.ctx, row); err != nil {
			return fmt.Errorf("seed issue %d: %w", iss.Number, err)
		}
		s.issuePK[iss.Number] = row.PK
		s.issueStamp[iss.Number] = [2]time.Time{iss.CreatedAt, iss.UpdatedAt}
		if iss.Number > s.maxIssueNumber {
			s.maxIssueNumber = iss.Number
		}

		if len(iss.Labels) > 0 {
			pks := make([]int64, 0, len(iss.Labels))
			for _, l := range iss.Labels {
				if pk, ok := s.labelPK[strings.ToLower(l.Name)]; ok {
					pks = append(pks, pk)
				}
			}
			if err := s.tx.AttachLabels(s.ctx, row.PK, pks); err != nil {
				return err
			}
		}
		if len(iss.Assignees) > 0 {
			pks := make([]int64, 0, len(iss.Assignees))
			for _, a := range iss.Assignees {
				if pk, ok := s.userPK[a]; ok {
					pks = append(pks, pk)
				}
			}
			if err := s.tx.AddAssignees(s.ctx, row.PK, pks); err != nil {
				return err
			}
		}
		if err := s.expandReactions("issue", row.PK, iss.Reactions, iss.CreatedAt); err != nil {
			return err
		}
	}
	s.res.Rows["issues"] = len(s.issuePK)
	return nil
}

func (s *seeder) seedPulls() error {
	order := slices.Clone(s.c.PullRequests)
	slices.SortFunc(order, func(a, b PullRequest) int { return int(a.Number - b.Number) })
	for _, pr := range order {
		issuePK, ok := s.issuePK[pr.Number]
		if !ok {
			s.drop("pull_request", "no issue row for PR number", 1)
			continue
		}
		row := &store.PullRow{
			IssuePK: issuePK, RepoPK: s.repoPK, BaseRef: pr.BaseRef, HeadRef: pr.HeadRef,
			HeadSHA: pr.HeadSHA, Draft: pr.Draft, MaintainerCanModify: pr.MaintainerCanModify,
			Merged: pr.Merged, MergedAt: pr.MergedAt, Additions: pr.Additions,
			Deletions: pr.Deletions, ChangedFiles: pr.ChangedFiles,
			CreatedAt: userEpoch, UpdatedAt: userEpoch,
		}
		if ts, ok := s.issueStamp[pr.Number]; ok {
			row.CreatedAt, row.UpdatedAt = ts[0], ts[1]
		}
		if pr.MergedBy != "" {
			if pk, ok := s.userPK[pr.MergedBy]; ok {
				row.MergedByPK = &pk
			}
		}
		if pr.MergeCommitSHA != "" {
			row.MergeCommitSHA = &pr.MergeCommitSHA
		}
		if err := s.tx.SeedPull(s.ctx, row); err != nil {
			return fmt.Errorf("seed pull %d: %w", pr.Number, err)
		}
		s.pullPK[pr.Number] = row.PK
	}
	s.res.Rows["pull_requests"] = len(s.pullPK)
	return nil
}

func (s *seeder) seedComments() error {
	order := slices.Clone(s.c.Comments)
	slices.SortFunc(order, func(a, b Comment) int { return int(a.ID - b.ID) })
	n := 0
	for _, cm := range order {
		issuePK, ok := s.issuePK[cm.IssueNumber]
		if !ok {
			s.drop("comment", "no issue row for comment", 1)
			continue
		}
		userPK, ok := s.userPK[cm.Author]
		if !ok {
			s.drop("comment", "unknown comment author", 1)
			continue
		}
		row := &store.CommentRow{IssuePK: issuePK, UserPK: userPK, Body: cm.Body, CreatedAt: cm.CreatedAt, UpdatedAt: cm.UpdatedAt}
		if err := s.tx.SeedComment(s.ctx, row); err != nil {
			return fmt.Errorf("seed comment %d: %w", cm.ID, err)
		}
		if err := s.expandReactions("comment", row.PK, cm.Reactions, cm.CreatedAt); err != nil {
			return err
		}
		n++
	}
	s.res.Rows["comments"] = n
	return nil
}

func (s *seeder) seedReviews() error {
	order := slices.Clone(s.c.Reviews)
	slices.SortFunc(order, func(a, b Review) int { return int(a.ID - b.ID) })
	for _, r := range order {
		pullPK, ok := s.pullPK[r.PRNumber]
		if !ok {
			s.drop("review", "no pull row for review", 1)
			continue
		}
		userPK, ok := s.userPK[r.Author]
		if !ok {
			s.drop("review", "unknown review author", 1)
			continue
		}
		row := &store.ReviewRow{
			PullPK: pullPK, RepoPK: s.repoPK, UserPK: userPK, State: r.State,
			Body: r.Body, CommitID: r.CommitID, SubmittedAt: r.SubmittedAt,
			CreatedAt: userEpoch, UpdatedAt: userEpoch,
		}
		if r.SubmittedAt != nil {
			row.CreatedAt, row.UpdatedAt = *r.SubmittedAt, *r.SubmittedAt
		}
		if err := s.tx.SeedReview(s.ctx, row); err != nil {
			return fmt.Errorf("seed review %d: %w", r.ID, err)
		}
		s.reviewPK[r.ID] = row.PK
	}
	s.res.Rows["reviews"] = len(s.reviewPK)
	return nil
}

func (s *seeder) seedReviewComments() error {
	order := slices.Clone(s.c.ReviewComments)
	slices.SortFunc(order, func(a, b ReviewComment) int { return int(a.ID - b.ID) })
	for _, rc := range order {
		reviewPK, ok := s.reviewPK[rc.ReviewID]
		if !ok {
			s.drop("review_comment", "no review row for inline comment", 1)
			continue
		}
		pullPK, ok := s.pullPK[rc.PRNumber]
		if !ok {
			s.drop("review_comment", "no pull row for inline comment", 1)
			continue
		}
		userPK, ok := s.userPK[rc.Author]
		if !ok {
			s.drop("review_comment", "unknown inline-comment author", 1)
			continue
		}
		row := &store.ReviewCommentRow{
			ReviewPK: reviewPK, PullPK: pullPK, RepoPK: s.repoPK, UserPK: userPK,
			Path: rc.Path, Side: rc.Side, Line: rc.Line, DiffHunk: rc.DiffHunk,
			Body: rc.Body, CreatedAt: rc.CreatedAt, UpdatedAt: rc.UpdatedAt,
		}
		if rc.InReplyToID != nil {
			if pk, ok := s.reviewCommentPK[*rc.InReplyToID]; ok {
				row.InReplyToPK = &pk
			}
		}
		if err := s.tx.SeedReviewComment(s.ctx, row); err != nil {
			return fmt.Errorf("seed review comment %d: %w", rc.ID, err)
		}
		s.reviewCommentPK[rc.ID] = row.PK
	}
	s.res.Rows["review_comments"] = len(s.reviewCommentPK)
	return nil
}

func (s *seeder) seedTimeline() error {
	order := slices.Clone(s.c.TimelineEvents)
	slices.SortFunc(order, func(a, b TimelineEvent) int { return int(a.ID - b.ID) })
	// The timeline is the largest table in an automation-heavy corpus, so it
	// loads through the chunked bulk path. Events are leaf rows; nothing
	// references their pk, so they need neither RETURNING nor a per-row id call.
	rows := make([]store.IssueEventRow, 0, len(order))
	for _, ev := range order {
		issuePK, ok := s.issuePK[ev.IssueNumber]
		if !ok {
			s.drop("timeline_event", "no issue row for event", 1)
			continue
		}
		row := store.IssueEventRow{
			RepoPK: s.repoPK, IssuePK: issuePK, Event: ev.EventType,
			Payload: renderEventPayload(ev), CreatedAt: ev.CreatedAt,
		}
		if ev.Actor != "" {
			if pk, ok := s.userPK[ev.Actor]; ok {
				actor := pk
				row.ActorPK = &actor
			}
		}
		rows = append(rows, row)
	}
	if err := s.tx.SeedIssueEventsBulk(s.ctx, rows); err != nil {
		return fmt.Errorf("seed timeline: %w", err)
	}
	s.res.Rows["timeline_events"] = len(rows)
	return nil
}

func (s *seeder) seedStatuses() error {
	order := slices.Clone(s.c.CommitStatuses)
	slices.SortFunc(order, func(a, b CommitStatus) int {
		if a.CreatedAt.Equal(b.CreatedAt) {
			return strings.Compare(a.Context, b.Context)
		}
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	for _, st := range order {
		row := &store.CommitStatusRow{
			RepoPK: s.repoPK, SHA: st.SHA, State: st.State, Context: st.Context,
			CreatedAt: st.CreatedAt, UpdatedAt: st.CreatedAt,
		}
		if st.Description != "" {
			row.Description = &st.Description
		}
		if st.TargetURL != "" {
			row.TargetURL = &st.TargetURL
		}
		if err := s.tx.SeedCommitStatus(s.ctx, row); err != nil {
			return fmt.Errorf("seed status %s/%s: %w", st.SHA, st.Context, err)
		}
	}
	s.res.Rows["commit_statuses"] = len(order)
	return nil
}

// expandReactions materializes a content->count map into one reactions row per
// reacting user, drawn from the bounded reactor pool with a deterministic
// assignment. A content with more reactions than the pool has reactors is capped
// to the pool size and the shortfall is recorded as a drop, never silently lost.
func (s *seeder) expandReactions(subjectType string, subjectPK int64, counts map[string]int, createdAt time.Time) error {
	if len(counts) == 0 || len(s.reactorPKs) == 0 {
		return nil
	}
	// Iterate contents in the canonical reaction order so the assignment does
	// not depend on map iteration order.
	for _, content := range store.ReactionContents {
		k := counts[content]
		if k <= 0 {
			continue
		}
		if k > len(s.reactorPKs) {
			s.drop("reaction", fmt.Sprintf("%s count %d exceeds reactor pool %d", content, k, len(s.reactorPKs)), k-len(s.reactorPKs))
			k = len(s.reactorPKs)
		}
		start := int(s.reactor.Seed) + int(subjectPK) + contentSalt(content)
		for i := 0; i < k; i++ {
			idx := ((start + i) % len(s.reactorPKs))
			if idx < 0 {
				idx += len(s.reactorPKs)
			}
			s.reactionBuf = append(s.reactionBuf, store.ReactionRow{
				SubjectType: subjectType, SubjectPK: subjectPK,
				UserPK: s.reactorPKs[idx], Content: content, CreatedAt: createdAt,
			})
		}
	}
	return nil
}

// flushReactions loads the buffered reactor-pool rows through the chunked bulk
// path. The buffer was filled in issue-then-comment order with a deterministic
// reactor assignment, so the load is reproducible across runs.
func (s *seeder) flushReactions() error {
	if err := s.tx.SeedReactionsBulk(s.ctx, s.reactionBuf); err != nil {
		return fmt.Errorf("seed reactions: %w", err)
	}
	s.res.Rows["reactions"] = len(s.reactionBuf)
	return nil
}

func (s *seeder) drop(what, reason string, count int) {
	s.res.Dropped = append(s.res.Dropped, DropNote{What: what, Count: count, Reason: reason})
}

// contentSalt spreads reactor assignment across contents so two contents on the
// same subject do not draw the identical reactor set.
func contentSalt(content string) int {
	n := 0
	for _, r := range content {
		n = n*31 + int(r)
	}
	return n
}

// isBot recognizes the automation accounts that dominate an automation-heavy
// repo's timeline, so they seed with type Bot the way GitHub marks them.
func isBot(login string) bool {
	return strings.HasSuffix(login, "-bot") || strings.HasSuffix(login, "[bot]") ||
		strings.HasSuffix(login, "-ci-robot") || login == "k8s-ci-robot"
}

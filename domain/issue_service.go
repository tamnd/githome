package domain

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tamnd/githome/store"
	"github.com/tamnd/githome/worker"
)

// The issue service errors the REST and GraphQL layers map to status. An issue
// in an invisible repository is reported as not found through the repository
// resolution, so the repo's existence never leaks; a missing issue in a visible
// repository is ErrIssueNotFound (404). A write the actor may not perform is
// ErrForbidden (403, reusing the git-write sentinel); a malformed edit is
// ErrValidation (422).
var (
	// ErrIssueNotFound is returned when no issue matches the lookup in a visible
	// repository.
	ErrIssueNotFound = errors.New("domain: issue not found")

	// ErrCommentNotFound is returned when no comment matches the lookup.
	ErrCommentNotFound = errors.New("domain: comment not found")

	// ErrLabelNotFound is returned when no label matches the lookup.
	ErrLabelNotFound = errors.New("domain: label not found")

	// ErrMilestoneNotFound is returned when no milestone matches the lookup.
	ErrMilestoneNotFound = errors.New("domain: milestone not found")

	// ErrValidation is returned for an edit that violates a field rule (an empty
	// title, an unknown state, a bad reaction content).
	ErrValidation = errors.New("domain: validation failed")

	// ErrLabelExists is returned by CreateLabel when the name is already taken.
	ErrLabelExists = errors.New("domain: label already exists")
)

// reactionContents is the set of reaction names the create paths accept.
var reactionContents = map[string]bool{
	"+1": true, "-1": true, "laugh": true, "confused": true,
	"heart": true, "hooray": true, "rocket": true, "eyes": true,
}

// IssueStore is the slice of the store the issue service needs: the issue,
// comment, label, milestone, and reaction reads and writes, the transaction
// entry point the multi-statement writes run through, the user and repository
// lookups it resolves participants and locations with, and the job enqueue it
// records the webhook event through.
type IssueStore interface {
	UserByPK(ctx context.Context, pk int64) (*store.UserRow, error)
	UserByLogin(ctx context.Context, login string) (*store.UserRow, error)

	GetIssueByNumber(ctx context.Context, repoPK, number int64) (*store.IssueRow, error)
	GetIssueByPK(ctx context.Context, pk int64) (*store.IssueRow, error)
	GetIssueByDBID(ctx context.Context, dbID int64) (*store.IssueRow, error)
	ListIssues(ctx context.Context, repoPK int64, f store.IssueFilter) ([]store.IssueRow, error)
	ListIssuesPage(ctx context.Context, repoPK int64, f store.IssueFilter) ([]store.IssueRow, bool, error)
	CountIssues(ctx context.Context, repoPK int64, f store.IssueFilter) (int, error)
	LabelsByIssue(ctx context.Context, issuePK int64) ([]store.LabelRow, error)
	ListAssigneePKs(ctx context.Context, issuePK int64) ([]int64, error)
	LabelsByIssuePKs(ctx context.Context, issuePKs []int64) (map[int64][]store.LabelRow, error)
	AssigneesByIssuePKs(ctx context.Context, issuePKs []int64) (map[int64][]int64, error)
	UsersByPKs(ctx context.Context, pks []int64) (map[int64]*store.UserRow, error)
	MilestonesByPKs(ctx context.Context, pks []int64) (map[int64]*store.MilestoneRow, error)
	ReactionRollupsBySubjectPKs(ctx context.Context, subjectType string, subjectPKs []int64) (map[int64]store.ReactionRollup, error)

	ListLabels(ctx context.Context, repoPK int64) ([]store.LabelRow, error)
	GetLabel(ctx context.Context, repoPK int64, name string) (*store.LabelRow, error)
	GetLabelByDBID(ctx context.Context, dbID int64) (*store.LabelRow, error)
	LabelsByNames(ctx context.Context, repoPK int64, names []string) ([]store.LabelRow, error)
	InsertLabel(ctx context.Context, l *store.LabelRow) error
	UpdateLabel(ctx context.Context, l *store.LabelRow) error
	DeleteLabel(ctx context.Context, pk int64) error

	ListMilestones(ctx context.Context, repoPK int64, state string) ([]store.MilestoneRow, error)
	GetMilestoneByNumber(ctx context.Context, repoPK, number int64) (*store.MilestoneRow, error)
	GetMilestoneByPK(ctx context.Context, pk int64) (*store.MilestoneRow, error)
	InsertMilestone(ctx context.Context, m *store.MilestoneRow) error
	UpdateMilestone(ctx context.Context, m *store.MilestoneRow) error
	DeleteMilestone(ctx context.Context, pk int64) error
	MilestoneIssueCounts(ctx context.Context, milestonePK int64) (open, closed int, err error)
	MilestoneIssueCountsByPKs(ctx context.Context, pks []int64) (map[int64]store.MilestoneCount, error)

	ListIssueComments(ctx context.Context, issuePK int64, limit, offset int) ([]store.CommentRow, error)
	GetComment(ctx context.Context, dbID int64) (*store.CommentRow, error)
	UpdateComment(ctx context.Context, c *store.CommentRow) error
	DeleteComment(ctx context.Context, pk int64) error

	ReactionRollupFor(ctx context.Context, subjectType string, subjectPK int64) (store.ReactionRollup, error)
	ListReactions(ctx context.Context, subjectType string, subjectPK int64) ([]store.ReactionRow, error)
	InsertReaction(ctx context.Context, r *store.ReactionRow) (bool, error)
	DeleteReaction(ctx context.Context, subjectType string, subjectPK, dbID int64) error

	WithTx(ctx context.Context, fn func(*store.Tx) error) error
	EnqueueJob(ctx context.Context, j *store.JobRow) (bool, error)
	InsertEvent(ctx context.Context, e *store.EventRow) error
}

// IssueService implements the issue subsystem over the store, reusing the repo
// service for repository resolution so the visibility and write rules stay in
// one place. Writes that touch several rows run inside one transaction; a create
// also records an `issues` webhook event in the durable queue, delivered when
// the webhook milestone lands.
type IssueService struct {
	store IssueStore
	repos *RepoService
	enq   worker.Enqueuer
}

// NewIssueService builds an IssueService over the store and the repo service.
func NewIssueService(st IssueStore, repos *RepoService) *IssueService {
	return &IssueService{store: st, repos: repos, enq: worker.NewStoreEnqueuer(st)}
}

// IssueInput is the create payload: a title, an optional body, and the labels,
// assignees, and milestone to attach. Labels and assignees are named (label
// names, user logins) the way the REST and GraphQL inputs name them; unknown
// names are skipped, matching GitHub.
type IssueInput struct {
	Title           string
	Body            *string
	Labels          []string
	AssigneeLogins  []string
	MilestoneNumber *int64
}

// IssuePatch is the edit payload. A nil field is left unchanged; a non-nil field
// is written. State moves through Open/Closed and carries StateReason
// (completed, not_planned, reopened).
type IssuePatch struct {
	Title           *string
	Body            *string
	State           *string
	StateReason     *string
	Labels          *[]string
	AssigneeLogins  *[]string
	MilestoneNumber *int64 // a pointer-to-zero clears the milestone
	ClearMilestone  bool
}

// IssueQuery narrows the list endpoint.
type IssueQuery struct {
	State           string
	Labels          []string
	CreatorLogin    string
	AssigneeLogin   string
	MilestoneNumber *int64
	Sort            string
	Direction       string
	Page            int
	PerPage         int
	// Cursor is an opaque token from the previous page's Link header. When set
	// and the sort is "created" DESC (the default), the store uses a keyset
	// seek instead of OFFSET so deep pages are O(1) in depth.
	Cursor string
}

// CreateIssue opens an issue in the repository after authorizing write access.
// It allocates the per-repo number, inserts the row, attaches the resolved
// labels and assignees, bumps the repository's open-issue count, and enqueues
// the `issues` opened event, all in one transaction plus the durable enqueue.
func (s *IssueService) CreateIssue(ctx context.Context, actorPK int64, owner, name string, in IssueInput) (*Issue, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return nil, ErrValidation
	}
	labels, err := s.store.LabelsByNames(ctx, repo.PK, in.Labels)
	if err != nil {
		return nil, err
	}
	assigneePKs, err := s.resolveLogins(ctx, in.AssigneeLogins)
	if err != nil {
		return nil, err
	}
	var milestonePK *int64
	if in.MilestoneNumber != nil {
		m, err := s.store.GetMilestoneByNumber(ctx, repo.PK, *in.MilestoneNumber)
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrMilestoneNotFound
		}
		if err != nil {
			return nil, err
		}
		milestonePK = &m.PK
	}

	row := &store.IssueRow{
		RepoPK:      repo.PK,
		UserPK:      actorPK,
		Title:       strings.TrimSpace(in.Title),
		Body:        in.Body,
		State:       "open",
		MilestonePK: milestonePK,
	}
	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		n, err := tx.AllocIssueNumber(ctx, repo.PK)
		if err != nil {
			return err
		}
		row.Number = n
		if err := tx.InsertIssue(ctx, row); err != nil {
			return err
		}
		if err := tx.AttachLabels(ctx, row.PK, labelPKs(labels)); err != nil {
			return err
		}
		if err := tx.AddAssignees(ctx, row.PK, assigneePKs); err != nil {
			return err
		}
		return tx.AdjustOpenIssuesCount(ctx, repo.PK, 1)
	})
	if err != nil {
		return nil, err
	}
	s.recordIssueEvent(ctx, actorPK, EventIssues, "opened", repo, row.PK)
	return s.assembleIssue(ctx, repo, row)
}

// GetIssue resolves one issue by number for the viewer.
func (s *IssueService) GetIssue(ctx context.Context, viewerPK int64, owner, name string, number int64) (*Issue, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetIssueByNumber(ctx, repo.PK, number)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrIssueNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.assembleIssue(ctx, repo, row)
}

// IssueForEvent assembles an issue by internal pk for the webhook renderer. The
// repository is already resolved by the caller; no visibility check applies
// because the event was authorized when it was recorded.
func (s *IssueService) IssueForEvent(ctx context.Context, repo *Repo, issuePK int64) (*Issue, error) {
	row, err := s.store.GetIssueByPK(ctx, issuePK)
	if err != nil {
		return nil, err
	}
	return s.assembleIssue(ctx, repo, row)
}

// IssueRef resolves an issue's public database id to the owner login,
// repository name, and per-repo number, the coordinates the write methods take.
// The GraphQL mutations decode an issue node id to its database id and resolve
// it here. It does not authorize: the write method the caller invokes next
// (EditIssue, CreateComment) enforces write access and repository visibility.
func (s *IssueService) IssueRef(ctx context.Context, issueDBID int64) (owner, name string, number int64, err error) {
	row, err := s.store.GetIssueByDBID(ctx, issueDBID)
	if errors.Is(err, store.ErrNotFound) {
		return "", "", 0, ErrIssueNotFound
	}
	if err != nil {
		return "", "", 0, err
	}
	repoRow, err := s.repos.store.RepoByPK(ctx, row.RepoPK)
	if errors.Is(err, store.ErrNotFound) {
		return "", "", 0, ErrIssueNotFound
	}
	if err != nil {
		return "", "", 0, err
	}
	ownerRow, err := s.repos.store.UserByPK(ctx, repoRow.OwnerPK)
	if err != nil {
		return "", "", 0, err
	}
	return ownerRow.Login, repoRow.Name, row.Number, nil
}

// ListIssues returns a page of the repository's issues plus the total matching
// the filter, for the pagination headers.
func (s *IssueService) ListIssues(ctx context.Context, viewerPK int64, owner, name string, q IssueQuery) ([]*Issue, int, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, 0, err
	}
	f, err := s.buildFilter(ctx, repo, q)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.store.ListIssues(ctx, repo.PK, f)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.store.CountIssues(ctx, repo.PK, f)
	if err != nil {
		return nil, 0, err
	}
	out, err := s.assembleIssues(ctx, repo, rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ListIssuesPage returns a keyset-paginated page of the repository's issues plus
// whether a further page exists, without the COUNT that ListIssues runs for the
// page-number Link header. It is the flat read path for cursor walks: deep pages
// of a several-hundred-thousand-issue repo cost the page, not a full count plus
// a deep OFFSET scan. The caller routes here only when the cursor decoded, so
// the filter is keyset-eligible.
func (s *IssueService) ListIssuesPage(ctx context.Context, viewerPK int64, owner, name string, q IssueQuery) ([]*Issue, bool, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, false, err
	}
	f, err := s.buildFilter(ctx, repo, q)
	if err != nil {
		return nil, false, err
	}
	rows, hasMore, err := s.store.ListIssuesPage(ctx, repo.PK, f)
	if err != nil {
		return nil, false, err
	}
	out, err := s.assembleIssues(ctx, repo, rows)
	if err != nil {
		return nil, false, err
	}
	return out, hasMore, nil
}

// EditIssue applies a patch to an issue under the optimistic lock, retrying once
// on a lost race. It writes the state transition (stamping or clearing closed_at
// and closed_by), replaces labels and assignees when the patch names them,
// adjusts the open-issue count on a state change, records a timeline event for
// the transition, and enqueues the matching `issues` action.
func (s *IssueService) EditIssue(ctx context.Context, actorPK int64, owner, name string, number int64, p IssuePatch) (*Issue, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	if p.State != nil && *p.State != "open" && *p.State != "closed" {
		return nil, ErrValidation
	}
	if p.Title != nil && strings.TrimSpace(*p.Title) == "" {
		return nil, ErrValidation
	}

	var (
		action    string
		labels    []store.LabelRow
		assignees []int64
		setLabels bool
		setAssign bool
	)
	if p.Labels != nil {
		setLabels = true
		if labels, err = s.store.LabelsByNames(ctx, repo.PK, *p.Labels); err != nil {
			return nil, err
		}
	}
	if p.AssigneeLogins != nil {
		setAssign = true
		if assignees, err = s.resolveLogins(ctx, *p.AssigneeLogins); err != nil {
			return nil, err
		}
	}

	row, err := s.store.GetIssueByNumber(ctx, repo.PK, number)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrIssueNotFound
	}
	if err != nil {
		return nil, err
	}

	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		openDelta := 0
		if p.Title != nil {
			row.Title = strings.TrimSpace(*p.Title)
		}
		if p.Body != nil {
			row.Body = p.Body
		}
		if p.ClearMilestone {
			row.MilestonePK = nil
		} else if p.MilestoneNumber != nil {
			m, err := s.store.GetMilestoneByNumber(ctx, repo.PK, *p.MilestoneNumber)
			if errors.Is(err, store.ErrNotFound) {
				return ErrMilestoneNotFound
			}
			if err != nil {
				return err
			}
			row.MilestonePK = &m.PK
		}
		if p.State != nil && *p.State != row.State {
			switch *p.State {
			case "closed":
				row.State = "closed"
				now := nowUTC()
				row.ClosedAt = &now
				row.ClosedByPK = &actorPK
				reason := "completed"
				if p.StateReason != nil {
					reason = *p.StateReason
				}
				row.StateReason = &reason
				openDelta = -1
				action = "closed"
			case "open":
				row.State = "open"
				row.ClosedAt = nil
				row.ClosedByPK = nil
				reason := "reopened"
				row.StateReason = &reason
				openDelta = 1
				action = "reopened"
			}
		} else if p.StateReason != nil {
			row.StateReason = p.StateReason
		}

		if err := tx.UpdateIssue(ctx, row); err != nil {
			return err
		}
		if setLabels {
			if err := tx.ReplaceLabels(ctx, row.PK, labelPKs(labels)); err != nil {
				return err
			}
		}
		if setAssign {
			if err := tx.ReplaceAssignees(ctx, row.PK, assignees); err != nil {
				return err
			}
		}
		if action != "" {
			if err := tx.InsertIssueEvent(ctx, &store.IssueEventRow{
				RepoPK: repo.PK, IssuePK: row.PK, ActorPK: &actorPK, Event: action,
			}); err != nil {
				return err
			}
			if err := tx.AdjustOpenIssuesCount(ctx, repo.PK, openDelta); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, store.ErrOptimisticLock) {
		// A concurrent writer moved first; re-read and retry once.
		return s.EditIssue(ctx, actorPK, owner, name, number, p)
	}
	if err != nil {
		return nil, err
	}
	if action == "" {
		action = "edited"
	}
	s.recordIssueEvent(ctx, actorPK, EventIssues, action, repo, row.PK)
	return s.assembleIssue(ctx, repo, row)
}

// resolveLogins maps user logins to internal pks, skipping logins that do not
// resolve, the way GitHub silently drops assignees who are not collaborators.
func (s *IssueService) resolveLogins(ctx context.Context, logins []string) ([]int64, error) {
	out := make([]int64, 0, len(logins))
	seen := map[int64]bool{}
	for _, login := range logins {
		u, err := s.store.UserByLogin(ctx, login)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !seen[u.PK] {
			out = append(out, u.PK)
			seen[u.PK] = true
		}
	}
	return out, nil
}

// buildFilter translates the domain query into the store filter, resolving the
// creator and assignee logins and the milestone number. An unresolvable creator
// or assignee yields a filter that matches nothing.
func (s *IssueService) buildFilter(ctx context.Context, repo *Repo, q IssueQuery) (store.IssueFilter, error) {
	f := store.IssueFilter{
		State:     q.State,
		Labels:    q.Labels,
		Sort:      q.Sort,
		Direction: q.Direction,
		Limit:     q.PerPage,
		Offset:    offsetFor(q.Page, q.PerPage),
	}
	if q.CreatorLogin != "" {
		u, err := s.store.UserByLogin(ctx, q.CreatorLogin)
		if errors.Is(err, store.ErrNotFound) {
			return matchNothing(f), nil
		}
		if err != nil {
			return f, err
		}
		f.CreatorPK = &u.PK
	}
	if q.AssigneeLogin != "" {
		u, err := s.store.UserByLogin(ctx, q.AssigneeLogin)
		if errors.Is(err, store.ErrNotFound) {
			return matchNothing(f), nil
		}
		if err != nil {
			return f, err
		}
		f.AssigneePK = &u.PK
	}
	if q.MilestoneNumber != nil {
		m, err := s.store.GetMilestoneByNumber(ctx, repo.PK, *q.MilestoneNumber)
		if errors.Is(err, store.ErrNotFound) {
			return matchNothing(f), nil
		}
		if err != nil {
			return f, err
		}
		f.MilestonePK = &m.PK
	}
	// Decode the opaque cursor from the REST layer. A malformed cursor is
	// silently ignored and falls back to the OFFSET path, so a corrupted URL
	// still returns a (possibly incorrect) page rather than a hard error.
	if q.Cursor != "" {
		if cur, err := store.DecodeCursor(q.Cursor); err == nil {
			f.Cursor = &cur
		}
	}
	return f, nil
}

// matchNothing forces an empty result by filtering on an impossible creator pk,
// used when a named filter does not resolve to an account.
func matchNothing(f store.IssueFilter) store.IssueFilter {
	var impossible int64 = -1
	f.CreatorPK = &impossible
	return f
}

// assembleIssues batch-loads all ancillary data for a page of issue rows in
// five round trips (users, labels, assignees, milestones, reactions) instead of
// N×5, then assembles each row using the pre-loaded maps. Follows the pattern
// established in domain/event.go.
func (s *IssueService) assembleIssues(ctx context.Context, repo *Repo, rows []store.IssueRow) ([]*Issue, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	// Collect the unique PKs we need across the whole page.
	issuePKs := make([]int64, len(rows))
	userPKSet := map[int64]struct{}{}
	milestonePKSet := map[int64]struct{}{}
	for i := range rows {
		issuePKs[i] = rows[i].PK
		userPKSet[rows[i].UserPK] = struct{}{}
		if rows[i].MilestonePK != nil {
			milestonePKSet[*rows[i].MilestonePK] = struct{}{}
		}
		if rows[i].ClosedByPK != nil {
			userPKSet[*rows[i].ClosedByPK] = struct{}{}
		}
	}

	// Batch-load labels and assignees (keyed by issue PK).
	labelMap, err := s.store.LabelsByIssuePKs(ctx, issuePKs)
	if err != nil {
		return nil, err
	}
	assigneeMap, err := s.store.AssigneesByIssuePKs(ctx, issuePKs)
	if err != nil {
		return nil, err
	}

	// Collect assignee user PKs discovered from the assignee map.
	for _, pks := range assigneeMap {
		for _, pk := range pks {
			userPKSet[pk] = struct{}{}
		}
	}

	// Flatten sets into slices for the batch loaders.
	userPKs := make([]int64, 0, len(userPKSet))
	for pk := range userPKSet {
		userPKs = append(userPKs, pk)
	}
	milestonePKs := make([]int64, 0, len(milestonePKSet))
	for pk := range milestonePKSet {
		milestonePKs = append(milestonePKs, pk)
	}

	// Batch-load users, milestones, and reaction rollups.
	userMap, err := s.store.UsersByPKs(ctx, userPKs)
	if err != nil {
		return nil, err
	}
	milestoneMap, err := s.store.MilestonesByPKs(ctx, milestonePKs)
	if err != nil {
		return nil, err
	}
	milestoneCountMap, err := s.store.MilestoneIssueCountsByPKs(ctx, milestonePKs)
	if err != nil {
		return nil, err
	}
	rollupMap, err := s.store.ReactionRollupsBySubjectPKs(ctx, "issue", issuePKs)
	if err != nil {
		return nil, err
	}

	// Assemble each issue from the pre-loaded maps.
	out := make([]*Issue, 0, len(rows))
	for i := range rows {
		row := &rows[i]

		var author *User
		if u, ok := userMap[row.UserPK]; ok {
			author = userFromRow(u)
		}

		assigneePKs := assigneeMap[row.PK]
		assignees := make([]*User, 0, len(assigneePKs))
		for _, pk := range assigneePKs {
			if u, ok := userMap[pk]; ok {
				assignees = append(assignees, userFromRow(u))
			}
		}

		var milestone *Milestone
		if row.MilestonePK != nil {
			if mr, ok := milestoneMap[*row.MilestonePK]; ok {
				// milestone creator may or may not be in the user map; fall back to a
				// direct load only if we missed them (uncommon: milestone creator not
				// also an issue author/assignee on this page).
				var creator *User
				if mr.CreatorPK != nil {
					if cu, ok := userMap[*mr.CreatorPK]; ok {
						creator = userFromRow(cu)
					} else {
						cu2, err := s.store.UserByPK(ctx, *mr.CreatorPK)
						if err == nil {
							creator = userFromRow(cu2)
						}
					}
				}
				cnt := milestoneCountMap[mr.PK]
				milestone = &Milestone{
					ID: mr.DBID, Number: mr.Number, Title: mr.Title,
					Description: mr.Description, State: mr.State, Creator: creator,
					OpenIssues: cnt.Open, ClosedIssues: cnt.Closed,
					DueOn: mr.DueOn, ClosedAt: mr.ClosedAt,
					CreatedAt: mr.CreatedAt, UpdatedAt: mr.UpdatedAt,
				}
			}
		}

		var closedBy *User
		if row.ClosedByPK != nil {
			if u, ok := userMap[*row.ClosedByPK]; ok {
				closedBy = userFromRow(u)
			}
		}

		roll := rollupMap[row.PK]

		out = append(out, &Issue{
			PK:            row.PK,
			ID:            row.DBID,
			RepoPK:        repo.PK,
			RepoID:        repo.ID,
			Number:        row.Number,
			Title:         row.Title,
			Body:          row.Body,
			State:         row.State,
			StateReason:   row.StateReason,
			Locked:        row.Locked,
			User:          author,
			UserPK:        row.UserPK,
			Assignees:     assignees,
			Labels:        labelsFromRows(labelMap[row.PK]),
			Milestone:     milestone,
			ClosedBy:      closedBy,
			Reactions:     rollup(roll),
			CommentsCount: row.CommentsCount,
			ClosedAt:      row.ClosedAt,
			CreatedAt:     row.CreatedAt,
			UpdatedAt:     row.UpdatedAt,
			lockVersion:   row.LockVersion,
		})
	}
	return out, nil
}

// assembleIssue resolves the author, assignees, labels, milestone, closer, and
// reaction rollup for an issue row into the domain Issue.
func (s *IssueService) assembleIssue(ctx context.Context, repo *Repo, row *store.IssueRow) (*Issue, error) {
	author, err := s.userByPK(ctx, row.UserPK)
	if err != nil {
		return nil, err
	}
	labelRows, err := s.store.LabelsByIssue(ctx, row.PK)
	if err != nil {
		return nil, err
	}
	assigneePKs, err := s.store.ListAssigneePKs(ctx, row.PK)
	if err != nil {
		return nil, err
	}
	assignees := make([]*User, 0, len(assigneePKs))
	for _, pk := range assigneePKs {
		u, err := s.userByPK(ctx, pk)
		if err != nil {
			return nil, err
		}
		assignees = append(assignees, u)
	}
	var milestone *Milestone
	if row.MilestonePK != nil {
		mr, err := s.store.GetMilestoneByPK(ctx, *row.MilestonePK)
		if err == nil {
			milestone, err = s.assembleMilestone(ctx, mr)
			if err != nil {
				return nil, err
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	var closedBy *User
	if row.ClosedByPK != nil {
		if closedBy, err = s.userByPK(ctx, *row.ClosedByPK); err != nil {
			return nil, err
		}
	}
	roll, err := s.store.ReactionRollupFor(ctx, "issue", row.PK)
	if err != nil {
		return nil, err
	}

	iss := &Issue{
		PK:            row.PK,
		ID:            row.DBID,
		RepoPK:        repo.PK,
		RepoID:        repo.ID,
		Number:        row.Number,
		Title:         row.Title,
		Body:          row.Body,
		State:         row.State,
		StateReason:   row.StateReason,
		Locked:        row.Locked,
		User:          author,
		UserPK:        row.UserPK,
		Assignees:     assignees,
		Labels:        labelsFromRows(labelRows),
		Milestone:     milestone,
		ClosedBy:      closedBy,
		Reactions:     rollup(roll),
		CommentsCount: row.CommentsCount,
		ClosedAt:      row.ClosedAt,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
		lockVersion:   row.LockVersion,
	}
	return iss, nil
}

// assembleIssueSearch batch-assembles a page of search results that can span
// multiple repositories. It uses the same five-round-trip pattern as
// assembleIssues but accepts a pre-loaded repo map keyed by repoPK, so each
// row resolves its repository without an extra query.
func (s *IssueService) assembleIssueSearch(ctx context.Context, repoMap map[int64]*Repo, rows []store.IssueRow) ([]*Issue, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	issuePKs := make([]int64, len(rows))
	userPKSet := map[int64]struct{}{}
	milestonePKSet := map[int64]struct{}{}
	for i := range rows {
		issuePKs[i] = rows[i].PK
		userPKSet[rows[i].UserPK] = struct{}{}
		if rows[i].MilestonePK != nil {
			milestonePKSet[*rows[i].MilestonePK] = struct{}{}
		}
		if rows[i].ClosedByPK != nil {
			userPKSet[*rows[i].ClosedByPK] = struct{}{}
		}
	}

	labelMap, err := s.store.LabelsByIssuePKs(ctx, issuePKs)
	if err != nil {
		return nil, err
	}
	assigneeMap, err := s.store.AssigneesByIssuePKs(ctx, issuePKs)
	if err != nil {
		return nil, err
	}
	for _, pks := range assigneeMap {
		for _, pk := range pks {
			userPKSet[pk] = struct{}{}
		}
	}

	userPKs := make([]int64, 0, len(userPKSet))
	for pk := range userPKSet {
		userPKs = append(userPKs, pk)
	}
	milestonePKs := make([]int64, 0, len(milestonePKSet))
	for pk := range milestonePKSet {
		milestonePKs = append(milestonePKs, pk)
	}

	userMap, err := s.store.UsersByPKs(ctx, userPKs)
	if err != nil {
		return nil, err
	}
	milestoneMap, err := s.store.MilestonesByPKs(ctx, milestonePKs)
	if err != nil {
		return nil, err
	}
	milestoneCountMap, err := s.store.MilestoneIssueCountsByPKs(ctx, milestonePKs)
	if err != nil {
		return nil, err
	}
	rollupMap, err := s.store.ReactionRollupsBySubjectPKs(ctx, "issue", issuePKs)
	if err != nil {
		return nil, err
	}

	out := make([]*Issue, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		repo := repoMap[row.RepoPK]
		if repo == nil {
			continue
		}

		var author *User
		if u, ok := userMap[row.UserPK]; ok {
			author = userFromRow(u)
		}

		assigneePKs := assigneeMap[row.PK]
		assignees := make([]*User, 0, len(assigneePKs))
		for _, pk := range assigneePKs {
			if u, ok := userMap[pk]; ok {
				assignees = append(assignees, userFromRow(u))
			}
		}

		var milestone *Milestone
		if row.MilestonePK != nil {
			if mr, ok := milestoneMap[*row.MilestonePK]; ok {
				var creator *User
				if mr.CreatorPK != nil {
					if cu, ok := userMap[*mr.CreatorPK]; ok {
						creator = userFromRow(cu)
					} else {
						cu2, err := s.store.UserByPK(ctx, *mr.CreatorPK)
						if err == nil {
							creator = userFromRow(cu2)
						}
					}
				}
				cnt := milestoneCountMap[mr.PK]
				milestone = &Milestone{
					ID: mr.DBID, Number: mr.Number, Title: mr.Title,
					Description: mr.Description, State: mr.State, Creator: creator,
					OpenIssues: cnt.Open, ClosedIssues: cnt.Closed,
					DueOn: mr.DueOn, ClosedAt: mr.ClosedAt,
					CreatedAt: mr.CreatedAt, UpdatedAt: mr.UpdatedAt,
				}
			}
		}

		var closedBy *User
		if row.ClosedByPK != nil {
			if u, ok := userMap[*row.ClosedByPK]; ok {
				closedBy = userFromRow(u)
			}
		}

		roll := rollupMap[row.PK]

		out = append(out, &Issue{
			PK:            row.PK,
			ID:            row.DBID,
			RepoPK:        repo.PK,
			RepoID:        repo.ID,
			Number:        row.Number,
			Title:         row.Title,
			Body:          row.Body,
			State:         row.State,
			StateReason:   row.StateReason,
			Locked:        row.Locked,
			User:          author,
			UserPK:        row.UserPK,
			Assignees:     assignees,
			Labels:        labelsFromRows(labelMap[row.PK]),
			Milestone:     milestone,
			ClosedBy:      closedBy,
			Reactions:     rollup(roll),
			CommentsCount: row.CommentsCount,
			ClosedAt:      row.ClosedAt,
			CreatedAt:     row.CreatedAt,
			UpdatedAt:     row.UpdatedAt,
			lockVersion:   row.LockVersion,
		})
	}
	return out, nil
}

func (s *IssueService) assembleMilestone(ctx context.Context, mr *store.MilestoneRow) (*Milestone, error) {
	open, closed, err := s.store.MilestoneIssueCounts(ctx, mr.PK)
	if err != nil {
		return nil, err
	}
	var creator *User
	if mr.CreatorPK != nil {
		if creator, err = s.userByPK(ctx, *mr.CreatorPK); err != nil {
			return nil, err
		}
	}
	return &Milestone{
		ID:           mr.DBID,
		Number:       mr.Number,
		Title:        mr.Title,
		Description:  mr.Description,
		State:        mr.State,
		Creator:      creator,
		OpenIssues:   open,
		ClosedIssues: closed,
		DueOn:        mr.DueOn,
		ClosedAt:     mr.ClosedAt,
		CreatedAt:    mr.CreatedAt,
		UpdatedAt:    mr.UpdatedAt,
	}, nil
}

func (s *IssueService) userByPK(ctx context.Context, pk int64) (*User, error) {
	row, err := s.store.UserByPK(ctx, pk)
	if err != nil {
		return nil, err
	}
	return userFromRow(row), nil
}

// recordIssueEvent appends an activity event for an issue action and enqueues
// its webhook fan-out. The actor, the repository, and the issue are the
// coordinates the renderer rebuilds the payload from; delivery is best-effort,
// so a failure here never fails the user's write.
func (s *IssueService) recordIssueEvent(ctx context.Context, actorPK int64, event, action string, repo *Repo, issuePK int64) {
	pk := issuePK
	recordEvent(ctx, s.store, s.enq, &store.EventRow{
		Event:   event,
		Action:  action,
		ActorPK: actorPK,
		RepoPK:  repo.PK,
		IssuePK: &pk,
		Public:  !repo.Private,
	}, nil)
}

func offsetFor(page, perPage int) int {
	if page <= 1 {
		return 0
	}
	if perPage <= 0 {
		perPage = 30
	}
	return (page - 1) * perPage
}

func labelPKs(rows []store.LabelRow) []int64 {
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.PK
	}
	return out
}

func labelsFromRows(rows []store.LabelRow) []*Label {
	out := make([]*Label, 0, len(rows))
	for i := range rows {
		out = append(out, labelFromRow(&rows[i]))
	}
	return out
}

func labelFromRow(r *store.LabelRow) *Label {
	return &Label{
		ID:          r.DBID,
		Name:        r.Name,
		Color:       r.Color,
		Description: r.Description,
		Default:     r.IsDefault,
	}
}

func rollup(r store.ReactionRollup) ReactionRollup {
	counts := r.Counts
	if counts == nil {
		counts = map[string]int{}
	}
	return ReactionRollup{TotalCount: r.TotalCount, Counts: counts}
}

// AddLabels attaches the given label names to an issue, ignoring any that are
// already attached or do not exist. It is the additive counterpart to the full
// replacement that EditIssue performs when Labels is set.
func (s *IssueService) AddLabels(ctx context.Context, actorPK int64, owner, name string, number int64, labelNames []string) (*Issue, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetIssueByNumber(ctx, repo.PK, number)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrIssueNotFound
	}
	if err != nil {
		return nil, err
	}
	labels, err := s.store.LabelsByNames(ctx, repo.PK, labelNames)
	if err != nil {
		return nil, err
	}
	if err := s.store.WithTx(ctx, func(tx *store.Tx) error {
		return tx.AddLabels(ctx, row.PK, labelPKs(labels))
	}); err != nil {
		return nil, err
	}
	return s.assembleIssue(ctx, repo, row)
}

// RemoveLabels detaches the given label names from an issue, ignoring any that
// are not currently attached.
func (s *IssueService) RemoveLabels(ctx context.Context, actorPK int64, owner, name string, number int64, labelNames []string) (*Issue, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetIssueByNumber(ctx, repo.PK, number)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrIssueNotFound
	}
	if err != nil {
		return nil, err
	}
	labels, err := s.store.LabelsByNames(ctx, repo.PK, labelNames)
	if err != nil {
		return nil, err
	}
	if err := s.store.WithTx(ctx, func(tx *store.Tx) error {
		return tx.RemoveLabels(ctx, row.PK, labelPKs(labels))
	}); err != nil {
		return nil, err
	}
	return s.assembleIssue(ctx, repo, row)
}

// AddAssignees links the given user logins to an issue's assignee list, ignoring
// logins that are not known.
func (s *IssueService) AddAssignees(ctx context.Context, actorPK int64, owner, name string, number int64, logins []string) (*Issue, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetIssueByNumber(ctx, repo.PK, number)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrIssueNotFound
	}
	if err != nil {
		return nil, err
	}
	assignees, err := s.resolveLogins(ctx, logins)
	if err != nil {
		return nil, err
	}
	if err := s.store.WithTx(ctx, func(tx *store.Tx) error {
		return tx.AddAssignees(ctx, row.PK, assignees)
	}); err != nil {
		return nil, err
	}
	return s.assembleIssue(ctx, repo, row)
}

// RemoveAssignees unlinks the given user logins from an issue's assignee list.
func (s *IssueService) RemoveAssignees(ctx context.Context, actorPK int64, owner, name string, number int64, logins []string) (*Issue, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetIssueByNumber(ctx, repo.PK, number)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrIssueNotFound
	}
	if err != nil {
		return nil, err
	}
	assignees, err := s.resolveLogins(ctx, logins)
	if err != nil {
		return nil, err
	}
	if err := s.store.WithTx(ctx, func(tx *store.Tx) error {
		return tx.RemoveAssignees(ctx, row.PK, assignees)
	}); err != nil {
		return nil, err
	}
	return s.assembleIssue(ctx, repo, row)
}

// LabelNameByDBID resolves a label's name by its public database ID. The caller
// uses it to convert a GraphQL label node ID (which carries the DB ID) back to
// a name before calling add/remove/replace label methods.
func (s *IssueService) LabelNameByDBID(ctx context.Context, dbID int64) (string, error) {
	row, err := s.store.GetLabelByDBID(ctx, dbID)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrLabelNotFound
	}
	if err != nil {
		return "", err
	}
	return row.Name, nil
}

// LabelRepoRef resolves a label by its public database ID and returns the
// label name together with the owner login and repository name. Used by the
// GraphQL deleteLabel and updateLabel mutations which receive a label node ID.
func (s *IssueService) LabelRepoRef(ctx context.Context, dbID int64) (name, owner, repoName string, err error) {
	row, err := s.store.GetLabelByDBID(ctx, dbID)
	if errors.Is(err, store.ErrNotFound) {
		return "", "", "", ErrLabelNotFound
	}
	if err != nil {
		return "", "", "", err
	}
	repoRow, err := s.repos.GetRepoByPK(ctx, 0, row.RepoPK)
	if err != nil {
		return "", "", "", err
	}
	return row.Name, repoRow.Owner.Login, repoRow.Name, nil
}

// UserLoginByPK resolves a user's login by their internal primary key.
func (s *IssueService) UserLoginByPK(ctx context.Context, pk int64) (string, error) {
	row, err := s.store.UserByPK(ctx, pk)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrUserNotFound
	}
	if err != nil {
		return "", err
	}
	return row.Login, nil
}

// nowUTCFunc lets tests pin the clock for the close-transition timestamp; the
// default is the wall clock in UTC.
var nowUTC = func() time.Time { return time.Now().UTC() }

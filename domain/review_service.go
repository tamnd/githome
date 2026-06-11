package domain

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
	"github.com/tamnd/githome/worker"
)

// The review service errors the REST and GraphQL layers map to status. A review
// or comment missing in a visible repository is ErrReviewNotFound (404); an
// anchor that is not part of the pull request's diff, a missing body where one is
// required, or an attempt to self-approve is ErrValidation (422). Forbidden
// writes reuse the shared ErrForbidden.
var (
	// ErrReviewNotFound is returned when no review or review comment matches the
	// lookup in a visible repository.
	ErrReviewNotFound = errors.New("domain: review not found")

	// ErrPendingReviewExists is returned when a user who already holds a pending
	// review draft on a pull request tries to open a second one.
	ErrPendingReviewExists = errors.New("domain: a pending review already exists")
)

// ReviewService implements the code review subsystem. It leans on the repo
// service for visibility and write authorization, on the pull request service to
// resolve and load a pull request, and on the issue service for user assembly. It
// reads the pull request's diff through the git store so it can resolve a comment
// anchor between the line/side and legacy position models and reject one that is
// not in the diff.
type ReviewService struct {
	store    reviewStore
	repos    *RepoService
	prs      *PRService
	issues   *IssueService
	gitStore *git.Store
	enq      worker.Enqueuer
}

// reviewStore is the concrete store surface, narrowed to the methods used. It is
// a local interface so the service can be tested against a fake, mirroring the
// pull request service.
type reviewStore interface {
	UserByPK(ctx context.Context, pk int64) (*store.UserRow, error)
	GetIssueByPK(ctx context.Context, pk int64) (*store.IssueRow, error)
	GetPullByIssuePK(ctx context.Context, issuePK int64) (*store.PullRow, error)
	PullNumberByPK(ctx context.Context, pullPK int64) (int64, error)

	GetReviewByDBID(ctx context.Context, dbID int64) (*store.ReviewRow, error)
	GetReviewByPK(ctx context.Context, pk int64) (*store.ReviewRow, error)
	PendingReviewFor(ctx context.Context, pullPK, userPK int64) (*store.ReviewRow, error)
	ListReviews(ctx context.Context, pullPK int64) ([]store.ReviewRow, error)
	DismissReview(ctx context.Context, pk int64, message string) error
	DeleteReview(ctx context.Context, pk int64) error

	GetReviewComment(ctx context.Context, dbID int64) (*store.ReviewCommentRow, error)
	GetReviewCommentByPK(ctx context.Context, pk int64) (*store.ReviewCommentRow, error)
	ListReviewComments(ctx context.Context, pullPK int64) ([]store.ReviewCommentRow, error)
	ListReviewCommentsForReview(ctx context.Context, reviewPK int64) ([]store.ReviewCommentRow, error)
	UpdateReviewCommentBody(ctx context.Context, pk int64, body string) error
	DeleteReviewComment(ctx context.Context, pk int64) error
	ListAllReviewComments(ctx context.Context, repoPK int64) ([]store.ReviewCommentRow, error)
	SetThreadResolved(ctx context.Context, rootPK int64, resolved bool, resolverPK *int64) error

	ListCommitStatuses(ctx context.Context, repoPK int64, sha string) ([]store.CommitStatusRow, error)
	ListCheckRunsForRef(ctx context.Context, repoPK int64, headSHA string) ([]store.CheckRunRow, error)
	UpsertPullCheckState(ctx context.Context, pullPK int64, decision *string, rollup string, at time.Time) error

	WithTx(ctx context.Context, fn func(*store.Tx) error) error
	EnqueueJob(ctx context.Context, j *store.JobRow) (bool, error)
	InsertEvent(ctx context.Context, e *store.EventRow) error
}

// NewReviewService builds a ReviewService over the store, the repo, pull request,
// and issue services, and the git store.
func NewReviewService(st reviewStore, repos *RepoService, prs *PRService, issues *IssueService, gs *git.Store) *ReviewService {
	return &ReviewService{store: st, repos: repos, prs: prs, issues: issues, gitStore: gs, enq: worker.NewStoreEnqueuer(st)}
}

// ReviewInput is the submit payload: the event (empty opens a pending draft),
// the review body, an optional commit the review pins to, and the batch of inline
// comments to attach.
type ReviewInput struct {
	Event    string
	Body     string
	CommitID string
	Comments []ReviewCommentInput
}

// ReviewCommentInput is one inline comment in a review batch or a standalone
// comment. A caller gives either the line/side anchor or the legacy position; the
// service resolves the other from the diff.
type ReviewCommentInput struct {
	Path      string
	Body      string
	Side      string
	Line      *int64
	StartSide string
	StartLine *int64
	Position  *int64
}

// CreateReview opens a review on a pull request. With an event it submits
// immediately (approved, changes requested, or commented); without one it opens
// the author's single pending draft. It authorizes read access, forbids
// self-approval, requires a body where the event needs one, resolves every inline
// anchor against the diff, writes the review and its comments in one transaction,
// and enqueues the decision recompute.
func (s *ReviewService) CreateReview(ctx context.Context, actorPK int64, owner, name string, number int64, in ReviewInput) (*Review, error) {
	repo, err := s.repos.GetRepo(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	issueRow, pullRow, err := s.loadPull(ctx, repo.PK, number)
	if err != nil {
		return nil, err
	}

	event := strings.ToUpper(strings.TrimSpace(in.Event))
	state, err := stateForEvent(event)
	if err != nil {
		return nil, err
	}
	if (state == ReviewApproved || state == ReviewChangesRequested) && issueRow.UserPK == actorPK {
		return nil, ErrValidation // a user cannot approve or block their own pull request
	}
	if state == ReviewChangesRequested && strings.TrimSpace(in.Body) == "" {
		return nil, ErrValidation
	}
	if state == ReviewPending {
		if _, err := s.store.PendingReviewFor(ctx, pullRow.PK, actorPK); err == nil {
			return nil, ErrPendingReviewExists
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}

	resolved, err := s.resolveComments(ctx, repo.PK, pullRow, in.Comments)
	if err != nil {
		return nil, err
	}

	reviewRow := &store.ReviewRow{
		PullPK: pullRow.PK, RepoPK: repo.PK, UserPK: actorPK,
		State: state, Body: in.Body, CommitID: pullRow.HeadSHA,
	}
	if state != ReviewPending {
		now := nowUTC()
		reviewRow.SubmittedAt = &now
	}
	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.InsertReview(ctx, reviewRow); err != nil {
			return err
		}
		for _, c := range resolved {
			c.ReviewPK = reviewRow.PK
			c.PullPK = pullRow.PK
			c.RepoPK = repo.PK
			c.UserPK = actorPK
			if err := tx.InsertReviewComment(ctx, c); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if state != ReviewPending {
		s.enqueueRecompute(ctx, issueRow.PK)
		s.recordReviewEvent(ctx, actorPK, "submitted", repo, issueRow.PK, reviewRow.PK)
		for _, c := range resolved {
			s.recordReviewCommentEvent(ctx, actorPK, repo, issueRow.PK, c.PK)
		}
	}
	return s.assembleReview(ctx, reviewRow, number)
}

// SubmitReview submits a previously opened pending review under an event, the
// path the reviews/{id}/events endpoint and gh pr review on a draft take.
func (s *ReviewService) SubmitReview(ctx context.Context, actorPK int64, owner, name string, number, reviewDBID int64, event, body string) (*Review, error) {
	repo, err := s.repos.GetRepo(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	issueRow, pullRow, err := s.loadPull(ctx, repo.PK, number)
	if err != nil {
		return nil, err
	}
	reviewRow, err := s.loadReview(ctx, pullRow.PK, reviewDBID)
	if err != nil {
		return nil, err
	}
	if reviewRow.UserPK != actorPK || reviewRow.State != ReviewPending {
		return nil, ErrReviewNotFound
	}
	state, err := stateForEvent(strings.ToUpper(strings.TrimSpace(event)))
	if err != nil || state == ReviewPending {
		return nil, ErrValidation
	}
	if (state == ReviewApproved || state == ReviewChangesRequested) && issueRow.UserPK == actorPK {
		return nil, ErrValidation
	}
	if state == ReviewChangesRequested && strings.TrimSpace(body) == "" {
		return nil, ErrValidation
	}
	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		return tx.SubmitReview(ctx, reviewRow.PK, state, body, pullRow.HeadSHA, nowUTC())
	})
	if err != nil {
		return nil, err
	}
	s.enqueueRecompute(ctx, issueRow.PK)
	s.recordReviewEvent(ctx, actorPK, "submitted", repo, issueRow.PK, reviewRow.PK)
	return s.GetReview(ctx, actorPK, owner, name, number, reviewDBID)
}

// DismissReview drops a submitted review's approval or change request, recording
// a reason. It needs write access, the permission to override a reviewer.
func (s *ReviewService) DismissReview(ctx context.Context, actorPK int64, owner, name string, number, reviewDBID int64, message string) (*Review, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	issueRow, pullRow, err := s.loadPull(ctx, repo.PK, number)
	if err != nil {
		return nil, err
	}
	reviewRow, err := s.loadReview(ctx, pullRow.PK, reviewDBID)
	if err != nil {
		return nil, err
	}
	if reviewRow.State != ReviewApproved && reviewRow.State != ReviewChangesRequested {
		return nil, ErrValidation
	}
	if err := s.store.DismissReview(ctx, reviewRow.PK, message); err != nil {
		return nil, err
	}
	s.enqueueRecompute(ctx, issueRow.PK)
	s.recordReviewEvent(ctx, actorPK, "dismissed", repo, issueRow.PK, reviewRow.PK)
	return s.GetReview(ctx, actorPK, owner, name, number, reviewDBID)
}

// DeleteReview deletes a pending (PENDING state) review by its database ID. Only
// the review's author may delete their own pending draft.
func (s *ReviewService) DeleteReview(ctx context.Context, actorPK int64, reviewDBID int64) (*Review, error) {
	row, err := s.store.GetReviewByDBID(ctx, reviewDBID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrReviewNotFound
	}
	if err != nil {
		return nil, err
	}
	if row.UserPK != actorPK {
		return nil, ErrForbidden
	}
	if row.State != "PENDING" {
		return nil, ErrValidation
	}
	number, err := s.store.PullNumberByPK(ctx, row.PullPK)
	if err != nil {
		return nil, err
	}
	rev, err := s.assembleReview(ctx, row, number)
	if err != nil {
		return nil, err
	}
	return rev, s.store.DeleteReview(ctx, row.PK)
}

// GetReview resolves one review by id for the viewer.
func (s *ReviewService) GetReview(ctx context.Context, viewerPK int64, owner, name string, number, reviewDBID int64) (*Review, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, err
	}
	_, pullRow, err := s.loadPull(ctx, repo.PK, number)
	if err != nil {
		return nil, err
	}
	reviewRow, err := s.loadReview(ctx, pullRow.PK, reviewDBID)
	if err != nil {
		return nil, err
	}
	// A pending draft is private to its author.
	if reviewRow.State == ReviewPending && reviewRow.UserPK != viewerPK {
		return nil, ErrReviewNotFound
	}
	return s.assembleReview(ctx, reviewRow, number)
}

// ListReviews returns a pull request's submitted reviews for the viewer.
func (s *ReviewService) ListReviews(ctx context.Context, viewerPK int64, owner, name string, number int64) ([]*Review, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, err
	}
	_, pullRow, err := s.loadPull(ctx, repo.PK, number)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListReviews(ctx, pullRow.PK)
	if err != nil {
		return nil, err
	}
	out := make([]*Review, 0, len(rows))
	for i := range rows {
		r, err := s.assembleReview(ctx, &rows[i], number)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// CreateComment adds a standalone inline comment, the POST pulls/{n}/comments
// path. A standalone comment rides its own submitted, bodyless COMMENTED review,
// so every comment still belongs to a review.
func (s *ReviewService) CreateComment(ctx context.Context, actorPK int64, owner, name string, number int64, in ReviewCommentInput) (*ReviewComment, error) {
	review, err := s.CreateReview(ctx, actorPK, owner, name, number, ReviewInput{
		Event:    EventComment,
		Comments: []ReviewCommentInput{in},
	})
	if err != nil {
		return nil, err
	}
	if len(review.Comments) == 0 {
		return nil, ErrValidation
	}
	return review.Comments[0], nil
}

// ReplyComment adds a reply under an existing comment's thread, the POST
// pulls/{n}/comments/{id}/replies path. The reply inherits the root's anchor.
func (s *ReviewService) ReplyComment(ctx context.Context, actorPK int64, owner, name string, number, inReplyToDBID int64, body string) (*ReviewComment, error) {
	repo, err := s.repos.GetRepo(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	issueRow, pullRow, err := s.loadPull(ctx, repo.PK, number)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(body) == "" {
		return nil, ErrValidation
	}
	root, err := s.store.GetReviewComment(ctx, inReplyToDBID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrReviewNotFound
	}
	if err != nil {
		return nil, err
	}
	if root.PullPK != pullRow.PK {
		return nil, ErrReviewNotFound
	}
	rootPK := root.PK
	if root.InReplyToPK != nil {
		rootPK = *root.InReplyToPK // chain every reply to the thread root
	}
	reviewRow := &store.ReviewRow{
		PullPK: pullRow.PK, RepoPK: repo.PK, UserPK: actorPK,
		State: ReviewCommented, CommitID: pullRow.HeadSHA,
	}
	now := nowUTC()
	reviewRow.SubmittedAt = &now
	commentRow := &store.ReviewCommentRow{
		PullPK: pullRow.PK, RepoPK: repo.PK, UserPK: actorPK,
		Path: root.Path, Side: root.Side, Line: root.Line, CommitID: pullRow.HeadSHA,
		OriginalCommitID: pullRow.HeadSHA, InReplyToPK: &rootPK,
		Position: root.Position, OriginalPosition: root.Position,
		DiffHunk: root.DiffHunk, Body: body,
	}
	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.InsertReview(ctx, reviewRow); err != nil {
			return err
		}
		commentRow.ReviewPK = reviewRow.PK
		return tx.InsertReviewComment(ctx, commentRow)
	})
	if err != nil {
		return nil, err
	}
	s.enqueueRecompute(ctx, issueRow.PK)
	s.recordReviewCommentEvent(ctx, actorPK, repo, issueRow.PK, commentRow.PK)
	return s.assembleComment(ctx, commentRow, number)
}

// ListComments returns every inline comment on a pull request for the viewer.
func (s *ReviewService) ListComments(ctx context.Context, viewerPK int64, owner, name string, number int64) ([]*ReviewComment, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, err
	}
	_, pullRow, err := s.loadPull(ctx, repo.PK, number)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListReviewComments(ctx, pullRow.PK)
	if err != nil {
		return nil, err
	}
	out := make([]*ReviewComment, 0, len(rows))
	for i := range rows {
		c, err := s.assembleComment(ctx, &rows[i], number)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// GetComment resolves one inline comment by id for the viewer. The standalone
// pulls/comments/{id} route carries no pull number, so the owning pull request's
// number is resolved from the comment to build its urls.
func (s *ReviewService) GetComment(ctx context.Context, viewerPK int64, owner, name string, commentDBID int64) (*ReviewComment, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetReviewComment(ctx, commentDBID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && row.RepoPK != repo.PK) {
		return nil, ErrReviewNotFound
	}
	if err != nil {
		return nil, err
	}
	number, err := s.store.PullNumberByPK(ctx, row.PullPK)
	if err != nil {
		return nil, err
	}
	return s.assembleComment(ctx, row, number)
}

// ReviewThreads folds a pull request's inline comments into threads (a root and
// its replies) and marks each resolved and outdated, the shape the GraphQL
// reviewThreads connection renders. viewerPK gates repository visibility.
func (s *ReviewService) ReviewThreads(ctx context.Context, viewerPK int64, owner, name string, number int64) ([]*ReviewThread, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, err
	}
	_, pullRow, err := s.loadPull(ctx, repo.PK, number)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListReviewComments(ctx, pullRow.PK)
	if err != nil {
		return nil, err
	}
	diff, err := s.diffIndex(ctx, repo.PK, pullRow)
	if err != nil {
		return nil, err
	}

	byRoot := map[int64]*ReviewThread{}
	var order []int64
	for i := range rows {
		row := &rows[i]
		rootPK := row.PK
		if row.InReplyToPK != nil {
			rootPK = *row.InReplyToPK
		}
		thread, ok := byRoot[rootPK]
		if !ok {
			thread = &ReviewThread{RootPK: rootPK, ID: row.DBID, PullPK: pullRow.PK, Path: row.Path, Line: row.Line}
			byRoot[rootPK] = thread
			order = append(order, rootPK)
		}
		if row.PK == rootPK {
			thread.ID = row.DBID
			thread.IsResolved = row.Resolved
			thread.IsOutdated = isOutdated(diff, row)
		}
		c, err := s.assembleComment(ctx, row, number)
		if err != nil {
			return nil, err
		}
		thread.Comments = append(thread.Comments, c)
	}
	out := make([]*ReviewThread, 0, len(order))
	for _, rootPK := range order {
		out = append(out, byRoot[rootPK])
	}
	return out, nil
}

// ResolveThread resolves or unresolves a review thread by its root comment id. It
// needs write access, the permission to settle a conversation.
func (s *ReviewService) ResolveThread(ctx context.Context, actorPK int64, owner, name string, number, rootDBID int64, resolved bool) (*ReviewThread, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	_, pullRow, err := s.loadPull(ctx, repo.PK, number)
	if err != nil {
		return nil, err
	}
	root, err := s.store.GetReviewComment(ctx, rootDBID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && root.PullPK != pullRow.PK) {
		return nil, ErrReviewNotFound
	}
	if err != nil {
		return nil, err
	}
	var resolverPK *int64
	if resolved {
		resolverPK = &actorPK
	}
	if err := s.store.SetThreadResolved(ctx, root.PK, resolved, resolverPK); err != nil {
		return nil, err
	}
	threads, err := s.ReviewThreads(ctx, actorPK, owner, name, number)
	if err != nil {
		return nil, err
	}
	for _, th := range threads {
		if th.RootPK == root.PK {
			return th, nil
		}
	}
	return nil, ErrReviewNotFound
}

// ThreadRef decodes a review thread's root comment id into the owner, repo, and
// pull number the resolve and unresolve mutations address it by. It enforces the
// viewer's visibility through GetRepo, so a thread in a repository the viewer
// cannot see resolves as not found rather than leaking its existence.
func (s *ReviewService) ThreadRef(ctx context.Context, viewerPK, rootDBID int64) (owner, name string, number int64, err error) {
	root, err := s.store.GetReviewComment(ctx, rootDBID)
	if errors.Is(err, store.ErrNotFound) {
		return "", "", 0, ErrReviewNotFound
	}
	if err != nil {
		return "", "", 0, err
	}
	repoRow, err := s.repos.store.RepoByPK(ctx, root.RepoPK)
	if errors.Is(err, store.ErrNotFound) {
		return "", "", 0, ErrReviewNotFound
	}
	if err != nil {
		return "", "", 0, err
	}
	ownerRow, err := s.repos.store.UserByPK(ctx, repoRow.OwnerPK)
	if err != nil {
		return "", "", 0, err
	}
	if _, err := s.repos.GetRepo(ctx, viewerPK, ownerRow.Login, repoRow.Name); err != nil {
		return "", "", 0, err
	}
	number, err = s.store.PullNumberByPK(ctx, root.PullPK)
	if errors.Is(err, store.ErrNotFound) {
		return "", "", 0, ErrReviewNotFound
	}
	if err != nil {
		return "", "", 0, err
	}
	return ownerRow.Login, repoRow.Name, number, nil
}

// ReviewRef decodes a review's db id into the owner, repo, and pull number the
// submit and dismiss mutations address it by. It enforces the viewer's visibility
// through GetRepo, so a review in a private repository resolves as not found.
func (s *ReviewService) ReviewRef(ctx context.Context, viewerPK, reviewDBID int64) (owner, name string, number int64, err error) {
	row, err := s.store.GetReviewByDBID(ctx, reviewDBID)
	if errors.Is(err, store.ErrNotFound) {
		return "", "", 0, ErrReviewNotFound
	}
	if err != nil {
		return "", "", 0, err
	}
	repoRow, err := s.repos.store.RepoByPK(ctx, row.RepoPK)
	if errors.Is(err, store.ErrNotFound) {
		return "", "", 0, ErrReviewNotFound
	}
	if err != nil {
		return "", "", 0, err
	}
	ownerRow, err := s.repos.store.UserByPK(ctx, repoRow.OwnerPK)
	if err != nil {
		return "", "", 0, err
	}
	if _, err := s.repos.GetRepo(ctx, viewerPK, ownerRow.Login, repoRow.Name); err != nil {
		return "", "", 0, err
	}
	number, err = s.store.PullNumberByPK(ctx, row.PullPK)
	if errors.Is(err, store.ErrNotFound) {
		return "", "", 0, ErrReviewNotFound
	}
	if err != nil {
		return "", "", 0, err
	}
	return ownerRow.Login, repoRow.Name, number, nil
}

// ReviewDecision returns a pull request's derived review decision for the viewer,
// the value the GraphQL field and the pull request view surface.
func (s *ReviewService) ReviewDecision(ctx context.Context, viewerPK int64, owner, name string, number int64) (*string, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, err
	}
	_, pullRow, err := s.loadPull(ctx, repo.PK, number)
	if err != nil {
		return nil, err
	}
	return s.computeDecision(ctx, pullRow.PK)
}

// RecomputeReviewDecision resolves and caches a pull request's review decision
// and status check rollup, the body of the recompute_review_decision worker. A
// missing pull request is a no-op, the same tolerance the mergeability recompute
// has for a deleted row.
func (s *ReviewService) RecomputeReviewDecision(ctx context.Context, issuePK int64) error {
	pullRow, err := s.store.GetPullByIssuePK(ctx, issuePK)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	decision, err := s.computeDecision(ctx, pullRow.PK)
	if err != nil {
		return err
	}
	rollup, err := s.computeRollupState(ctx, pullRow.RepoPK, pullRow.HeadSHA)
	if err != nil {
		return err
	}
	return s.store.UpsertPullCheckState(ctx, pullRow.PK, decision, rollup, nowUTC())
}

// computeDecision derives a pull request's review decision from its submitted
// reviews. It tracks each user's latest blocking-or-approving review, ignoring
// comment-only reviews and dropping a user whose review was dismissed: any
// outstanding change request is CHANGES_REQUESTED, otherwise at least one
// approval is APPROVED, otherwise nil.
//
// This diverges from GitHub in one documented way: GitHub returns REVIEW_REQUIRED
// when the base branch's protection demands a review that has not arrived. Branch
// protection is an M8 concern, so without it the decision is computed from the
// reviews alone and is nil when none block or approve.
func (s *ReviewService) computeDecision(ctx context.Context, pullPK int64) (*string, error) {
	rows, err := s.store.ListReviews(ctx, pullPK)
	if err != nil {
		return nil, err
	}
	latest := map[int64]string{}
	for i := range rows {
		switch rows[i].State {
		case ReviewApproved, ReviewChangesRequested:
			latest[rows[i].UserPK] = rows[i].State
		case ReviewDismissed:
			delete(latest, rows[i].UserPK)
		}
	}
	approvals := false
	for _, state := range latest {
		if state == ReviewChangesRequested {
			cr := ReviewChangesRequested
			return &cr, nil
		}
		if state == ReviewApproved {
			approvals = true
		}
	}
	if approvals {
		ap := ReviewApproved
		return &ap, nil
	}
	return nil, nil
}

// computeRollupState folds a head sha's statuses and check runs into one rollup
// state through the shared worst-wins algorithm.
func (s *ReviewService) computeRollupState(ctx context.Context, repoPK int64, sha string) (string, error) {
	statuses, err := s.store.ListCommitStatuses(ctx, repoPK, sha)
	if err != nil {
		return "", err
	}
	runs, err := s.store.ListCheckRunsForRef(ctx, repoPK, sha)
	if err != nil {
		return "", err
	}
	return rollupState(statuses, runs), nil
}

// resolveComments turns the inline comment inputs into store rows, resolving each
// anchor against the pull request's diff and rejecting any that is not in it.
func (s *ReviewService) resolveComments(ctx context.Context, repoPK int64, pullRow *store.PullRow, inputs []ReviewCommentInput) ([]*store.ReviewCommentRow, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	diff, err := s.diffIndex(ctx, repoPK, pullRow)
	if err != nil {
		return nil, err
	}
	out := make([]*store.ReviewCommentRow, 0, len(inputs))
	for _, in := range inputs {
		if strings.TrimSpace(in.Body) == "" || in.Path == "" {
			return nil, ErrValidation
		}
		fd, ok := diff.files[in.Path]
		if !ok {
			return nil, ErrValidation // a comment must land on a file in the diff
		}
		side := strings.ToUpper(strings.TrimSpace(in.Side))
		if side == "" {
			side = sideRight
		}
		var line *int64
		var position *int64
		switch {
		case in.Line != nil:
			idx, ok := anchorIndex(*in.Line)
			if !ok {
				return nil, ErrValidation
			}
			pos, ok := fd.positionFor(idx, side)
			if !ok {
				return nil, ErrValidation
			}
			line = in.Line
			p := int64(pos)
			position = &p
		case in.Position != nil:
			idx, ok := anchorIndex(*in.Position)
			if !ok {
				return nil, ErrValidation
			}
			ln, sd, ok := fd.lineFor(idx)
			if !ok {
				return nil, ErrValidation
			}
			l := int64(ln)
			line, side, position = &l, sd, in.Position
		default:
			return nil, ErrValidation // need a line or a position
		}
		row := &store.ReviewCommentRow{
			Path: in.Path, Side: side, Line: line, Position: position,
			OriginalLine: line, OriginalPosition: position,
			CommitID: pullRow.HeadSHA, OriginalCommitID: pullRow.HeadSHA,
			DiffHunk: diff.patch[in.Path], Body: in.Body,
		}
		out = append(out, row)
	}
	return out, nil
}

// maxAnchorValue bounds a review anchor's line or position before it is narrowed
// to int for the diff lookup. A real diff line or position is a small positive
// number; a value past this bound cannot land on any rendered diff. Guarding here
// keeps the int64-to-int narrowing safe on a 32-bit build (where it could
// otherwise wrap) and rejects an absurd value as a validation miss. The web inline
// composer feeds these straight from a form, so the guard sits on user input.
const maxAnchorValue = 1 << 30

// anchorIndex bounds-checks a user-supplied 1-based line or position and narrows
// it to int, reporting whether it is in range. An out-of-range value is a
// validation miss, the same way an off-diff anchor is.
func anchorIndex(v int64) (int, bool) {
	if v < 1 || v > maxAnchorValue {
		return 0, false
	}
	return int(v), true
}

// diffIndex parses the pull request's current diff into per-file position maps
// and patches, the index the anchor resolution and outdated check read.
func (s *ReviewService) diffIndex(ctx context.Context, repoPK int64, pullRow *store.PullRow) (*pullDiff, error) {
	base := pullRow.BaseSHA
	if tip, err := s.gitStore.RefSHA(ctx, repoPK, "refs/heads/"+pullRow.BaseRef); err == nil {
		base = tip
	}
	files, err := s.gitStore.ChangedFiles(ctx, repoPK, base, pullRow.HeadSHA)
	if err != nil {
		return nil, err
	}
	d := &pullDiff{files: map[string]*fileDiff{}, patch: map[string]string{}}
	for _, f := range files {
		d.files[f.Path] = parseFileDiff(f.Patch)
		d.patch[f.Path] = f.Patch
	}
	return d, nil
}

// pullDiff is a pull request's diff indexed by file path.
type pullDiff struct {
	files map[string]*fileDiff
	patch map[string]string
}

// loadPull resolves an issue row and its pull extension by number, mapping a
// missing or non-pull issue to ErrReviewNotFound.
func (s *ReviewService) loadPull(ctx context.Context, repoPK, number int64) (*store.IssueRow, *store.PullRow, error) {
	issueRow, pullRow, err := s.prs.load(ctx, repoPK, number)
	if errors.Is(err, ErrPullNotFound) {
		return nil, nil, ErrReviewNotFound
	}
	return issueRow, pullRow, err
}

// loadReview resolves a review by id and confirms it belongs to the pull request.
func (s *ReviewService) loadReview(ctx context.Context, pullPK, reviewDBID int64) (*store.ReviewRow, error) {
	row, err := s.store.GetReviewByDBID(ctx, reviewDBID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && row.PullPK != pullPK) {
		return nil, ErrReviewNotFound
	}
	return row, err
}

// assembleReview composes a domain Review from its row, resolving the author and
// its inline comments.
func (s *ReviewService) assembleReview(ctx context.Context, row *store.ReviewRow, number int64) (*Review, error) {
	author, err := s.issues.userByPK(ctx, row.UserPK)
	if err != nil {
		return nil, err
	}
	commentRows, err := s.store.ListReviewCommentsForReview(ctx, row.PK)
	if err != nil {
		return nil, err
	}
	comments := make([]*ReviewComment, 0, len(commentRows))
	for i := range commentRows {
		c, err := s.assembleComment(ctx, &commentRows[i], number)
		if err != nil {
			return nil, err
		}
		comments = append(comments, c)
	}
	return &Review{
		PK: row.PK, ID: row.DBID, PullPK: row.PullPK, PullNumber: number, RepoPK: row.RepoPK,
		User: author, State: row.State, Body: row.Body, CommitID: row.CommitID,
		DismissedMessage: row.DismissedMessage, Comments: comments,
		SubmittedAt: row.SubmittedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

// assembleComment composes a domain ReviewComment from its row.
func (s *ReviewService) assembleComment(ctx context.Context, row *store.ReviewCommentRow, number int64) (*ReviewComment, error) {
	author, err := s.issues.userByPK(ctx, row.UserPK)
	if err != nil {
		return nil, err
	}
	// The wire pull_request_review_id is the owning review's public id, not its
	// internal pk; resolve it from the row's review_pk.
	var reviewID int64
	if rev, err := s.store.GetReviewByPK(ctx, row.ReviewPK); err == nil {
		reviewID = rev.DBID
	}
	return &ReviewComment{
		PK: row.PK, ID: row.DBID, ReviewPK: row.ReviewPK, ReviewID: reviewID, PullPK: row.PullPK,
		PullNumber: number, RepoPK: row.RepoPK, User: author, Path: row.Path,
		Side: row.Side, Line: row.Line, StartLine: row.StartLine, StartSide: row.StartSide,
		Position: row.Position, OriginalPosition: row.OriginalPosition,
		CommitID: row.CommitID, OriginalCommitID: row.OriginalCommitID,
		InReplyTo: row.InReplyToPK, DiffHunk: row.DiffHunk, SubjectType: row.SubjectType,
		Body: row.Body, Resolved: row.Resolved,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

// enqueueRecompute submits a review decision recompute for a pull request, deduped
// by issue so a burst of reviews collapses into one pending recompute.
func (s *ReviewService) enqueueRecompute(ctx context.Context, issuePK int64) {
	key := "review_decision:issue:" + strconv.FormatInt(issuePK, 10)
	payload, err := json.Marshal(recomputePayload{IssuePK: issuePK})
	if err != nil {
		return
	}
	_, _ = s.enq.Enqueue(ctx, JobRecomputeReviewDecision, string(payload), key)
}

// recordReviewEvent appends a pull_request_review activity event and enqueues
// its webhook fan-out. The actor, the repository, and the pull request's issue
// row are the coordinates the renderer rebuilds the payload from, and the
// review's pk rides the event detail so the body can embed the review object;
// delivery is best-effort, so a failure here never fails the user's write.
func (s *ReviewService) recordReviewEvent(ctx context.Context, actorPK int64, action string, repo *Repo, issuePK, reviewPK int64) {
	pk := issuePK
	recordEventFull(ctx, s.store, s.enq, &store.EventRow{
		Event:   EventPullRequestReview,
		Action:  action,
		ActorPK: actorPK,
		RepoPK:  repo.PK,
		IssuePK: &pk,
		Public:  !repo.Private,
	}, nil, nil, &EventDetail{ReviewPK: reviewPK})
}

// recordReviewCommentEvent appends a pull_request_review_comment created event
// for one inline comment, carrying the comment's pk for the delivery body.
func (s *ReviewService) recordReviewCommentEvent(ctx context.Context, actorPK int64, repo *Repo, issuePK, commentPK int64) {
	pk := issuePK
	recordEventFull(ctx, s.store, s.enq, &store.EventRow{
		Event:   EventPullRequestReviewComment,
		Action:  "created",
		ActorPK: actorPK,
		RepoPK:  repo.PK,
		IssuePK: &pk,
		Public:  !repo.Private,
	}, nil, nil, &EventDetail{ReviewCommentPK: commentPK})
}

// stateForEvent maps a submit event to the review state it produces. An empty
// event opens a pending draft; an unknown event is a validation error.
func stateForEvent(event string) (string, error) {
	switch event {
	case "":
		return ReviewPending, nil
	case EventApprove:
		return ReviewApproved, nil
	case EventRequestChanges:
		return ReviewChangesRequested, nil
	case EventComment:
		return ReviewCommented, nil
	default:
		return "", ErrValidation
	}
}

// isOutdated reports whether a comment's anchor is no longer present in the
// current diff, the state that greys a thread out.
func isOutdated(diff *pullDiff, row *store.ReviewCommentRow) bool {
	fd, ok := diff.files[row.Path]
	if !ok {
		return true
	}
	if row.Line == nil {
		return false
	}
	return !fd.contains(int(*row.Line), row.Side)
}

// ReviewForEvent loads one review by its internal pk for the delivery
// renderer, off the visibility gate like every ForEvent loader.
func (s *ReviewService) ReviewForEvent(ctx context.Context, reviewPK int64) (*Review, error) {
	row, err := s.store.GetReviewByPK(ctx, reviewPK)
	if err != nil {
		return nil, err
	}
	number, err := s.store.PullNumberByPK(ctx, row.PullPK)
	if err != nil {
		return nil, err
	}
	return s.assembleReview(ctx, row, number)
}

// ReviewCommentForEvent loads one inline review comment by its internal pk for
// the delivery renderer.
func (s *ReviewService) ReviewCommentForEvent(ctx context.Context, commentPK int64) (*ReviewComment, error) {
	row, err := s.store.GetReviewCommentByPK(ctx, commentPK)
	if err != nil {
		return nil, err
	}
	number, err := s.store.PullNumberByPK(ctx, row.PullPK)
	if err != nil {
		return nil, err
	}
	return s.assembleComment(ctx, row, number)
}

// EditReviewComment updates the body of an inline review comment.
func (s *ReviewService) EditReviewComment(ctx context.Context, actorPK, commentDBID int64, body string) (*ReviewComment, error) {
	row, err := s.store.GetReviewComment(ctx, commentDBID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if row.UserPK != actorPK {
		return nil, ErrForbidden
	}
	if err := s.store.UpdateReviewCommentBody(ctx, row.PK, body); err != nil {
		return nil, err
	}
	row.Body = body
	return s.assembleComment(ctx, row, 0)
}

// DeleteReviewComment removes an inline review comment.
func (s *ReviewService) DeleteReviewComment(ctx context.Context, actorPK, commentDBID int64) error {
	row, err := s.store.GetReviewComment(ctx, commentDBID)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if row.UserPK != actorPK {
		return ErrForbidden
	}
	return s.store.DeleteReviewComment(ctx, row.PK)
}

// ListAllReviewComments returns all inline review comments in a repository.
func (s *ReviewService) ListAllReviewComments(ctx context.Context, viewerPK int64, owner, name string) ([]*ReviewComment, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListAllReviewComments(ctx, repo.PK)
	if err != nil {
		return nil, err
	}
	out := make([]*ReviewComment, 0, len(rows))
	for i := range rows {
		c, err := s.assembleComment(ctx, &rows[i], 0)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}


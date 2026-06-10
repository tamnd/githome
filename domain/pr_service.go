package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
	"github.com/tamnd/githome/worker"
)

// The pull request service errors the REST and GraphQL layers map to status. A
// pull request missing in a visible repository is ErrPullNotFound (404); a merge
// of a pull request that cannot land (closed, already merged, conflicting, or
// nothing to merge) is ErrNotMergeable (405); a merge whose expected head sha no
// longer matches is ErrHeadMismatch (409); a bad merge_method is
// ErrInvalidMergeMethod (422). Forbidden writes and validation reuse the shared
// sentinels.
var (
	// ErrPullNotFound is returned when no pull request matches the lookup in a
	// visible repository.
	ErrPullNotFound = errors.New("domain: pull request not found")

	// ErrNotMergeable is returned when a pull request cannot be merged: it is
	// closed, already merged, conflicting, or has nothing to merge.
	ErrNotMergeable = errors.New("domain: pull request is not mergeable")

	// ErrHeadMismatch is returned when a merge's expected head sha does not match
	// the pull request's current head.
	ErrHeadMismatch = errors.New("domain: head sha does not match")

	// ErrInvalidMergeMethod is returned for a merge_method other than merge,
	// squash, or rebase.
	ErrInvalidMergeMethod = errors.New("domain: invalid merge method")
)

// PullStore is the slice of the store the pull request service needs: the pull
// extension reads and writes, the issue, user, repository, label, and milestone
// lookups it assembles a pull request from, the transaction entry point the
// create and merge paths run through, and the job enqueue the mergeability
// recompute and the webhook event ride on.
type PullStore interface {
	UserByPK(ctx context.Context, pk int64) (*store.UserRow, error)
	UserByLogin(ctx context.Context, login string) (*store.UserRow, error)
	RepoByPK(ctx context.Context, pk int64) (*store.RepoRow, error)

	GetIssueByNumber(ctx context.Context, repoPK, number int64) (*store.IssueRow, error)
	GetIssueByPK(ctx context.Context, pk int64) (*store.IssueRow, error)
	IssuesByPKs(ctx context.Context, pks []int64) (map[int64]*store.IssueRow, error)
	LabelsByIssue(ctx context.Context, issuePK int64) ([]store.LabelRow, error)
	ListAssigneePKs(ctx context.Context, issuePK int64) ([]int64, error)
	GetMilestoneByPK(ctx context.Context, pk int64) (*store.MilestoneRow, error)
	LabelsByIssuePKs(ctx context.Context, issuePKs []int64) (map[int64][]store.LabelRow, error)
	AssigneesByIssuePKs(ctx context.Context, issuePKs []int64) (map[int64][]int64, error)
	UsersByPKs(ctx context.Context, pks []int64) (map[int64]*store.UserRow, error)
	MilestonesByPKs(ctx context.Context, pks []int64) (map[int64]*store.MilestoneRow, error)

	GetPullByIssuePK(ctx context.Context, issuePK int64) (*store.PullRow, error)
	GetPullByDBID(ctx context.Context, dbID int64) (*store.PullRow, error)
	ListPulls(ctx context.Context, repoPK int64, state string, limit, offset int) ([]store.PullRow, error)
	ListPullsPage(ctx context.Context, repoPK int64, state string, cursor *store.PullCursor, limit int) ([]store.PullRow, bool, error)
	CountPulls(ctx context.Context, repoPK int64, state string) (int, error)
	OpenPullsByHeadRef(ctx context.Context, repoPK int64, headRef string) ([]store.PullRow, error)
	OpenPullsByBaseRef(ctx context.Context, repoPK int64, baseRef string) ([]store.PullRow, error)
	SetMergeability(ctx context.Context, issuePK int64, mergeable *bool, state string, rebaseable *bool, additions, deletions, changedFiles, commits int, checkedAt time.Time) error

	WithTx(ctx context.Context, fn func(*store.Tx) error) error

	EnqueueJob(ctx context.Context, j *store.JobRow) (bool, error)
	InsertEvent(ctx context.Context, e *store.EventRow) error
}

// PRService implements the pull request subsystem. It leans on the repo service
// for visibility and write authorization and on the issue service for the issue
// half of a pull request, so those rules live in one place. The git store backs
// the merge surface: the test merge behind mergeability, the three merge
// strategies, and the synthetic refs/pull/<n>/head and /merge refs.
type PRService struct {
	store    PullStore
	repos    *RepoService
	issues   *IssueService
	gitStore *git.Store
	enq      worker.Enqueuer
}

// NewPRService builds a PRService over the store, the repo and issue services,
// and the git store.
func NewPRService(st PullStore, repos *RepoService, issues *IssueService, gs *git.Store) *PRService {
	return &PRService{store: st, repos: repos, issues: issues, gitStore: gs, enq: worker.NewStoreEnqueuer(st)}
}

// PRInput is the create payload: the title and optional body, the base and head
// branch names, and the draft and maintainer flags. For M5 the head is a branch
// in the same repository; cross-repository forks arrive with their milestone.
type PRInput struct {
	Title               string
	Body                *string
	Base                string
	Head                string
	Draft               bool
	MaintainerCanModify bool
}

// PRQuery narrows the list endpoint to a state (open, closed, all) and a page.
// Cursor, when set, is the opaque keyset token from the previous page's Link
// header, which switches the list to a number seek instead of OFFSET.
type PRQuery struct {
	State   string
	Page    int
	PerPage int
	Cursor  string
}

// MergeInput is the merge payload: the strategy, the optional commit title and
// message overriding the defaults, and the optional expected head sha that
// guards against merging a head that moved out from under the caller.
type MergeInput struct {
	Method        git.MergeMethod
	CommitTitle   string
	CommitMessage string
	ExpectedHead  string
}

// MergeResult is the outcome of a successful merge: the new commit on the base
// branch and the message it carries.
type MergeResult struct {
	SHA     string
	Merged  bool
	Message string
}

// PRPatch is the update payload for an existing pull request. Only non-nil
// pointer fields are applied so callers can patch a single field without
// knowing the current value of the others.
type PRPatch struct {
	Title               *string
	Body                *string
	BaseRef             *string
	State               *string // "open" | "closed"
	Draft               *bool
	MaintainerCanModify *bool
	Labels              *[]string // replace; nil = no change
	AssigneeLogins      *[]string // replace; nil = no change
	MilestoneNumber     *int64
	ClearMilestone      bool
}

// CreatePR opens a pull request after authorizing write access. It validates the
// base and head branches resolve and differ, allocates the shared issue number,
// writes the issue row (is_pull) and the pull extension in one transaction,
// publishes the synthetic refs/pull/<n>/head ref at the head tip, and enqueues
// the mergeability recompute so mergeable transitions from null to a value.
func (s *PRService) CreatePR(ctx context.Context, actorPK int64, owner, name string, in PRInput) (*PullRequest, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return nil, ErrValidation
	}
	base, head := strings.TrimSpace(in.Base), strings.TrimSpace(in.Head)
	if base == "" || head == "" || base == head {
		return nil, ErrValidation
	}
	baseSHA, err := s.gitStore.RefSHA(ctx, repo.PK, "refs/heads/"+base)
	if errors.Is(err, git.ErrRefNotFound) {
		return nil, ErrValidation
	}
	if err != nil {
		return nil, err
	}
	headSHA, err := s.gitStore.RefSHA(ctx, repo.PK, "refs/heads/"+head)
	if errors.Is(err, git.ErrRefNotFound) {
		return nil, ErrValidation
	}
	if err != nil {
		return nil, err
	}

	issueRow := &store.IssueRow{
		RepoPK: repo.PK,
		UserPK: actorPK,
		IsPull: true,
		Title:  strings.TrimSpace(in.Title),
		Body:   in.Body,
		State:  "open",
	}
	pullRow := &store.PullRow{
		RepoPK:              repo.PK,
		BaseRef:             base,
		BaseSHA:             baseSHA,
		HeadRef:             head,
		HeadSHA:             headSHA,
		Draft:               in.Draft,
		MaintainerCanModify: in.MaintainerCanModify,
		MergeableState:      "unknown",
	}
	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		n, err := tx.AllocIssueNumber(ctx, repo.PK)
		if err != nil {
			return err
		}
		issueRow.Number = n
		if err := tx.InsertIssue(ctx, issueRow); err != nil {
			return err
		}
		pullRow.IssuePK = issueRow.PK
		if err := tx.InsertPull(ctx, pullRow); err != nil {
			return err
		}
		return tx.AdjustOpenIssuesCount(ctx, repo.PK, 1)
	})
	if err != nil {
		return nil, err
	}

	// Publish the head ref so a client can fetch refs/pull/<n>/head, then ask the
	// worker to compute mergeability. A ref-write failure should not undo the
	// committed pull request, so it surfaces as a plain error the caller logs.
	if err := s.upsertRef(ctx, repo.PK, headRef(issueRow.Number), headSHA); err != nil {
		return nil, err
	}
	s.enqueueRecompute(ctx, issueRow.PK)
	s.recordPullEvent(ctx, actorPK, "opened", repo, issueRow.PK)
	return s.assemble(ctx, repo, issueRow, pullRow)
}

// GetPR resolves one pull request by number for the viewer.
func (s *PRService) GetPR(ctx context.Context, viewerPK int64, owner, name string, number int64) (*PullRequest, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, err
	}
	issueRow, pullRow, err := s.load(ctx, repo.PK, number)
	if err != nil {
		return nil, err
	}
	return s.assemble(ctx, repo, issueRow, pullRow)
}

// GetPRByID resolves a pull request by its public database id for the viewer,
// the path a PullRequest node id decodes to. A pull request in a repository the
// viewer cannot see is ErrPullNotFound, never leaked.
func (s *PRService) GetPRByID(ctx context.Context, viewerPK, dbID int64) (*PullRequest, error) {
	pullRow, err := s.store.GetPullByDBID(ctx, dbID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrPullNotFound
	}
	if err != nil {
		return nil, err
	}
	repo, err := s.repoByPK(ctx, viewerPK, pullRow.RepoPK)
	if err != nil {
		return nil, err
	}
	issueRow, err := s.store.GetIssueByPK(ctx, pullRow.IssuePK)
	if err != nil {
		return nil, err
	}
	return s.assemble(ctx, repo, issueRow, pullRow)
}

// PRRef resolves the owner/repo/number coordinates of a pull request by its
// internal DB id. The GraphQL layer uses it to convert a PullRequest node id
// into the coordinates the domain methods need.
func (s *PRService) PRRef(ctx context.Context, dbID int64) (owner, name string, number int64, err error) {
	pullRow, err := s.store.GetPullByDBID(ctx, dbID)
	if errors.Is(err, store.ErrNotFound) {
		return "", "", 0, ErrPullNotFound
	}
	if err != nil {
		return "", "", 0, err
	}
	issueRow, err := s.store.GetIssueByPK(ctx, pullRow.IssuePK)
	if err != nil {
		return "", "", 0, err
	}
	repoRow, err := s.store.RepoByPK(ctx, pullRow.RepoPK)
	if err != nil {
		return "", "", 0, err
	}
	ownerRow, err := s.store.UserByPK(ctx, repoRow.OwnerPK)
	if err != nil {
		return "", "", 0, err
	}
	return ownerRow.Login, repoRow.Name, issueRow.Number, nil
}

// UpdatePR applies the non-nil fields of patch to an existing open pull request.
// Issue-level fields (title, body, state, labels, assignees, milestone) are
// delegated to the issue service; pull-level fields (base branch, draft,
// maintainer-can-modify) are updated in one UPDATE directly.
func (s *PRService) UpdatePR(ctx context.Context, actorPK int64, owner, name string, number int64, p PRPatch) (*PullRequest, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	issueRow, pullRow, err := s.load(ctx, repo.PK, number)
	if err != nil {
		return nil, err
	}
	patch := IssuePatch{
		Title:           p.Title,
		Body:            p.Body,
		State:           p.State,
		Labels:          p.Labels,
		AssigneeLogins:  p.AssigneeLogins,
		MilestoneNumber: p.MilestoneNumber,
		ClearMilestone:  p.ClearMilestone,
	}
	updatedIssue, err := s.issues.EditIssue(ctx, actorPK, owner, name, number, patch)
	if err != nil {
		return nil, err
	}
	_ = updatedIssue
	needsPullUpdate := p.BaseRef != nil || p.Draft != nil || p.MaintainerCanModify != nil
	if needsPullUpdate {
		newBase := pullRow.BaseRef
		newDraft := pullRow.Draft
		newMCM := pullRow.MaintainerCanModify
		if p.BaseRef != nil {
			newBase = *p.BaseRef
		}
		if p.Draft != nil {
			newDraft = *p.Draft
		}
		if p.MaintainerCanModify != nil {
			newMCM = *p.MaintainerCanModify
		}
		if err := s.store.WithTx(ctx, func(tx *store.Tx) error {
			return tx.UpdatePullMeta(ctx, pullRow.PK, newBase, newDraft, newMCM)
		}); err != nil {
			return nil, err
		}
	}
	issueRow, err = s.store.GetIssueByPK(ctx, issueRow.PK)
	if err != nil {
		return nil, err
	}
	pullRow, err = s.store.GetPullByIssuePK(ctx, issueRow.PK)
	if err != nil {
		return nil, err
	}
	return s.assemble(ctx, repo, issueRow, pullRow)
}

// ListPRs returns a page of the repository's pull requests plus the total
// matching the state filter, for the pagination headers.
func (s *PRService) ListPRs(ctx context.Context, viewerPK int64, owner, name string, q PRQuery) ([]*PullRequest, int, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.store.ListPulls(ctx, repo.PK, q.State, q.PerPage, offsetFor(q.Page, q.PerPage))
	if err != nil {
		return nil, 0, err
	}
	total, err := s.store.CountPulls(ctx, repo.PK, q.State)
	if err != nil {
		return nil, 0, err
	}
	out, err := s.assemblePRs(ctx, repo, rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ListPRsPage returns a keyset-paginated page of the repository's pull requests
// plus whether a further page exists, without the COUNT that ListPRs runs for
// the page-number Link header. It is the flat read path for cursor walks: a
// malformed cursor decodes to nil and starts from the newest, matching the
// issue list's degrade-to-first-page behavior.
func (s *PRService) ListPRsPage(ctx context.Context, viewerPK int64, owner, name string, q PRQuery) ([]*PullRequest, bool, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, false, err
	}
	var cursor *store.PullCursor
	if q.Cursor != "" {
		if cur, derr := store.DecodePullCursor(q.Cursor); derr == nil {
			cursor = &cur
		}
	}
	rows, hasMore, err := s.store.ListPullsPage(ctx, repo.PK, q.State, cursor, q.PerPage)
	if err != nil {
		return nil, false, err
	}
	out, err := s.assemblePRs(ctx, repo, rows)
	if err != nil {
		return nil, false, err
	}
	return out, hasMore, nil
}

// Files returns the per-file diff of a pull request over the three-dot range
// from the base branch tip to the head, the body of the files endpoint.
func (s *PRService) Files(ctx context.Context, viewerPK int64, owner, name string, number int64) ([]git.FileChange, error) {
	repo, base, head, err := s.diffEnds(ctx, viewerPK, owner, name, number)
	if err != nil {
		return nil, err
	}
	return s.gitStore.ChangedFiles(ctx, repo.PK, base, head)
}

// Commits returns a pull request's own commits, oldest first.
func (s *PRService) Commits(ctx context.Context, viewerPK int64, owner, name string, number int64) ([]git.Commit, error) {
	repo, base, head, err := s.diffEnds(ctx, viewerPK, owner, name, number)
	if err != nil {
		return nil, err
	}
	return s.gitStore.CommitsBetween(ctx, repo.PK, base, head)
}

// Diff returns the pull request's unified diff, the .diff media body and what gh
// pr diff prints.
func (s *PRService) Diff(ctx context.Context, viewerPK int64, owner, name string, number int64) ([]byte, error) {
	repo, base, head, err := s.diffEnds(ctx, viewerPK, owner, name, number)
	if err != nil {
		return nil, err
	}
	return s.gitStore.DiffRaw(ctx, repo.PK, base, head)
}

// Patch returns the pull request's commits as an mbox patch series, the .patch
// media body.
func (s *PRService) Patch(ctx context.Context, viewerPK int64, owner, name string, number int64) ([]byte, error) {
	repo, base, head, err := s.diffEnds(ctx, viewerPK, owner, name, number)
	if err != nil {
		return nil, err
	}
	return s.gitStore.FormatPatch(ctx, repo.PK, base, head)
}

// Merge lands a pull request by the given method after authorizing write access.
// It guards the expected head sha, refuses a closed, merged, conflicting, or
// empty merge, writes the merge commit, advances the base branch to it, closes
// the issue, and records the merge, all so a re-read reports merged true.
func (s *PRService) Merge(ctx context.Context, actorPK int64, owner, name string, number int64, in MergeInput) (*MergeResult, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	method := in.Method
	if method == "" {
		method = git.MergeCommit
	}
	if method != git.MergeCommit && method != git.MergeSquash && method != git.MergeRebase {
		return nil, ErrInvalidMergeMethod
	}
	issueRow, pullRow, err := s.load(ctx, repo.PK, number)
	if err != nil {
		return nil, err
	}
	if pullRow.Merged || issueRow.State != "open" {
		return nil, ErrNotMergeable
	}
	if in.ExpectedHead != "" && in.ExpectedHead != pullRow.HeadSHA {
		return nil, ErrHeadMismatch
	}

	baseTip, err := s.gitStore.RefSHA(ctx, repo.PK, "refs/heads/"+pullRow.BaseRef)
	if err != nil {
		return nil, ErrNotMergeable
	}
	ahead, _, err := s.gitStore.AheadBehind(ctx, repo.PK, baseTip, pullRow.HeadSHA)
	if err != nil {
		return nil, err
	}
	if ahead == 0 {
		return nil, ErrNotMergeable // nothing to merge
	}

	merger, err := s.issues.userByPK(ctx, actorPK)
	if err != nil {
		return nil, err
	}
	who := prSignature(merger)
	message := mergeMessage(method, number, issueRow, in, headLabel(repo, pullRow))
	sha, ok, err := s.gitStore.Merge(ctx, repo.PK, method, baseTip, pullRow.HeadSHA, message, who, who)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotMergeable
	}
	if err := s.gitStore.UpdateRef(ctx, repo.PK, "refs/heads/"+pullRow.BaseRef, sha, true); err != nil {
		return nil, err
	}

	now := nowUTC()
	err = s.store.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.MarkMerged(ctx, pullRow.PK, actorPK, sha, now); err != nil {
			return err
		}
		issueRow.State = "closed"
		issueRow.ClosedAt = &now
		issueRow.ClosedByPK = &actorPK
		reason := "completed"
		issueRow.StateReason = &reason
		if err := tx.UpdateIssue(ctx, issueRow); err != nil {
			return err
		}
		return tx.AdjustOpenIssuesCount(ctx, repo.PK, -1)
	})
	if err != nil {
		return nil, err
	}
	s.recordPullEvent(ctx, actorPK, "closed", repo, issueRow.PK)
	return &MergeResult{SHA: sha, Merged: true, Message: message}, nil
}

// RecomputeMergeability resolves and persists a pull request's merge state: it
// test-merges the current base tip against the head, records mergeable, the
// mergeable_state string, the rebaseable flag, the diff stats, and the commit
// count, and on a clean merge publishes a real refs/pull/<n>/merge commit. It is
// the body of the recompute_mergeability worker; a merged pull request is left
// untouched.
func (s *PRService) RecomputeMergeability(ctx context.Context, issuePK int64) error {
	pullRow, err := s.store.GetPullByIssuePK(ctx, issuePK)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if pullRow.Merged {
		return nil
	}

	baseTip, baseErr := s.gitStore.RefSHA(ctx, pullRow.RepoPK, "refs/heads/"+pullRow.BaseRef)
	headTip, headErr := s.gitStore.RefSHA(ctx, pullRow.RepoPK, "refs/heads/"+pullRow.HeadRef)
	if errors.Is(baseErr, git.ErrRefNotFound) || errors.Is(headErr, git.ErrRefNotFound) {
		// A side disappeared (branch deleted): the pull request cannot merge.
		no := false
		return s.store.SetMergeability(ctx, issuePK, &no, "dirty", &no, 0, 0, 0, 0, nowUTC())
	}
	if baseErr != nil {
		return baseErr
	}
	if headErr != nil {
		return headErr
	}

	ahead, behind, err := s.gitStore.AheadBehind(ctx, pullRow.RepoPK, baseTip, headTip)
	if err != nil {
		return err
	}
	additions, deletions, changed, err := s.gitStore.DiffStat(ctx, pullRow.RepoPK, baseTip, headTip)
	if err != nil {
		return err
	}
	_, clean, err := s.gitStore.TestMerge(ctx, pullRow.RepoPK, baseTip, headTip)
	if err != nil {
		return err
	}

	rebaseable := clean && s.linearHistory(ctx, pullRow.RepoPK, baseTip, headTip)
	state := mergeableState(pullRow, clean, behind)
	if clean {
		s.publishMergeRef(ctx, pullRow, baseTip, headTip)
	}
	return s.store.SetMergeability(ctx, issuePK, &clean, state, &rebaseable, additions, deletions, changed, ahead, nowUTC())
}

// OnHeadPush refreshes the pull requests a push to a branch touches. A push to a
// head branch repoints that pull request's recorded head and its
// refs/pull/<n>/head ref; a push to a base branch leaves the head alone. Either
// way every affected open pull request is re-checked for mergeability.
func (s *PRService) OnHeadPush(ctx context.Context, repoPK int64, branch, newSHA string) error {
	headPulls, err := s.store.OpenPullsByHeadRef(ctx, repoPK, branch)
	if err != nil {
		return err
	}
	for i := range headPulls {
		p := &headPulls[i]
		issueRow, err := s.store.GetIssueByPK(ctx, p.IssuePK)
		if err != nil {
			return err
		}
		err = s.store.WithTx(ctx, func(tx *store.Tx) error {
			return tx.UpdatePullHead(ctx, p.PK, newSHA)
		})
		if err != nil {
			return err
		}
		if err := s.upsertRef(ctx, repoPK, headRef(issueRow.Number), newSHA); err != nil {
			return err
		}
		s.enqueueRecompute(ctx, p.IssuePK)
	}
	basePulls, err := s.store.OpenPullsByBaseRef(ctx, repoPK, branch)
	if err != nil {
		return err
	}
	for i := range basePulls {
		s.enqueueRecompute(ctx, basePulls[i].IssuePK)
	}
	return nil
}

// diffEnds resolves the base branch tip and the recorded head of a pull request
// for the viewer, the two ends the diff, files, commits, and patch reads run
// between.
func (s *PRService) diffEnds(ctx context.Context, viewerPK int64, owner, name string, number int64) (*Repo, string, string, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, "", "", err
	}
	_, pullRow, err := s.load(ctx, repo.PK, number)
	if err != nil {
		return nil, "", "", err
	}
	baseTip, err := s.gitStore.RefSHA(ctx, repo.PK, "refs/heads/"+pullRow.BaseRef)
	if err != nil {
		// A base branch that no longer exists falls back to the recorded base sha.
		baseTip = pullRow.BaseSHA
	}
	return repo, baseTip, pullRow.HeadSHA, nil
}

// load resolves the issue row and its pull extension by number, mapping a
// missing or non-pull issue to ErrPullNotFound.
func (s *PRService) load(ctx context.Context, repoPK, number int64) (*store.IssueRow, *store.PullRow, error) {
	issueRow, err := s.store.GetIssueByNumber(ctx, repoPK, number)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, ErrPullNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if !issueRow.IsPull {
		return nil, nil, ErrPullNotFound
	}
	pullRow, err := s.store.GetPullByIssuePK(ctx, issueRow.PK)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, ErrPullNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	return issueRow, pullRow, nil
}

// repoByPK resolves a repository by internal pk for the viewer, applying the
// visibility rule so an invisible repository surfaces as ErrPullNotFound.
func (s *PRService) repoByPK(ctx context.Context, viewerPK, repoPK int64) (*Repo, error) {
	repoRow, err := s.store.RepoByPK(ctx, repoPK)
	if err != nil {
		return nil, ErrPullNotFound
	}
	if repoRow.Private && (viewerPK == 0 || viewerPK != repoRow.OwnerPK) {
		return nil, ErrPullNotFound
	}
	ownerRow, err := s.store.UserByPK(ctx, repoRow.OwnerPK)
	if err != nil {
		return nil, err
	}
	return repoFromRow(repoRow, userFromRow(ownerRow)), nil
}

// PullForEvent assembles a pull request by its issue pk for the webhook
// renderer. No visibility check applies: the event was authorized when it was
// recorded.
func (s *PRService) PullForEvent(ctx context.Context, repo *Repo, issuePK int64) (*PullRequest, error) {
	issueRow, err := s.store.GetIssueByPK(ctx, issuePK)
	if err != nil {
		return nil, err
	}
	pullRow, err := s.store.GetPullByIssuePK(ctx, issuePK)
	if err != nil {
		return nil, err
	}
	return s.assemble(ctx, repo, issueRow, pullRow)
}

// assemblePRs batch-loads all ancillary data for a page of pull rows in five
// round trips (issues, users, labels, assignees, milestones) instead of N×5.
func (s *PRService) assemblePRs(ctx context.Context, repo *Repo, rows []store.PullRow) ([]*PullRequest, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	// Collect the issue PKs and batch-load them.
	issuePKs := make([]int64, len(rows))
	for i := range rows {
		issuePKs[i] = rows[i].IssuePK
	}
	issueMap, err := s.store.IssuesByPKs(ctx, issuePKs)
	if err != nil {
		return nil, err
	}

	// Collect unique user/milestone PKs across all issues.
	userPKSet := map[int64]struct{}{}
	milestonePKSet := map[int64]struct{}{}
	for _, iss := range issueMap {
		userPKSet[iss.UserPK] = struct{}{}
		if iss.MilestonePK != nil {
			milestonePKSet[*iss.MilestonePK] = struct{}{}
		}
	}
	for i := range rows {
		if rows[i].MergedByPK != nil {
			userPKSet[*rows[i].MergedByPK] = struct{}{}
		}
	}

	// Batch-load labels and assignees by issue PK.
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

	out := make([]*PullRequest, 0, len(rows))
	for i := range rows {
		pullRow := &rows[i]
		issueRow, ok := issueMap[pullRow.IssuePK]
		if !ok {
			continue
		}

		var author *User
		if u, ok := userMap[issueRow.UserPK]; ok {
			author = userFromRow(u)
		}

		assigneePKs := assigneeMap[issueRow.PK]
		assignees := make([]*User, 0, len(assigneePKs))
		for _, pk := range assigneePKs {
			if u, ok := userMap[pk]; ok {
				assignees = append(assignees, userFromRow(u))
			}
		}

		var milestone *Milestone
		if issueRow.MilestonePK != nil {
			if mr, ok := milestoneMap[*issueRow.MilestonePK]; ok {
				var creator *User
				if mr.CreatorPK != nil {
					if cu, ok := userMap[*mr.CreatorPK]; ok {
						creator = userFromRow(cu)
					} else {
						cu2, err := s.issues.store.UserByPK(ctx, *mr.CreatorPK)
						if err == nil {
							creator = userFromRow(cu2)
						}
					}
				}
				open, closed, err := s.issues.store.MilestoneIssueCounts(ctx, mr.PK)
				if err != nil {
					return nil, err
				}
				milestone = &Milestone{
					ID: mr.DBID, Number: mr.Number, Title: mr.Title,
					Description: mr.Description, State: mr.State, Creator: creator,
					OpenIssues: open, ClosedIssues: closed,
					DueOn: mr.DueOn, ClosedAt: mr.ClosedAt,
					CreatedAt: mr.CreatedAt, UpdatedAt: mr.UpdatedAt,
				}
			}
		}

		var mergedBy *User
		if pullRow.MergedByPK != nil {
			if u, ok := userMap[*pullRow.MergedByPK]; ok {
				mergedBy = userFromRow(u)
			}
		}

		out = append(out, &PullRequest{
			PK:                  pullRow.PK,
			ID:                  pullRow.DBID,
			IssueID:             issueRow.DBID,
			Number:              issueRow.Number,
			RepoPK:              repo.PK,
			Repo:                repo,
			Title:               issueRow.Title,
			Body:                issueRow.Body,
			State:               issueRow.State,
			Locked:              issueRow.Locked,
			User:                author,
			Assignees:           assignees,
			Labels:              labelsFromRows(labelMap[issueRow.PK]),
			Milestone:           milestone,
			CommentsCount:       issueRow.CommentsCount,
			Base:                endpoint(repo, pullRow.BaseRef, pullRow.BaseSHA),
			Head:                endpoint(repo, pullRow.HeadRef, pullRow.HeadSHA),
			Draft:               pullRow.Draft,
			MaintainerCanModify: pullRow.MaintainerCanModify,
			Merged:              pullRow.Merged,
			MergedAt:            pullRow.MergedAt,
			MergedBy:            mergedBy,
			MergeCommitSHA:      pullRow.MergeCommitSHA,
			Mergeable:           pullRow.Mergeable,
			MergeableState:      pullRow.MergeableState,
			Rebaseable:          pullRow.Rebaseable,
			Additions:           pullRow.Additions,
			Deletions:           pullRow.Deletions,
			ChangedFiles:        pullRow.ChangedFiles,
			CommitsCount:        pullRow.CommitsCount,
			ClosedAt:            issueRow.ClosedAt,
			CreatedAt:           issueRow.CreatedAt,
			UpdatedAt:           issueRow.UpdatedAt,
		})
	}
	return out, nil
}

// assemble composes the domain PullRequest from the issue row, the pull row, and
// the repository, resolving the author, assignees, labels, milestone, merger,
// and the base and head endpoints.
func (s *PRService) assemble(ctx context.Context, repo *Repo, issueRow *store.IssueRow, pullRow *store.PullRow) (*PullRequest, error) {
	author, err := s.issues.userByPK(ctx, issueRow.UserPK)
	if err != nil {
		return nil, err
	}
	labelRows, err := s.store.LabelsByIssue(ctx, issueRow.PK)
	if err != nil {
		return nil, err
	}
	assigneePKs, err := s.store.ListAssigneePKs(ctx, issueRow.PK)
	if err != nil {
		return nil, err
	}
	assignees := make([]*User, 0, len(assigneePKs))
	for _, pk := range assigneePKs {
		u, err := s.issues.userByPK(ctx, pk)
		if err != nil {
			return nil, err
		}
		assignees = append(assignees, u)
	}
	var milestone *Milestone
	if issueRow.MilestonePK != nil {
		mr, err := s.store.GetMilestoneByPK(ctx, *issueRow.MilestonePK)
		if err == nil {
			if milestone, err = s.issues.assembleMilestone(ctx, mr); err != nil {
				return nil, err
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	var mergedBy *User
	if pullRow.MergedByPK != nil {
		if mergedBy, err = s.issues.userByPK(ctx, *pullRow.MergedByPK); err != nil {
			return nil, err
		}
	}

	pr := &PullRequest{
		PK:                  pullRow.PK,
		ID:                  pullRow.DBID,
		IssueID:             issueRow.DBID,
		Number:              issueRow.Number,
		RepoPK:              repo.PK,
		Repo:                repo,
		Title:               issueRow.Title,
		Body:                issueRow.Body,
		State:               issueRow.State,
		Locked:              issueRow.Locked,
		User:                author,
		Assignees:           assignees,
		Labels:              labelsFromRows(labelRows),
		Milestone:           milestone,
		CommentsCount:       issueRow.CommentsCount,
		Base:                endpoint(repo, pullRow.BaseRef, pullRow.BaseSHA),
		Head:                endpoint(repo, pullRow.HeadRef, pullRow.HeadSHA),
		Draft:               pullRow.Draft,
		MaintainerCanModify: pullRow.MaintainerCanModify,
		Merged:              pullRow.Merged,
		MergedAt:            pullRow.MergedAt,
		MergedBy:            mergedBy,
		MergeCommitSHA:      pullRow.MergeCommitSHA,
		Mergeable:           pullRow.Mergeable,
		MergeableState:      pullRow.MergeableState,
		Rebaseable:          pullRow.Rebaseable,
		Additions:           pullRow.Additions,
		Deletions:           pullRow.Deletions,
		ChangedFiles:        pullRow.ChangedFiles,
		CommitsCount:        pullRow.CommitsCount,
		ClosedAt:            issueRow.ClosedAt,
		CreatedAt:           issueRow.CreatedAt,
		UpdatedAt:           issueRow.UpdatedAt,
	}
	return pr, nil
}

// linearHistory reports whether every commit a pull request adds has a single
// parent, the precondition a clean rebase needs.
func (s *PRService) linearHistory(ctx context.Context, repoPK int64, base, head string) bool {
	commits, err := s.gitStore.CommitsBetween(ctx, repoPK, base, head)
	if err != nil {
		return false
	}
	for _, c := range commits {
		if len(c.Parents) != 1 {
			return false
		}
	}
	return true
}

// publishMergeRef writes a real merge commit for a clean test merge and points
// refs/pull/<n>/merge at it, so a client can fetch the would-be merge result. A
// failure here does not fail the recompute; the merge ref is a convenience.
func (s *PRService) publishMergeRef(ctx context.Context, pullRow *store.PullRow, base, head string) {
	bot := git.Signature{Name: "githome", Email: "githome@localhost", When: nowUTC()}
	sha, ok, err := s.gitStore.Merge(ctx, pullRow.RepoPK, git.MergeCommit, base, head,
		"Merge "+head+" into "+base, bot, bot)
	if err != nil || !ok {
		return
	}
	issueRow, err := s.store.GetIssueByPK(ctx, pullRow.IssuePK)
	if err != nil {
		return
	}
	_ = s.upsertRef(ctx, pullRow.RepoPK, mergeRef(issueRow.Number), sha)
}

// upsertRef creates ref at sha, or moves it there if it already exists, the
// write the synthetic pull refs need since they are rewritten on every push.
func (s *PRService) upsertRef(ctx context.Context, repoPK int64, ref, sha string) error {
	err := s.gitStore.CreateRef(ctx, repoPK, ref, sha)
	if errors.Is(err, git.ErrRefExists) {
		return s.gitStore.UpdateRef(ctx, repoPK, ref, sha, true)
	}
	return err
}

// enqueueRecompute submits a mergeability recompute for a pull request, deduped
// by issue so a burst of pushes collapses into one pending recompute.
func (s *PRService) enqueueRecompute(ctx context.Context, issuePK int64) {
	key := "mergeability:issue:" + strconv.FormatInt(issuePK, 10)
	payload, err := json.Marshal(recomputePayload{IssuePK: issuePK})
	if err != nil {
		return
	}
	_, _ = s.enq.Enqueue(ctx, JobRecomputeMergeability, string(payload), key)
}

// recordPullEvent appends a pull_request activity event and enqueues its webhook
// fan-out. The actor, the repository, and the pull request's issue row are the
// coordinates the renderer rebuilds the payload from; delivery is best-effort,
// so a failure here never fails the user's write.
func (s *PRService) recordPullEvent(ctx context.Context, actorPK int64, action string, repo *Repo, issuePK int64) {
	pk := issuePK
	recordEvent(ctx, s.store, s.enq, &store.EventRow{
		Event:   EventPullRequest,
		Action:  action,
		ActorPK: actorPK,
		RepoPK:  repo.PK,
		IssuePK: &pk,
		Public:  !repo.Private,
	}, nil)
}

// recomputePayload is the JSON body of a recompute_mergeability job. The worker
// handler decodes the same shape to dispatch back into the service.
type recomputePayload struct {
	IssuePK int64 `json:"issue_pk"`
}

// endpoint builds a same-repository git endpoint: the owner:ref label, the short
// ref, the tip sha, and the repository and its owner on both sides.
func endpoint(repo *Repo, ref, sha string) GitEndpoint {
	owner := ""
	if repo.Owner != nil {
		owner = repo.Owner.Login
	}
	return GitEndpoint{
		Label: owner + ":" + ref,
		Ref:   ref,
		SHA:   sha,
		Repo:  repo,
		User:  repo.Owner,
	}
}

// headLabel is the owner:ref form of a pull request's head, for the default
// merge commit message.
func headLabel(repo *Repo, pullRow *store.PullRow) string {
	owner := ""
	if repo.Owner != nil {
		owner = repo.Owner.Login
	}
	return owner + ":" + pullRow.HeadRef
}

// mergeMessage builds the commit message for a merge, honoring an explicit title
// and body and otherwise using GitHub's defaults per strategy.
func mergeMessage(method git.MergeMethod, number int64, issueRow *store.IssueRow, in MergeInput, headLabel string) string {
	title := in.CommitTitle
	if title == "" {
		switch method {
		case git.MergeSquash:
			title = fmt.Sprintf("%s (#%d)", issueRow.Title, number)
		default:
			title = fmt.Sprintf("Merge pull request #%d from %s", number, headLabel)
		}
	}
	body := in.CommitMessage
	if body == "" && method == git.MergeCommit {
		body = issueRow.Title
	}
	if body == "" {
		return title + "\n"
	}
	return title + "\n\n" + body + "\n"
}

// mergeableState maps a test-merge outcome to GitHub's mergeable_state string.
// A draft pull request is "draft" regardless; a conflicting merge is "dirty"; a
// clean merge behind its base is "behind"; an otherwise clean merge is "clean".
// The blocked, unstable, and has_hooks states wait on the review and check
// milestones that produce them.
func mergeableState(pullRow *store.PullRow, clean bool, behind int) string {
	switch {
	case pullRow.Draft:
		return "draft"
	case !clean:
		return "dirty"
	case behind > 0:
		return "behind"
	default:
		return "clean"
	}
}

// prSignature builds the git identity for a merge commit from the merging user,
// falling back to the login and a noreply address when the profile is sparse.
func prSignature(u *User) git.Signature {
	name := u.Login
	if u.Name != nil && *u.Name != "" {
		name = *u.Name
	}
	email := u.Login + "@users.noreply.githome.local"
	if u.Email != nil && *u.Email != "" {
		email = *u.Email
	}
	return git.Signature{Name: name, Email: email, When: nowUTC()}
}

// headRef and mergeRef name the synthetic refs a pull request publishes.
func headRef(number int64) string  { return "refs/pull/" + strconv.FormatInt(number, 10) + "/head" }
func mergeRef(number int64) string { return "refs/pull/" + strconv.FormatInt(number, 10) + "/merge" }

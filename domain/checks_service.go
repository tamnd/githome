package domain

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
	"github.com/tamnd/githome/worker"
)

// ErrCheckNotFound is returned when no check run matches the lookup in a visible
// repository.
var ErrCheckNotFound = errors.New("domain: check run not found")

// ChecksService implements the commit status and check run subsystem. Both are
// reported against a head sha by a client with write access and read back per sha
// to form the combined status and the status check rollup. The service leans on
// the repo service for visibility and authorization and on the git store to
// resolve a ref to the sha a status or check anchors to.
type ChecksService struct {
	store    checksStore
	repos    *RepoService
	issues   *IssueService
	gitStore *git.Store
	enq      worker.Enqueuer
}

// checksStore is the slice of the store the checks service needs.
type checksStore interface {
	UserByPK(ctx context.Context, pk int64) (*store.UserRow, error)
	OpenPullsByHeadSHA(ctx context.Context, repoPK int64, headSHA string) ([]store.PullRow, error)

	InsertCommitStatus(ctx context.Context, st *store.CommitStatusRow) error
	ListCommitStatuses(ctx context.Context, repoPK int64, sha string) ([]store.CommitStatusRow, error)

	EnsureCheckSuite(ctx context.Context, repoPK int64, headSHA, appSlug string) (*store.CheckSuiteRow, error)
	ListCheckSuites(ctx context.Context, repoPK int64, headSHA string) ([]store.CheckSuiteRow, error)
	SetCheckSuiteState(ctx context.Context, pk int64, status string, conclusion *string) error

	InsertCheckRun(ctx context.Context, r *store.CheckRunRow) error
	UpdateCheckRun(ctx context.Context, r *store.CheckRunRow) error
	GetCheckRun(ctx context.Context, dbID int64) (*store.CheckRunRow, error)
	ListCheckRunsForRef(ctx context.Context, repoPK int64, headSHA string) ([]store.CheckRunRow, error)
	ListCheckRunsForSuite(ctx context.Context, suitePK int64) ([]store.CheckRunRow, error)

	EnqueueJob(ctx context.Context, j *store.JobRow) (bool, error)
}

// NewChecksService builds a ChecksService over the store, the repo and issue
// services, and the git store.
func NewChecksService(st checksStore, repos *RepoService, issues *IssueService, gs *git.Store) *ChecksService {
	return &ChecksService{store: st, repos: repos, issues: issues, gitStore: gs, enq: worker.NewStoreEnqueuer(st)}
}

// StatusInput is the create payload for a commit status.
type StatusInput struct {
	State       string
	Context     string
	TargetURL   string
	Description string
}

// CheckRunInput is the create or update payload for a check run.
type CheckRunInput struct {
	Name          string
	HeadSHA       string
	Status        string
	Conclusion    string
	DetailsURL    string
	ExternalID    string
	OutputTitle   string
	OutputSummary string
	OutputText    string
}

// CreateStatus reports a commit status against a sha. It needs write access,
// validates the state, writes the row, and enqueues a decision recompute for
// every open pull request whose head is that sha so their rollup refreshes.
func (s *ChecksService) CreateStatus(ctx context.Context, actorPK int64, owner, name, sha string, in StatusInput) (*CommitStatus, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	state := strings.ToLower(strings.TrimSpace(in.State))
	if !validStatusState(state) {
		return nil, ErrValidation
	}
	resolved, err := s.resolveSHA(ctx, repo.PK, sha)
	if err != nil {
		return nil, ErrValidation
	}
	row := &store.CommitStatusRow{
		RepoPK: repo.PK, SHA: resolved, State: state, Context: strings.TrimSpace(in.Context),
		TargetURL: optStr(in.TargetURL), Description: optStr(in.Description), CreatorPK: &actorPK,
	}
	if err := s.store.InsertCommitStatus(ctx, row); err != nil {
		return nil, err
	}
	s.recomputeForSHA(ctx, repo.PK, resolved)
	return s.assembleStatus(ctx, row)
}

// ListStatuses returns every status reported against a ref for the viewer.
func (s *ChecksService) ListStatuses(ctx context.Context, viewerPK int64, owner, name, ref string) ([]*CommitStatus, string, error) {
	repo, sha, err := s.resolveForRead(ctx, viewerPK, owner, name, ref)
	if err != nil {
		return nil, "", err
	}
	rows, err := s.store.ListCommitStatuses(ctx, repo.PK, sha)
	if err != nil {
		return nil, "", err
	}
	out := make([]*CommitStatus, 0, len(rows))
	for i := range rows {
		st, err := s.assembleStatus(ctx, &rows[i])
		if err != nil {
			return nil, "", err
		}
		out = append(out, st)
	}
	return out, sha, nil
}

// CombinedStatus folds the latest status per context against a ref into one
// state, the body of the combined-status endpoint.
func (s *ChecksService) CombinedStatus(ctx context.Context, viewerPK int64, owner, name, ref string) (*CombinedStatus, error) {
	repo, sha, err := s.resolveForRead(ctx, viewerPK, owner, name, ref)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListCommitStatuses(ctx, repo.PK, sha)
	if err != nil {
		return nil, err
	}
	latest := latestPerContext(rows)
	statuses := make([]*CommitStatus, 0, len(latest))
	for i := range latest {
		st, err := s.assembleStatus(ctx, latest[i])
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, st)
	}
	return &CombinedStatus{
		State:      combinedState(latest),
		SHA:        sha,
		TotalCount: len(latest),
		Statuses:   statuses,
		Repo:       repo,
	}, nil
}

// CreateCheckRun reports a check run against a head sha, resolving or creating its
// suite first. It needs write access.
func (s *ChecksService) CreateCheckRun(ctx context.Context, actorPK int64, owner, name string, in CheckRunInput) (*CheckRun, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, ErrValidation
	}
	sha, err := s.resolveSHA(ctx, repo.PK, in.HeadSHA)
	if err != nil {
		return nil, ErrValidation
	}
	suite, err := s.store.EnsureCheckSuite(ctx, repo.PK, sha, "githome")
	if err != nil {
		return nil, err
	}
	row := &store.CheckRunRow{
		SuitePK: suite.PK, RepoPK: repo.PK, HeadSHA: sha, Name: strings.TrimSpace(in.Name),
		Status: statusOrDefault(in.Status), Conclusion: optStr(in.Conclusion),
		DetailsURL: optStr(in.DetailsURL), ExternalID: optStr(in.ExternalID),
		OutputTitle: optStr(in.OutputTitle), OutputSummary: optStr(in.OutputSummary),
		OutputText: optStr(in.OutputText),
	}
	stampRunTimes(row)
	if err := s.store.InsertCheckRun(ctx, row); err != nil {
		return nil, err
	}
	s.refreshSuite(ctx, suite.PK)
	s.recomputeForSHA(ctx, repo.PK, sha)
	return s.assembleRun(row), nil
}

// UpdateCheckRun rolls a check run forward, the transition a run finishing or
// reporting progress performs. It needs write access.
func (s *ChecksService) UpdateCheckRun(ctx context.Context, actorPK int64, owner, name string, runDBID int64, in CheckRunInput) (*CheckRun, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetCheckRun(ctx, runDBID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && row.RepoPK != repo.PK) {
		return nil, ErrCheckNotFound
	}
	if err != nil {
		return nil, err
	}
	if in.Name != "" {
		row.Name = strings.TrimSpace(in.Name)
	}
	if in.Status != "" {
		row.Status = in.Status
	}
	if in.Conclusion != "" {
		row.Conclusion = optStr(in.Conclusion)
	}
	if in.DetailsURL != "" {
		row.DetailsURL = optStr(in.DetailsURL)
	}
	if in.OutputTitle != "" {
		row.OutputTitle = optStr(in.OutputTitle)
	}
	if in.OutputSummary != "" {
		row.OutputSummary = optStr(in.OutputSummary)
	}
	stampRunTimes(row)
	if err := s.store.UpdateCheckRun(ctx, row); err != nil {
		return nil, err
	}
	s.refreshSuite(ctx, row.SuitePK)
	s.recomputeForSHA(ctx, repo.PK, row.HeadSHA)
	return s.assembleRun(row), nil
}

// GetCheckRun resolves one check run by id for the viewer.
func (s *ChecksService) GetCheckRun(ctx context.Context, viewerPK int64, owner, name string, runDBID int64) (*CheckRun, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetCheckRun(ctx, runDBID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && row.RepoPK != repo.PK) {
		return nil, ErrCheckNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.assembleRun(row), nil
}

// ListCheckRuns returns every check run reported against a ref for the viewer.
func (s *ChecksService) ListCheckRuns(ctx context.Context, viewerPK int64, owner, name, ref string) ([]*CheckRun, string, error) {
	repo, sha, err := s.resolveForRead(ctx, viewerPK, owner, name, ref)
	if err != nil {
		return nil, "", err
	}
	rows, err := s.store.ListCheckRunsForRef(ctx, repo.PK, sha)
	if err != nil {
		return nil, "", err
	}
	out := make([]*CheckRun, 0, len(rows))
	for i := range rows {
		out = append(out, s.assembleRun(&rows[i]))
	}
	return out, sha, nil
}

// ListCheckSuites returns the check suites reported against a ref for the viewer,
// each carrying its check runs.
func (s *ChecksService) ListCheckSuites(ctx context.Context, viewerPK int64, owner, name, ref string) ([]*CheckSuite, string, error) {
	repo, sha, err := s.resolveForRead(ctx, viewerPK, owner, name, ref)
	if err != nil {
		return nil, "", err
	}
	rows, err := s.store.ListCheckSuites(ctx, repo.PK, sha)
	if err != nil {
		return nil, "", err
	}
	out := make([]*CheckSuite, 0, len(rows))
	for i := range rows {
		runRows, err := s.store.ListCheckRunsForSuite(ctx, rows[i].PK)
		if err != nil {
			return nil, "", err
		}
		runs := make([]*CheckRun, 0, len(runRows))
		for j := range runRows {
			runs = append(runs, s.assembleRun(&runRows[j]))
		}
		out = append(out, &CheckSuite{
			PK: rows[i].PK, ID: rows[i].DBID, RepoPK: rows[i].RepoPK, HeadSHA: rows[i].HeadSHA,
			AppSlug: rows[i].AppSlug, Status: rows[i].Status, Conclusion: rows[i].Conclusion,
			Runs: runs, CreatedAt: rows[i].CreatedAt, UpdatedAt: rows[i].UpdatedAt,
		})
	}
	return out, sha, nil
}

// Rollup folds a ref's statuses and check runs into the status check rollup for
// the viewer, the value the pull request and its head commit surface.
func (s *ChecksService) Rollup(ctx context.Context, viewerPK int64, owner, name, ref string) (*StatusCheckRollup, error) {
	repo, sha, err := s.resolveForRead(ctx, viewerPK, owner, name, ref)
	if err != nil {
		return nil, err
	}
	return s.rollupForSHA(ctx, repo, sha)
}

// RollupForPull builds the rollup at a pull request's recorded head, the path the
// GraphQL pull request resolver takes without re-resolving a ref.
func (s *ChecksService) RollupForPull(ctx context.Context, repo *Repo, headSHA string) (*StatusCheckRollup, error) {
	return s.rollupForSHA(ctx, repo, headSHA)
}

func (s *ChecksService) rollupForSHA(ctx context.Context, repo *Repo, sha string) (*StatusCheckRollup, error) {
	statusRows, err := s.store.ListCommitStatuses(ctx, repo.PK, sha)
	if err != nil {
		return nil, err
	}
	runRows, err := s.store.ListCheckRunsForRef(ctx, repo.PK, sha)
	if err != nil {
		return nil, err
	}
	latest := latestPerContext(statusRows)
	statuses := make([]*CommitStatus, 0, len(latest))
	for i := range latest {
		st, err := s.assembleStatus(ctx, latest[i])
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, st)
	}
	runs := make([]*CheckRun, 0, len(runRows))
	for i := range runRows {
		runs = append(runs, s.assembleRun(&runRows[i]))
	}
	return &StatusCheckRollup{
		State:      rollupState(asStatusRows(latest), runRows),
		SHA:        sha,
		Statuses:   statuses,
		CheckRuns:  runs,
		TotalCount: len(latest) + len(runRows),
	}, nil
}

// refreshSuite rolls a suite's status and conclusion to summarize its runs.
func (s *ChecksService) refreshSuite(ctx context.Context, suitePK int64) {
	runs, err := s.store.ListCheckRunsForSuite(ctx, suitePK)
	if err != nil {
		return
	}
	status, conclusion := suiteSummary(runs)
	_ = s.store.SetCheckSuiteState(ctx, suitePK, status, conclusion)
}

// recomputeForSHA enqueues a decision recompute for every open pull request whose
// head is the sha, so a status or check report refreshes their cached rollup.
func (s *ChecksService) recomputeForSHA(ctx context.Context, repoPK int64, sha string) {
	pulls, err := s.store.OpenPullsByHeadSHA(ctx, repoPK, sha)
	if err != nil {
		return
	}
	for i := range pulls {
		key := "review_decision:issue:" + strconv.FormatInt(pulls[i].IssuePK, 10)
		payload, err := json.Marshal(recomputePayload{IssuePK: pulls[i].IssuePK})
		if err != nil {
			continue
		}
		_, _ = s.enq.Enqueue(ctx, JobRecomputeReviewDecision, string(payload), key)
	}
}

// resolveForRead resolves the repository and a ref to its sha for a viewer.
func (s *ChecksService) resolveForRead(ctx context.Context, viewerPK int64, owner, name, ref string) (*Repo, string, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, "", err
	}
	sha, err := s.resolveSHA(ctx, repo.PK, ref)
	if err != nil {
		return nil, "", ErrValidation
	}
	return repo, sha, nil
}

// resolveSHA turns a ref (a branch, a tag, or a sha already) into the commit sha
// a status or check anchors to.
func (s *ChecksService) resolveSHA(ctx context.Context, repoPK int64, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", git.ErrRefNotFound
	}
	for _, full := range []string{"refs/heads/" + ref, "refs/tags/" + ref, ref} {
		if sha, err := s.gitStore.RefSHA(ctx, repoPK, full); err == nil {
			return sha, nil
		}
	}
	if isHexSHA(ref) {
		if ok, _ := s.gitStore.ObjectExists(ctx, repoPK, ref); ok {
			return ref, nil
		}
	}
	return "", git.ErrRefNotFound
}

func (s *ChecksService) assembleStatus(ctx context.Context, row *store.CommitStatusRow) (*CommitStatus, error) {
	var creator *User
	if row.CreatorPK != nil {
		u, err := s.issues.userByPK(ctx, *row.CreatorPK)
		if err != nil {
			return nil, err
		}
		creator = u
	}
	return &CommitStatus{
		PK: row.PK, ID: row.DBID, RepoPK: row.RepoPK, SHA: row.SHA, State: row.State,
		Context: row.Context, TargetURL: row.TargetURL, Description: row.Description,
		Creator: creator, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

func (s *ChecksService) assembleRun(row *store.CheckRunRow) *CheckRun {
	return &CheckRun{
		PK: row.PK, ID: row.DBID, SuitePK: row.SuitePK, RepoPK: row.RepoPK,
		HeadSHA: row.HeadSHA, Name: row.Name, Status: row.Status, Conclusion: row.Conclusion,
		DetailsURL: row.DetailsURL, ExternalID: row.ExternalID,
		OutputTitle: row.OutputTitle, OutputSummary: row.OutputSummary, OutputText: row.OutputText,
		StartedAt: row.StartedAt, CompletedAt: row.CompletedAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

// combinedState folds the latest status per context into a combined state:
// failure if any errored or failed, pending if any is pending or there are none,
// otherwise success. Error folds into failure in this statuses-only vocabulary.
func combinedState(latest []*store.CommitStatusRow) string {
	if len(latest) == 0 {
		return "pending"
	}
	anyPending := false
	for _, st := range latest {
		switch st.State {
		case "error", "failure":
			return "failure"
		case "pending":
			anyPending = true
		}
	}
	if anyPending {
		return "pending"
	}
	return "success"
}

// rollupState folds every status and check run on a sha into one rollup state,
// worst first: ERROR, then FAILURE, then PENDING, then SUCCESS, then EXPECTED for
// an empty set.
func rollupState(statuses []store.CommitStatusRow, runs []store.CheckRunRow) string {
	worst := rollupExpectedRank
	for i := range statuses {
		worst = min(worst, rankStatus(statuses[i].State))
	}
	for i := range runs {
		worst = min(worst, rankRun(runs[i]))
	}
	switch worst {
	case rollupErrorRank:
		return RollupError
	case rollupFailureRank:
		return RollupFailure
	case rollupPendingRank:
		return RollupPending
	case rollupSuccessRank:
		return RollupSuccess
	default:
		return RollupExpected
	}
}

// The rollup ranks, lowest is worst, so the minimum rank present wins.
const (
	rollupErrorRank = iota
	rollupFailureRank
	rollupPendingRank
	rollupSuccessRank
	rollupExpectedRank
)

func rankStatus(state string) int {
	switch state {
	case "error":
		return rollupErrorRank
	case "failure":
		return rollupFailureRank
	case "pending":
		return rollupPendingRank
	case "success":
		return rollupSuccessRank
	default:
		return rollupExpectedRank
	}
}

func rankRun(r store.CheckRunRow) int {
	if r.Status != "completed" {
		return rollupPendingRank
	}
	if r.Conclusion == nil {
		return rollupSuccessRank
	}
	switch *r.Conclusion {
	case "failure", "timed_out", "startup_failure":
		return rollupFailureRank
	case "action_required", "cancelled", "stale":
		return rollupErrorRank
	case "success", "neutral", "skipped":
		return rollupSuccessRank
	default:
		return rollupSuccessRank
	}
}

// suiteSummary rolls a suite's runs into its status and conclusion: in_progress
// until every run completes, then the worst conclusion as its verdict.
func suiteSummary(runs []store.CheckRunRow) (string, *string) {
	if len(runs) == 0 {
		return "queued", nil
	}
	for i := range runs {
		if runs[i].Status != "completed" {
			return "in_progress", nil
		}
	}
	verdict := "success"
	for i := range runs {
		if runs[i].Conclusion != nil {
			switch *runs[i].Conclusion {
			case "failure", "timed_out", "startup_failure":
				verdict = "failure"
			case "action_required", "cancelled":
				if verdict != "failure" {
					verdict = *runs[i].Conclusion
				}
			}
		}
	}
	return "completed", &verdict
}

// latestPerContext keeps the newest status per context from a list ordered newest
// first, the set the combined state and rollup fold.
func latestPerContext(rows []store.CommitStatusRow) []*store.CommitStatusRow {
	seen := map[string]bool{}
	var out []*store.CommitStatusRow
	for i := range rows {
		if seen[rows[i].Context] {
			continue
		}
		seen[rows[i].Context] = true
		out = append(out, &rows[i])
	}
	return out
}

// asStatusRows flattens a slice of row pointers back to values for rollupState.
func asStatusRows(ptrs []*store.CommitStatusRow) []store.CommitStatusRow {
	out := make([]store.CommitStatusRow, 0, len(ptrs))
	for _, p := range ptrs {
		out = append(out, *p)
	}
	return out
}

func validStatusState(state string) bool {
	switch state {
	case "error", "failure", "pending", "success":
		return true
	default:
		return false
	}
}

func statusOrDefault(status string) string {
	if strings.TrimSpace(status) == "" {
		return "queued"
	}
	return status
}

// stampRunTimes fills started_at when a run begins and completed_at when it
// finishes, so the timeline matches the status without the caller passing them.
func stampRunTimes(row *store.CheckRunRow) {
	if row.Status != "queued" && row.StartedAt == nil {
		now := nowUTC()
		row.StartedAt = &now
	}
	if row.Status == "completed" && row.CompletedAt == nil {
		now := nowUTC()
		row.CompletedAt = &now
	}
}

func optStr(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

// isHexSHA reports whether s is a full 40-character hex object id.
func isHexSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

package domain

import "time"

// PullRequest is the domain view of a pull request: the issue it shares its
// number, title, body, and state with, plus the git coordinates and merge state
// the pull_requests extension carries. ID is the pull request's own public
// database id (pull_requests.db_id), the value a PullRequest node id decodes to;
// IssueID is the underlying issue row's db_id, which the REST id field renders,
// matching GitHub where a pull request and its issue share one id space.
//
// Mergeable, Rebaseable, MergedBy, MergedAt, MergeCommitSHA, and ClosedAt are
// pointers because a pull request acquires them only over its lifetime; a nil
// Mergeable is the not-yet-computed state the API surfaces as null, the
// null-then-value contract the mergeability worker resolves.
type PullRequest struct {
	PK      int64
	ID      int64
	IssueID int64
	Number  int64
	RepoPK  int64
	Repo    *Repo

	Title  string
	Body   *string
	State  string // open | closed
	Locked bool
	// ActiveLockReason mirrors the issue-side lock reason; nil when unlocked.
	ActiveLockReason *string
	User             *User
	Assignees        []*User
	Labels           []*Label
	Milestone        *Milestone
	CommentsCount    int
	// RequestedReviewers are the users whose review is currently requested,
	// in request order. A submitted review clears its author's request on
	// GitHub; here the set changes only through the request endpoints.
	RequestedReviewers []*User

	Base GitEndpoint
	Head GitEndpoint

	Draft               bool
	MaintainerCanModify bool

	Merged         bool
	MergedAt       *time.Time
	MergedBy       *User
	MergeCommitSHA *string
	Mergeable      *bool
	MergeableState string
	Rebaseable     *bool
	Additions      int
	Deletions      int
	ChangedFiles   int
	CommitsCount   int

	ClosedAt  *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GitEndpoint is one side of a pull request, a base or a head. Label is the
// "owner:branch" form GitHub renders; Ref is the short branch name; SHA is the
// tip the pull request recorded. Repo and User are the repository the ref lives
// in and its owner; for a same-repository pull request both sides point at the
// one repository.
type GitEndpoint struct {
	Label string
	Ref   string
	SHA   string
	Repo  *Repo
	User  *User
}

// MergeState is the worker-derived merge state of a pull request: the tri-state
// mergeable, the GitHub mergeable_state string, the rebaseable flag, the diff
// stats, and the commit count. The worker computes it and SetMergeability
// persists it; nil Mergeable is the unknown state.
type MergeState struct {
	Mergeable    *bool
	State        string
	Rebaseable   *bool
	Additions    int
	Deletions    int
	ChangedFiles int
	Commits      int
}

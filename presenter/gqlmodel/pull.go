package gqlmodel

// This file holds the GraphQL wire types for the pull request surface: the
// PullRequest object, its Relay connection, and the changed-file and commit
// connections gh pr view and gh pr diff select. The fields are the subset those
// documents read; the type grows toward GitHub's full PullRequest with later
// milestones. Nullable GraphQL fields use Go pointers so gqlgen renders null
// rather than a zero value, and Int fields are int32 to match the gqlgen Int
// binding.

// PullRequestState is the GraphQL PullRequestState enum: a pull request is OPEN,
// CLOSED, or MERGED. A merged pull request reports MERGED even though its issue
// is closed, the distinction GitHub draws.
type PullRequestState string

// The PullRequestState values.
const (
	PullRequestStateOpen   PullRequestState = "OPEN"
	PullRequestStateClosed PullRequestState = "CLOSED"
	PullRequestStateMerged PullRequestState = "MERGED"
)

// MergeableState is the GraphQL MergeableState enum, the tri-state the worker
// resolves: MERGEABLE for a clean test merge, CONFLICTING for one that conflicts,
// and UNKNOWN while the recompute has not yet run.
type MergeableState string

// The MergeableState values.
const (
	MergeableStateMergeable   MergeableState = "MERGEABLE"
	MergeableStateConflicting MergeableState = "CONFLICTING"
	MergeableStateUnknown     MergeableState = "UNKNOWN"
)

// MergeStateStatus is the GraphQL MergeStateStatus enum, GitHub's richer view of
// why a pull request can or cannot merge. Githome resolves CLEAN, DIRTY, BEHIND,
// DRAFT, and UNKNOWN today; BLOCKED, UNSTABLE, and HAS_HOOKS arrive with the
// review and check milestones that produce them.
type MergeStateStatus string

// The MergeStateStatus values.
const (
	MergeStateStatusBehind   MergeStateStatus = "BEHIND"
	MergeStateStatusBlocked  MergeStateStatus = "BLOCKED"
	MergeStateStatusClean    MergeStateStatus = "CLEAN"
	MergeStateStatusDirty    MergeStateStatus = "DIRTY"
	MergeStateStatusDraft    MergeStateStatus = "DRAFT"
	MergeStateStatusHasHooks MergeStateStatus = "HAS_HOOKS"
	MergeStateStatusUnknown  MergeStateStatus = "UNKNOWN"
	MergeStateStatusUnstable MergeStateStatus = "UNSTABLE"
)

// PatchStatus is the GraphQL PatchStatus enum: how a file changed across a pull
// request's diff.
type PatchStatus string

// The PatchStatus values.
const (
	PatchStatusAdded    PatchStatus = "ADDED"
	PatchStatusDeleted  PatchStatus = "DELETED"
	PatchStatusModified PatchStatus = "MODIFIED"
	PatchStatusRenamed  PatchStatus = "RENAMED"
	PatchStatusCopied   PatchStatus = "COPIED"
	PatchStatusChanged  PatchStatus = "CHANGED"
)

// PullRequest is the GraphQL PullRequest object, reduced to the fields gh pr view
// and gh pr diff select. mergeable and mergeStateStatus are the worker-resolved
// merge view; until the recompute runs mergeable is UNKNOWN and mergeStateStatus
// is UNKNOWN.
type PullRequest struct {
	ID                  string                   // the PullRequest node ID
	Number              int32                    // the per-repository pull request number
	Title               string                   // the pull request title
	Body                string                   // the body, empty string when unset
	State               PullRequestState         // OPEN, CLOSED, or MERGED
	URL                 URI                      // the pull request's HTML URL
	Locked              bool                     // whether the conversation is locked
	Closed              bool                     // whether the pull request is closed or merged
	IsDraft             bool                     // whether the pull request is a draft
	Merged              bool                     // whether the pull request has merged
	MergedAt            *DateTime                // null while unmerged
	Mergeable           MergeableState           // the tri-state mergeable
	MergeStateStatus    MergeStateStatus         // the richer merge state
	Author              Actor                    // null for a ghost author
	AuthorAssociation   CommentAuthorAssociation // the author's repository association
	BaseRefName         string                   // the base branch name
	HeadRefName         string                   // the head branch name
	BaseRefOid          GitObjectID              // the recorded base tip
	HeadRefOid          GitObjectID              // the recorded head tip
	IsCrossRepository   bool                     // whether the head lives in another repository
	MaintainerCanModify bool                     // whether maintainers can push the head branch
	HeadRepository      *Repository              // the head repository; null when the fork is gone
	HeadRepositoryOwner RepositoryOwner          // the head repository's owner
	FullDatabaseID      *BigInt                  // the BigInt twin of the REST id
	MergedBy            Actor                    // who merged the pull request; null while unmerged
	Additions           int32                    // lines added across the diff
	Deletions           int32                    // lines removed across the diff
	ChangedFiles        int32                    // files touched by the diff
	CreatedAt           DateTime                 // creation instant
	UpdatedAt           DateTime                 // last-update instant
	ClosedAt            *DateTime                // null while open
	Labels              *LabelConnection         // resolved on demand
	Assignees           *UserConnection          // resolved on demand
	Milestone           *Milestone               // resolved on demand
	BaseRef             *Ref                     // resolved on demand (carries the Ref node ID)
	HeadRef             *Ref                     // resolved on demand (carries the Ref node ID)
	AutoMergeRequest    *AutoMergeRequest        // null unless auto-merge is enabled
	IsInMergeQueue      bool                     // always false; Githome has no merge queue
	ReactionGroups      []ReactionGroup          // emoji reaction counts; always non-nil (empty when none)

	// RepoOwner and RepoName carry the repository coordinates so the files and
	// commits field resolvers can read them through the domain. They are not part
	// of the GraphQL schema, so gqlgen ignores them; the presenter fills them.
	RepoOwner string
	RepoName  string

	// IssuePK is the database primary key of the underlying issue row; it is not
	// part of the GraphQL schema but is used by the assignees/labels/milestone
	// field resolvers to avoid re-querying the domain for the same data.
	IssuePK int64

	// CommentsCount is the cached count of issue-level comments; used by the
	// comments resolver to return the correct totalCount without a second query.
	CommentsCount int32

	// CommitsCount is the cached count of the pull request's own commits, the
	// commits_count column the mergeability recompute maintains alongside
	// additions/deletions/changedFiles. The commits resolver answers a
	// count-only selection from it without forking git.
	CommitsCount int32
}

// AutoMergeRequest is the auto-merge configuration on a pull request. GitHub
// sets it when auto-merge is enabled; Githome always returns null because it
// does not implement auto-merge queuing.
type AutoMergeRequest struct {
	AuthorEmail    *string
	CommitBody     *string
	CommitHeadline *string
	EnabledAt      *DateTime
	EnabledBy      Actor
	MergeMethod    PullRequestMergeMethod
}

// IsNode marks PullRequest as implementing the Node interface.
func (PullRequest) IsNode() {}

// GetID satisfies the Node interface getter gqlgen requires.
func (p PullRequest) GetID() string { return p.ID }

// IsLabelableNode marks PullRequest as a member of the LabelableNode union type.
func (PullRequest) IsLabelableNode() {}

// IsAssignableNode marks PullRequest as a member of the AssignableNode union type.
func (PullRequest) IsAssignableNode() {}

// IsSearchResultItem marks PullRequest as a member of the SearchResultItem union.
func (PullRequest) IsSearchResultItem() {}

// PullRequestConnection is the Relay connection over a repository's pull
// requests.
type PullRequestConnection struct {
	Nodes      []*PullRequest
	Edges      []*PullRequestEdge
	PageInfo   *PageInfo
	TotalCount int32
}

// PullRequestEdge pairs a pull request with its opaque pagination cursor.
type PullRequestEdge struct {
	Cursor string
	Node   *PullRequest
}

// PullRequestMergeMethod is the GraphQL enum of supported merge strategies.
type PullRequestMergeMethod string

// The PullRequestMergeMethod values.
const (
	PullRequestMergeMethodMerge  PullRequestMergeMethod = "MERGE"
	PullRequestMergeMethodSquash PullRequestMergeMethod = "SQUASH"
	PullRequestMergeMethodRebase PullRequestMergeMethod = "REBASE"
)

// PullRequestReviewEvent is the GraphQL enum of review events: the action that
// a submitted review carries.
type PullRequestReviewEvent string

// The PullRequestReviewEvent values.
const (
	PullRequestReviewEventApprove        PullRequestReviewEvent = "APPROVE"
	PullRequestReviewEventComment        PullRequestReviewEvent = "COMMENT"
	PullRequestReviewEventRequestChanges PullRequestReviewEvent = "REQUEST_CHANGES"
	PullRequestReviewEventDismiss        PullRequestReviewEvent = "DISMISS"
)

// DiffSide is the side of a diff a review comment anchors to.
type DiffSide string

// The DiffSide values.
const (
	DiffSideLeft  DiffSide = "LEFT"
	DiffSideRight DiffSide = "RIGHT"
)

// PullRequestChangedFile is one file's change in a pull request's diff.
type PullRequestChangedFile struct {
	Path       string
	Additions  int32
	Deletions  int32
	ChangeType PatchStatus
}

// PullRequestChangedFileConnection is the connection over a pull request's
// changed files.
type PullRequestChangedFileConnection struct {
	Nodes      []*PullRequestChangedFile
	PageInfo   *PageInfo
	TotalCount int32
}

// PullRequestCommit is one commit on a pull request, wrapping the underlying git
// commit the way GitHub's schema nests it.
type PullRequestCommit struct {
	URL    URI
	Commit *Commit
}

// Commit is the reduced GraphQL Commit: the object id and the message gh pr view
// reads. It grows toward GitHub's full Commit with the review milestone.
type Commit struct {
	Oid             GitObjectID
	Message         string
	MessageHeadline string

	// RepoOwner and RepoName carry the repository coordinates so the
	// statusCheckRollup field resolver can fold the commit's statuses and check
	// runs. They are not part of the GraphQL schema, so gqlgen ignores them; the
	// presenter fills them.
	RepoOwner string
	RepoName  string
}

// PullRequestCommitConnection is the connection over a pull request's commits.
type PullRequestCommitConnection struct {
	Nodes      []*PullRequestCommit
	PageInfo   *PageInfo
	TotalCount int32
}

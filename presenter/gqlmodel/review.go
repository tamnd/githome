package gqlmodel

// This file holds the GraphQL wire types for the code review surface: the
// reviewDecision enum, the review threads and their comments, and the status
// check rollup gh pr view and gh pr status select. Object types bind to these
// hand-written structs; the enums bind to the typed string constants. Nullable
// GraphQL fields use Go pointers so gqlgen renders null rather than a zero value.

// PullRequestReviewDecision is the GraphQL enum for a pull request's derived
// review state. Githome computes APPROVED and CHANGES_REQUESTED from the reviews;
// REVIEW_REQUIRED needs branch protection and arrives with that milestone.
type PullRequestReviewDecision string

// The PullRequestReviewDecision values.
const (
	PullRequestReviewDecisionChangesRequested PullRequestReviewDecision = "CHANGES_REQUESTED"
	PullRequestReviewDecisionApproved         PullRequestReviewDecision = "APPROVED"
	PullRequestReviewDecisionReviewRequired   PullRequestReviewDecision = "REVIEW_REQUIRED"
)

// StatusState is the GraphQL enum for a rollup or status state, worst first.
type StatusState string

// The StatusState values.
const (
	StatusStateError    StatusState = "ERROR"
	StatusStateExpected StatusState = "EXPECTED"
	StatusStateFailure  StatusState = "FAILURE"
	StatusStatePending  StatusState = "PENDING"
	StatusStateSuccess  StatusState = "SUCCESS"
)

// PullRequestReviewThread is the GraphQL view of a review conversation: a root
// comment and its replies. ID is the thread node id; the comments field is paged
// by its own resolver, so the presenter fills the connection the resolver returns.
type PullRequestReviewThread struct {
	ID         string // the PullRequestReviewThread node ID
	IsResolved bool   // whether the conversation is resolved
	IsOutdated bool   // whether the anchored line left the diff
	Path       string // the file the thread anchors to
	Line       *int32 // the anchored line, null when the thread is file-level
	Comments   *PullRequestReviewCommentConnection
}

// PullRequestReviewThreadConnection is the connection over a pull request's review
// threads.
type PullRequestReviewThreadConnection struct {
	Nodes      []*PullRequestReviewThread
	TotalCount int32
}

// PullRequestReviewComment is the GraphQL view of one inline review comment.
type PullRequestReviewComment struct {
	ID        string // the PullRequestReviewComment node ID
	Body      string // the comment body
	Path      string // the file the comment anchors to
	Author    *Actor // null for a ghost author
	Outdated  bool   // whether the anchored line left the diff
	URL       URI    // the comment's HTML URL
	CreatedAt DateTime
}

// PullRequestReviewCommentConnection is the connection over a thread's comments.
type PullRequestReviewCommentConnection struct {
	Nodes      []*PullRequestReviewComment
	TotalCount int32
}

// StatusCheckRollup is the GraphQL view of a head commit's combined status and
// check state.
type StatusCheckRollup struct {
	State StatusState
}

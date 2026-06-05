package domain

import "time"

// The domain views of the code review surface. A Review is one act of reviewing a
// pull request; a ReviewComment is anchored to a diff line and belongs to a
// review; a ReviewThread is the conversation a root comment and its replies form.
// reviewDecision is the single value that summarizes whether a pull request has
// the approvals it needs.

// Review states, the values a review row carries and the API renders.
const (
	ReviewPending          = "PENDING"
	ReviewApproved         = "APPROVED"
	ReviewChangesRequested = "CHANGES_REQUESTED"
	ReviewCommented        = "COMMENTED"
	ReviewDismissed        = "DISMISSED"
)

// Review events, the verbs a submit names (the gh pr review flags map onto them).
const (
	EventApprove        = "APPROVE"
	EventRequestChanges = "REQUEST_CHANGES"
	EventComment        = "COMMENT"
)

// Review is the domain view of one review. ID is the review's own public database
// id (the value a PullRequestReview node id decodes to). State is one of the
// Review* constants; SubmittedAt is nil while the review is still a pending draft.
type Review struct {
	PK               int64
	ID               int64
	PullPK           int64
	PullNumber       int64
	RepoPK           int64
	User             *User
	State            string
	Body             string
	CommitID         string
	DismissedMessage *string
	Comments         []*ReviewComment
	SubmittedAt      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ReviewComment is the domain view of one inline comment. Line and StartLine are
// file line numbers in the line/side model; Position is the legacy 1-based diff
// offset. Both are filled in: the service resolves whichever the caller omitted
// from the pull request's diff. InReplyTo is the root comment's id when this is a
// reply.
type ReviewComment struct {
	PK               int64
	ID               int64
	ReviewPK         int64
	ReviewID         int64
	PullPK           int64
	PullNumber       int64
	RepoPK           int64
	User             *User
	Path             string
	Side             string
	Line             *int64
	StartLine        *int64
	StartSide        *string
	Position         *int64
	OriginalPosition *int64
	CommitID         string
	OriginalCommitID string
	InReplyTo        *int64
	DiffHunk         string
	SubjectType      string
	Body             string
	Resolved         bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ReviewThread is a conversation: a root comment and the replies chained under
// it. ID is the root comment's thread node id. IsResolved follows the root's
// resolved flag; IsOutdated is true when the anchored line is no longer present
// in the pull request's current diff.
type ReviewThread struct {
	RootPK     int64
	ID         int64
	PullPK     int64
	Path       string
	Line       *int64
	IsResolved bool
	IsOutdated bool
	Comments   []*ReviewComment
}

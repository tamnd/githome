package gqlmodel

// This file holds the GraphQL wire types for the issue surface: the Issue object
// and the Relay connections, comments, labels, and the actor shape gh issue view
// and gh issue list select. The fields are the subset those documents read; the
// type grows toward the full GitHub Issue with later milestones. Nullable GraphQL
// fields use Go pointers so gqlgen renders null rather than a zero value.

// IssueState is the GraphQL IssueState enum: an issue is OPEN or CLOSED.
type IssueState string

// The IssueState values.
const (
	IssueStateOpen   IssueState = "OPEN"
	IssueStateClosed IssueState = "CLOSED"
)

// IssueStateReason is the GraphQL enum for why an issue is in its state. It is
// null for an open issue that has never been closed.
type IssueStateReason string

// The IssueStateReason values.
const (
	IssueStateReasonCompleted  IssueStateReason = "COMPLETED"
	IssueStateReasonNotPlanned IssueStateReason = "NOT_PLANNED"
	IssueStateReasonReopened   IssueStateReason = "REOPENED"
)

// Actor is the GraphQL Actor: the login and URLs gh selects for an issue or
// comment author. Githome models it as a concrete object carrying the fields the
// issue documents read; the full Actor interface with the User and Organization
// implementers arrives with the GraphQL parity milestone.
type Actor struct {
	Login     string // the actor's login
	URL       URI    // the actor's HTML URL
	AvatarURL URI    // the actor's avatar URL
}

// Issue is the GraphQL Issue object, reduced to the fields gh issue view and gh
// issue list select.
type Issue struct {
	ID          string            // the Issue node ID
	Number      int32             // the per-repository issue number
	Title       string            // the issue title
	Body        string            // the issue body, empty string when unset
	State       IssueState        // OPEN or CLOSED
	StateReason *IssueStateReason // null until the issue has been closed
	URL         URI               // the issue's HTML URL
	Locked      bool              // whether the conversation is locked
	Closed      bool              // whether the issue is closed
	Author      *Actor            // null for a ghost author (resolved by dataloader)
	CreatedAt   DateTime          // creation instant
	UpdatedAt   DateTime          // last-update instant
	ClosedAt    *DateTime         // null while open
	Labels      *LabelConnection  // the attached labels (resolved by dataloader)
	Comments    *IssueCommentConnection

	// RepoOwner and RepoName carry the repository coordinates so the comments
	// field resolver can page the issue's comments. They are not part of the
	// GraphQL schema, so gqlgen ignores them; the presenter fills them.
	RepoOwner string
	RepoName  string

	// PK and UserPK are not part of the GraphQL schema. They carry the database
	// primary keys the per-request dataloaders use to look up Author and Labels
	// without re-hitting the pre-assembled domain data.
	PK     int64
	UserPK int64
}

// IssueConnection is the Relay connection over a repository's issues.
type IssueConnection struct {
	Nodes      []*Issue
	Edges      []*IssueEdge
	PageInfo   *PageInfo
	TotalCount int32
}

// IssueEdge pairs an issue with its opaque pagination cursor.
type IssueEdge struct {
	Cursor string
	Node   *Issue
}

// PageInfo is the Relay page-info shared by every connection.
type PageInfo struct {
	HasNextPage     bool
	HasPreviousPage bool
	StartCursor     *string
	EndCursor       *string
}

// Label is the GraphQL Label object.
type Label struct {
	ID          string  // the Label node ID
	Name        string  // the label name
	Color       string  // the six-hex color, no leading hash
	Description *string // null when unset
}

// LabelConnection is the connection over an issue's or repository's labels. The
// issue documents read it as nodes plus totalCount, so the page-info and edges a
// full connection carries are added when a paginated label query needs them.
type LabelConnection struct {
	Nodes      []*Label
	TotalCount int32
}

// IssueComment is the GraphQL IssueComment object.
type IssueComment struct {
	ID        string   // the IssueComment node ID
	Body      string   // the comment body
	Author    *Actor   // null for a ghost author
	URL       URI      // the comment's HTML URL
	CreatedAt DateTime // creation instant
	UpdatedAt DateTime // last-update instant
}

// IssueCommentConnection is the connection over an issue's comments.
type IssueCommentConnection struct {
	Nodes      []*IssueComment
	TotalCount int32
}

// IsLabelableNode marks Issue as a member of the LabelableNode union type.
func (Issue) IsLabelableNode() {}

// IsAssignableNode marks Issue as a member of the AssignableNode union type.
func (Issue) IsAssignableNode() {}

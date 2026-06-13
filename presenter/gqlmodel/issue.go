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

// Actor is the GraphQL Actor interface: an entity that can author issues,
// comments, and reviews. User is the only implementer today; gh's
// `... on User` inline fragments dispatch on the concrete type.
type Actor interface {
	IsActor()
}

// ReactionContent is the GraphQL ReactionContent enum.
type ReactionContent string

// The ReactionContent values.
const (
	ReactionContentThumbsUp   ReactionContent = "THUMBS_UP"
	ReactionContentThumbsDown ReactionContent = "THUMBS_DOWN"
	ReactionContentLaugh      ReactionContent = "LAUGH"
	ReactionContentHooray     ReactionContent = "HOORAY"
	ReactionContentConfused   ReactionContent = "CONFUSED"
	ReactionContentHeart      ReactionContent = "HEART"
	ReactionContentRocket     ReactionContent = "ROCKET"
	ReactionContentEyes       ReactionContent = "EYES"
)

// ReactingUserConnection is the slim connection gh reads for reaction counts.
type ReactingUserConnection struct {
	TotalCount int32
}

// ReactionGroup summarises how many users reacted with a given emoji on an issue.
type ReactionGroup struct {
	Content ReactionContent
	Users   ReactingUserConnection
}

// Reaction is a single emoji reaction a user left on a reactable subject, the
// node addReaction and removeReaction return.
type Reaction struct {
	ID        string          // the Reaction node ID
	Content   ReactionContent // the emoji
	User      *User           // who reacted; null for a ghost user
	CreatedAt DateTime        // when the reaction was added
}

// IsNode marks Reaction as implementing the Node interface.
func (Reaction) IsNode() {}

// GetID satisfies the Node interface getter gqlgen requires.
func (r Reaction) GetID() string { return r.ID }

// Issue is the GraphQL Issue object, reduced to the fields gh issue view and gh
// issue list select.
type Issue struct {
	ID             string            // the Issue node ID
	Number         int32             // the per-repository issue number
	Title          string            // the issue title
	Body           string            // the issue body, empty string when unset
	State          IssueState        // OPEN or CLOSED
	StateReason    *IssueStateReason // null until the issue has been closed
	URL            URI               // the issue's HTML URL
	Locked         bool              // whether the conversation is locked
	Closed         bool              // whether the issue is closed
	Author         Actor             // null for a ghost author (resolved by dataloader)
	CreatedAt      DateTime          // creation instant
	UpdatedAt      DateTime          // last-update instant
	ClosedAt       *DateTime         // null while open
	Labels         *LabelConnection  // the attached labels (resolved by dataloader)
	Assignees      *UserConnection   // the assignees (resolved on demand)
	Milestone      *Milestone        // the milestone (resolved on demand)
	Comments       *IssueCommentConnection
	ReactionGroups []ReactionGroup // emoji reaction counts; always non-nil (empty when none)

	// DatabaseID is the issue's integer database id (the legacy REST id),
	// served as databaseId by the resolver.
	DatabaseID int64
	// ActiveLockReason is GitHub's raw lock-reason string, nil when unlocked or
	// locked without a reason; the resolver maps it onto the LockReason enum.
	ActiveLockReason *string
	// IsPinned reports whether the issue is pinned to the repository. Githome
	// does not model pinned issues yet, so it is always false.
	IsPinned bool

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
	ID          string   // the Label node ID
	Name        string   // the label name
	Color       string   // the six-hex color, no leading hash
	Description *string  // null when unset
	IsDefault   bool     // whether the label is one GitHub seeds new repos with
	URL         URI      // the label's HTML URL (the filtered issue list)
	CreatedAt   DateTime // creation instant
	UpdatedAt   DateTime // last rename/recolor; mirrors CreatedAt when untracked
}

// IsNode marks Label as implementing the Node interface.
func (Label) IsNode() {}

// GetID satisfies the Node interface getter gqlgen requires.
func (l Label) GetID() string { return l.ID }

// LabelConnection is the connection over an issue's or repository's labels.
type LabelConnection struct {
	Edges      []*LabelEdge
	Nodes      []*Label
	PageInfo   *PageInfo
	TotalCount int32
}

// LabelEdge is one edge of a LabelConnection, pairing a label with its cursor.
type LabelEdge struct {
	Cursor string
	Node   *Label
}

// IssueTimelineItemsEdge is one edge of an issue's timeline. Githome models the
// timeline as the comment stream, so the node is an IssueComment.
type IssueTimelineItemsEdge struct {
	Cursor string
	Node   *IssueComment
}

// CommentAuthorAssociation is the GraphQL CommentAuthorAssociation enum: the
// comment author's relationship to the repository.
type CommentAuthorAssociation string

// The CommentAuthorAssociation values.
const (
	CommentAuthorAssociationMember               CommentAuthorAssociation = "MEMBER"
	CommentAuthorAssociationOwner                CommentAuthorAssociation = "OWNER"
	CommentAuthorAssociationMannequin            CommentAuthorAssociation = "MANNEQUIN"
	CommentAuthorAssociationCollaborator         CommentAuthorAssociation = "COLLABORATOR"
	CommentAuthorAssociationContributor          CommentAuthorAssociation = "CONTRIBUTOR"
	CommentAuthorAssociationFirstTimeContributor CommentAuthorAssociation = "FIRST_TIME_CONTRIBUTOR"
	CommentAuthorAssociationFirstTimer           CommentAuthorAssociation = "FIRST_TIMER"
	CommentAuthorAssociationNone                 CommentAuthorAssociation = "NONE"
)

// IssueComment is the GraphQL IssueComment object.
type IssueComment struct {
	ID                  string                   // the IssueComment node ID
	Body                string                   // the comment body
	Author              Actor                    // null for a ghost author
	AuthorAssociation   CommentAuthorAssociation // the author's repository association
	IncludesCreatedEdit bool                     // whether the body has been edited
	IsMinimized         bool                     // always false; Githome does not minimize
	MinimizedReason     *string                  // always null
	ReactionGroups      []ReactionGroup          // emoji reaction counts; non-nil
	URL                 URI                      // the comment's HTML URL
	CreatedAt           DateTime                 // creation instant
	UpdatedAt           DateTime                 // last-update instant

	// AuthorPK is not part of the GraphQL schema. It carries the author's
	// database key so the viewerDidAuthor resolver can compare it with the
	// request's viewer.
	AuthorPK int64
}

// IssueCommentConnection is the connection over an issue's comments.
type IssueCommentConnection struct {
	Nodes      []*IssueComment
	PageInfo   *PageInfo
	TotalCount int32
}

// IsNode marks IssueComment as implementing the Node interface.
func (IssueComment) IsNode() {}

// GetID satisfies the Node interface getter gqlgen requires.
func (c IssueComment) GetID() string { return c.ID }

// IsNode marks Issue as implementing the Node interface.
func (Issue) IsNode() {}

// GetID satisfies the Node interface getter gqlgen requires.
func (i Issue) GetID() string { return i.ID }

// IsLabelableNode marks Issue as a member of the LabelableNode union type.
func (Issue) IsLabelableNode() {}

// IsAssignableNode marks Issue as a member of the AssignableNode union type.
func (Issue) IsAssignableNode() {}

// IsSearchResultItem marks Issue as a member of the SearchResultItem union.
func (Issue) IsSearchResultItem() {}

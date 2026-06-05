package domain

import "time"

// Issue is the domain view of an issue, assembled with its author and the
// related rows GitHub embeds on the issue resource: labels, assignees, the
// milestone, and the reaction rollup. ID is the public database id
// (issues.db_id); PK is the internal primary key the service carries for the
// write paths and never renders. RepoPK and RepoID locate the repository the
// presenter builds the issue's URLs from.
type Issue struct {
	PK     int64
	ID     int64
	RepoPK int64
	RepoID int64

	Number      int64
	Title       string
	Body        *string
	State       string
	StateReason *string
	Locked      bool

	User        *User
	Assignees   []*User
	Labels      []*Label
	Milestone   *Milestone
	ClosedBy    *User
	Reactions   ReactionRollup
	CommentsCount int

	ClosedAt  *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time

	// lockVersion carries the optimistic-lock token from the row the edit read,
	// so EditIssue can write under it. It is not rendered.
	lockVersion int64
}

// Label is the domain view of a repository label.
type Label struct {
	ID          int64
	Name        string
	Color       string
	Description *string
	Default     bool
}

// Milestone is the domain view of a milestone, including the open and closed
// issue counts computed on read.
type Milestone struct {
	ID           int64
	Number       int64
	Title        string
	Description  *string
	State        string
	Creator      *User
	OpenIssues   int
	ClosedIssues int
	DueOn        *time.Time
	ClosedAt     *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Comment is the domain view of an issue comment. IssueNumber is the per-repo
// number of the issue the comment belongs to, carried so the presenter can build
// the comment's issue and html URLs without a second lookup.
type Comment struct {
	ID          int64
	IssuePK     int64
	IssueNumber int64
	User        *User
	Body        string
	Reactions   ReactionRollup
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ReactionRollup is the per-content reaction count GitHub embeds on reactable
// objects. Counts is keyed by reaction content (+1, heart, ...); TotalCount is
// their sum.
type ReactionRollup struct {
	TotalCount int
	Counts     map[string]int
}

// Reaction is the domain view of a single reaction.
type Reaction struct {
	ID        int64
	User      *User
	Content   string
	CreatedAt time.Time
}

package restmodel

// The issue subsystem wire models. Field order follows github.com's response so
// a recorded body reads the same top to bottom as the upstream API, though the
// contract harness compares by key rather than by byte order.

// Issue is the object GET /repos/{owner}/{repo}/issues/{number} returns and the
// element type of the issues list. A pull request is an issue with a
// pull_request member; M4 renders issues only, so PullRequest stays nil.
type Issue struct {
	ID                int64          `json:"id"`
	NodeID            string         `json:"node_id"`
	URL               string         `json:"url"`
	RepositoryURL     string         `json:"repository_url"`
	LabelsURL         string         `json:"labels_url"`
	CommentsURL       string         `json:"comments_url"`
	EventsURL         string         `json:"events_url"`
	HTMLURL           string         `json:"html_url"`
	Number            int64          `json:"number"`
	State             string         `json:"state"`
	StateReason       *string        `json:"state_reason"`
	Title             string         `json:"title"`
	Body              *string        `json:"body"`
	User              SimpleUser     `json:"user"`
	Labels            []Label        `json:"labels"`
	Assignee          *SimpleUser    `json:"assignee"`
	Assignees         []SimpleUser   `json:"assignees"`
	Milestone         *Milestone     `json:"milestone"`
	Locked            bool           `json:"locked"`
	ActiveLockReason  *string        `json:"active_lock_reason"`
	Comments          int            `json:"comments"`
	PullRequest       *IssuePRLink   `json:"pull_request,omitempty"`
	ClosedAt          *Time          `json:"closed_at"`
	CreatedAt         Time           `json:"created_at"`
	UpdatedAt         Time           `json:"updated_at"`
	ClosedBy          *SimpleUser    `json:"closed_by"`
	AuthorAssociation string         `json:"author_association"`
	Reactions         ReactionRollup `json:"reactions"`
	TimelineURL       string         `json:"timeline_url"`
	PerformedViaApp   *string        `json:"performed_via_github_app"`
}

// IssuePRLink is the pull_request member present on issues that are pull
// requests. It is reserved for the pull request milestone.
type IssuePRLink struct {
	URL      string `json:"url"`
	HTMLURL  string `json:"html_url"`
	DiffURL  string `json:"diff_url"`
	PatchURL string `json:"patch_url"`
	MergedAt *Time  `json:"merged_at"`
}

// Label is a repository label as embedded on issues and returned by the labels
// endpoints. Color is six hex digits with no leading hash.
type Label struct {
	ID          int64   `json:"id"`
	NodeID      string  `json:"node_id"`
	URL         string  `json:"url"`
	Name        string  `json:"name"`
	Color       string  `json:"color"`
	Default     bool    `json:"default"`
	Description *string `json:"description"`
}

// Milestone is the milestone object embedded on issues and returned by the
// milestones endpoints. open_issues and closed_issues are computed counts.
type Milestone struct {
	URL          string      `json:"url"`
	HTMLURL      string      `json:"html_url"`
	LabelsURL    string      `json:"labels_url"`
	ID           int64       `json:"id"`
	NodeID       string      `json:"node_id"`
	Number       int64       `json:"number"`
	State        string      `json:"state"`
	Title        string      `json:"title"`
	Description  *string     `json:"description"`
	Creator      *SimpleUser `json:"creator"`
	OpenIssues   int         `json:"open_issues"`
	ClosedIssues int         `json:"closed_issues"`
	CreatedAt    Time        `json:"created_at"`
	UpdatedAt    Time        `json:"updated_at"`
	ClosedAt     *Time       `json:"closed_at"`
	DueOn        *Time       `json:"due_on"`
}

// IssueEvent is one entry of an issue's event log, as the events and timeline
// endpoints return it. commit_id/commit_url are present-but-null for the action
// events githome records (none of them reference a commit).
type IssueEvent struct {
	ID        int64       `json:"id"`
	NodeID    string      `json:"node_id"`
	URL       string      `json:"url"`
	Actor     *SimpleUser `json:"actor"`
	Event     string      `json:"event"`
	CommitID  *string     `json:"commit_id"`
	CommitURL *string     `json:"commit_url"`
	CreatedAt Time        `json:"created_at"`
}

// IssueComment is the object the comment endpoints return.
type IssueComment struct {
	ID                int64          `json:"id"`
	NodeID            string         `json:"node_id"`
	URL               string         `json:"url"`
	HTMLURL           string         `json:"html_url"`
	Body              string         `json:"body"`
	User              SimpleUser     `json:"user"`
	CreatedAt         Time           `json:"created_at"`
	UpdatedAt         Time           `json:"updated_at"`
	IssueURL          string         `json:"issue_url"`
	AuthorAssociation string         `json:"author_association"`
	Reactions         ReactionRollup `json:"reactions"`
	PerformedViaApp   *string        `json:"performed_via_github_app"`
}

// ReactionRollup is the per-content reaction summary embedded on reactable
// objects. The +1/-1 keys carry their literal names; the JSON omits none.
type ReactionRollup struct {
	URL        string `json:"url"`
	TotalCount int    `json:"total_count"`
	PlusOne    int    `json:"+1"`
	MinusOne   int    `json:"-1"`
	Laugh      int    `json:"laugh"`
	Hooray     int    `json:"hooray"`
	Confused   int    `json:"confused"`
	Heart      int    `json:"heart"`
	Rocket     int    `json:"rocket"`
	Eyes       int    `json:"eyes"`
}

// Reaction is a single reaction as returned by the reactions list and create
// endpoints.
type Reaction struct {
	ID        int64      `json:"id"`
	NodeID    string     `json:"node_id"`
	User      SimpleUser `json:"user"`
	Content   string     `json:"content"`
	CreatedAt Time       `json:"created_at"`
}

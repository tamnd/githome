package restmodel

// The pull request subsystem wire models. Field order follows github.com's
// response so a recorded body reads the same top to bottom as the upstream API,
// though the contract harness compares by key rather than by byte order.

// PullRequest is the object GET /repos/{owner}/{repo}/pulls/{number} returns and
// the element type of the pulls list. A pull request shares its id space, number,
// title, body, and state with the issue that backs it; the merge fields below are
// null until the mergeability worker computes them, the null-then-value contract
// a poll resolves. The list endpoint omits the diff stats and the mergeable
// triplet, which only the single-pull view fills.
type PullRequest struct {
	URL                 string           `json:"url"`
	ID                  int64            `json:"id"`
	NodeID              string           `json:"node_id"`
	HTMLURL             string           `json:"html_url"`
	DiffURL             string           `json:"diff_url"`
	PatchURL            string           `json:"patch_url"`
	IssueURL            string           `json:"issue_url"`
	CommitsURL          string           `json:"commits_url"`
	ReviewCommentsURL   string           `json:"review_comments_url"`
	ReviewCommentURL    string           `json:"review_comment_url"`
	CommentsURL         string           `json:"comments_url"`
	StatusesURL         string           `json:"statuses_url"`
	Number              int64            `json:"number"`
	State               string           `json:"state"`
	Locked              bool             `json:"locked"`
	Title               string           `json:"title"`
	User                SimpleUser       `json:"user"`
	Body                *string          `json:"body"`
	Labels              []Label          `json:"labels"`
	Milestone           *Milestone       `json:"milestone"`
	ActiveLockReason    *string          `json:"active_lock_reason"`
	CreatedAt           Time             `json:"created_at"`
	UpdatedAt           Time             `json:"updated_at"`
	ClosedAt            *Time            `json:"closed_at"`
	MergedAt            *Time            `json:"merged_at"`
	MergeCommitSHA      *string          `json:"merge_commit_sha"`
	Assignee            *SimpleUser      `json:"assignee"`
	Assignees           []SimpleUser     `json:"assignees"`
	RequestedReviewers  []SimpleUser     `json:"requested_reviewers"`
	RequestedTeams      []any            `json:"requested_teams"`
	Head                PullRequestRef   `json:"head"`
	Base                PullRequestRef   `json:"base"`
	Links               PullRequestLinks `json:"_links"`
	AuthorAssociation   string           `json:"author_association"`
	AutoMerge           *any             `json:"auto_merge"`
	Draft               bool             `json:"draft"`
	MaintainerCanModify bool             `json:"maintainer_can_modify"`

	// The merge view fields. The list endpoint leaves them out; the single-pull
	// view fills them, with the mergeable triplet null until the worker runs.
	Merged         *bool       `json:"merged,omitempty"`
	Mergeable      *bool       `json:"mergeable,omitempty"`
	Rebaseable     *bool       `json:"rebaseable,omitempty"`
	MergeableState string      `json:"mergeable_state,omitempty"`
	MergedBy       *SimpleUser `json:"merged_by,omitempty"`
	Comments       *int        `json:"comments,omitempty"`
	ReviewComments *int        `json:"review_comments,omitempty"`
	Commits        *int        `json:"commits,omitempty"`
	Additions      *int        `json:"additions,omitempty"`
	Deletions      *int        `json:"deletions,omitempty"`
	ChangedFiles   *int        `json:"changed_files,omitempty"`
}

// PullRequestRef is one side of a pull request, a base or a head. Label is the
// "owner:branch" form; Ref is the short branch name; SHA is the recorded tip.
// Repo is the repository the ref lives in, present for a same-repository pull
// request and null only for a head whose fork was deleted.
type PullRequestRef struct {
	Label string      `json:"label"`
	Ref   string      `json:"ref"`
	SHA   string      `json:"sha"`
	User  *SimpleUser `json:"user"`
	Repo  *Repository `json:"repo"`
}

// PullRequestLinks is the _links block of a pull request, the hypermedia pointers
// to its related collections.
type PullRequestLinks struct {
	Self           Link `json:"self"`
	HTML           Link `json:"html"`
	Issue          Link `json:"issue"`
	Comments       Link `json:"comments"`
	ReviewComments Link `json:"review_comments"`
	ReviewComment  Link `json:"review_comment"`
	Commits        Link `json:"commits"`
	Statuses       Link `json:"statuses"`
}

// Link is one href member of a _links block.
type Link struct {
	HRef string `json:"href"`
}

// PullRequestFile is one element of GET /pulls/{number}/files: a file's diff over
// the pull request range. Changes is additions plus deletions. PreviousFilename
// is present only for a rename or copy. Patch is the unified hunk text, omitted
// for a binary file.
type PullRequestFile struct {
	SHA              string  `json:"sha"`
	Filename         string  `json:"filename"`
	Status           string  `json:"status"`
	Additions        int     `json:"additions"`
	Deletions        int     `json:"deletions"`
	Changes          int     `json:"changes"`
	BlobURL          string  `json:"blob_url"`
	RawURL           string  `json:"raw_url"`
	ContentsURL      string  `json:"contents_url"`
	Patch            string  `json:"patch,omitempty"`
	PreviousFilename *string `json:"previous_filename,omitempty"`
}

// PullRequestMergeResult is the body of a successful PUT
// /pulls/{number}/merge: the merge commit sha, the merged flag, and the message.
type PullRequestMergeResult struct {
	SHA     string `json:"sha"`
	Merged  bool   `json:"merged"`
	Message string `json:"message"`
}

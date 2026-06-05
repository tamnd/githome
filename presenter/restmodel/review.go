package restmodel

// The code review wire models. Field order follows github.com's response so a
// recorded body reads top to bottom like the upstream API, though the contract
// harness compares by key.

// Review is one element of GET /repos/{owner}/{repo}/pulls/{number}/reviews and
// the body of a single review. State is APPROVED, CHANGES_REQUESTED, COMMENTED,
// DISMISSED, or PENDING (a pending draft is visible only to its author).
// SubmittedAt is null while a review is still a draft.
type Review struct {
	ID                int64       `json:"id"`
	NodeID            string      `json:"node_id"`
	User              SimpleUser  `json:"user"`
	Body              string      `json:"body"`
	State             string      `json:"state"`
	HTMLURL           string      `json:"html_url"`
	PullRequestURL    string      `json:"pull_request_url"`
	Links             ReviewLinks `json:"_links"`
	SubmittedAt       *Time       `json:"submitted_at"`
	CommitID          string      `json:"commit_id"`
	AuthorAssociation string      `json:"author_association"`
}

// ReviewLinks is the _links block of a review: its html page and its pull request.
type ReviewLinks struct {
	HTML        Link `json:"html"`
	PullRequest Link `json:"pull_request"`
}

// ReviewComment is one element of the pull request comments collection and the
// body of a single review comment. The line/side anchor and the legacy position
// are both filled. Position and Line are null when the comment is outdated, the
// anchor no longer present in the diff. InReplyToID is set on a reply.
type ReviewComment struct {
	URL                 string             `json:"url"`
	PullRequestReviewID int64              `json:"pull_request_review_id"`
	ID                  int64              `json:"id"`
	NodeID              string             `json:"node_id"`
	DiffHunk            string             `json:"diff_hunk"`
	Path                string             `json:"path"`
	Position            *int64             `json:"position"`
	OriginalPosition    *int64             `json:"original_position"`
	CommitID            string             `json:"commit_id"`
	OriginalCommitID    string             `json:"original_commit_id"`
	InReplyToID         *int64             `json:"in_reply_to_id,omitempty"`
	User                SimpleUser         `json:"user"`
	Body                string             `json:"body"`
	CreatedAt           Time               `json:"created_at"`
	UpdatedAt           Time               `json:"updated_at"`
	HTMLURL             string             `json:"html_url"`
	PullRequestURL      string             `json:"pull_request_url"`
	AuthorAssociation   string             `json:"author_association"`
	Links               ReviewCommentLinks `json:"_links"`
	StartLine           *int64             `json:"start_line"`
	OriginalStartLine   *int64             `json:"original_start_line"`
	StartSide           *string            `json:"start_side"`
	Line                *int64             `json:"line"`
	OriginalLine        *int64             `json:"original_line"`
	Side                string             `json:"side"`
	SubjectType         string             `json:"subject_type"`
}

// ReviewCommentLinks is the _links block of a review comment.
type ReviewCommentLinks struct {
	Self        Link `json:"self"`
	HTML        Link `json:"html"`
	PullRequest Link `json:"pull_request"`
}

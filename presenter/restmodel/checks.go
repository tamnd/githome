package restmodel

// The commit status and check wire models. A commit status is the older, flat
// signal; a check run grouped under a check suite is the richer one. Their
// combination is the combined status (statuses only) and the check-runs list.

// Status is one element of GET /repos/{owner}/{repo}/commits/{ref}/statuses: a
// single external report against a sha under a context.
type Status struct {
	URL         string      `json:"url"`
	AvatarURL   *string     `json:"avatar_url"`
	ID          int64       `json:"id"`
	NodeID      string      `json:"node_id"`
	State       string      `json:"state"`
	Description *string     `json:"description"`
	TargetURL   *string     `json:"target_url"`
	Context     string      `json:"context"`
	CreatedAt   Time        `json:"created_at"`
	UpdatedAt   Time        `json:"updated_at"`
	Creator     *SimpleUser `json:"creator"`
}

// CombinedStatus is the body of GET /repos/{owner}/{repo}/commits/{ref}/status:
// the folded state across the latest status per context, with the contributing
// statuses and a minimal repository.
type CombinedStatus struct {
	State      string      `json:"state"`
	Statuses   []Status    `json:"statuses"`
	SHA        string      `json:"sha"`
	TotalCount int         `json:"total_count"`
	Repository MinimalRepo `json:"repository"`
	CommitURL  string      `json:"commit_url"`
	URL        string      `json:"url"`
}

// MinimalRepo is the trimmed repository the combined status embeds: enough to
// identify it without the full repository object.
type MinimalRepo struct {
	ID       int64      `json:"id"`
	NodeID   string     `json:"node_id"`
	Name     string     `json:"name"`
	FullName string     `json:"full_name"`
	Owner    SimpleUser `json:"owner"`
	Private  bool       `json:"private"`
	HTMLURL  string     `json:"html_url"`
	URL      string     `json:"url"`
}

// CheckRunList is the body of GET /repos/{owner}/{repo}/commits/{ref}/check-runs.
type CheckRunList struct {
	TotalCount int        `json:"total_count"`
	CheckRuns  []CheckRun `json:"check_runs"`
}

// CheckRun is the body of a single check run and an element of the list. Status is
// queued, in_progress, or completed; Conclusion is set once completed.
type CheckRun struct {
	ID           int64            `json:"id"`
	NodeID       string           `json:"node_id"`
	HeadSHA      string           `json:"head_sha"`
	ExternalID   string           `json:"external_id"`
	URL          string           `json:"url"`
	HTMLURL      string           `json:"html_url"`
	DetailsURL   string           `json:"details_url"`
	Status       string           `json:"status"`
	Conclusion   *string          `json:"conclusion"`
	StartedAt    *Time            `json:"started_at"`
	CompletedAt  *Time            `json:"completed_at"`
	Output       CheckRunOutput   `json:"output"`
	Name         string           `json:"name"`
	CheckSuite   CheckSuiteRef    `json:"check_suite"`
	App          *any             `json:"app"`
	PullRequests []any            `json:"pull_requests"`
	Actions      []CheckRunAction `json:"actions,omitempty"`
}

// CheckRunAction is one requested action button on a check run, echoed back as
// the reporter wrote it.
type CheckRunAction struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Identifier  string `json:"identifier"`
}

// CheckRunAnnotation is one element of GET
// /repos/{owner}/{repo}/check-runs/{check_run_id}/annotations.
type CheckRunAnnotation struct {
	Path            string  `json:"path"`
	StartLine       int64   `json:"start_line"`
	EndLine         int64   `json:"end_line"`
	StartColumn     *int64  `json:"start_column"`
	EndColumn       *int64  `json:"end_column"`
	AnnotationLevel string  `json:"annotation_level"`
	Title           *string `json:"title"`
	Message         string  `json:"message"`
	RawDetails      *string `json:"raw_details"`
	BlobHRef        string  `json:"blob_href"`
}

// CheckRunOutput is the output block of a check run.
type CheckRunOutput struct {
	Title            *string `json:"title"`
	Summary          *string `json:"summary"`
	Text             *string `json:"text"`
	AnnotationsCount int     `json:"annotations_count"`
	AnnotationsURL   string  `json:"annotations_url"`
}

// CheckSuiteRef is the trimmed suite a check run names: its id only.
type CheckSuiteRef struct {
	ID int64 `json:"id"`
}

// CheckSuiteList is the body of GET
// /repos/{owner}/{repo}/commits/{ref}/check-suites.
type CheckSuiteList struct {
	TotalCount  int          `json:"total_count"`
	CheckSuites []CheckSuite `json:"check_suites"`
}

// CheckSuite is the per-app container the check runs against a head sha roll up
// into. Status is queued, in_progress, or completed; Conclusion is its verdict.
type CheckSuite struct {
	ID                   int64   `json:"id"`
	NodeID               string  `json:"node_id"`
	HeadSHA              string  `json:"head_sha"`
	Status               string  `json:"status"`
	Conclusion           *string `json:"conclusion"`
	URL                  string  `json:"url"`
	Before               *string `json:"before"`
	After                *string `json:"after"`
	App                  *any    `json:"app"`
	CreatedAt            Time    `json:"created_at"`
	UpdatedAt            Time    `json:"updated_at"`
	LatestCheckRunsCount int     `json:"latest_check_runs_count"`
}

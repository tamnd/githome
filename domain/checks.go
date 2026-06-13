package domain

import "time"

// The domain views of the two commit-level signals: commit statuses (the older,
// flat model) and check runs grouped into check suites (the richer model). Their
// combination is the status check rollup, and the combined status is the same
// idea in the statuses-only vocabulary.

// Rollup and combined-status states, worst first. A rollup takes the worst state
// present across every status and check run; the combined status uses the
// statuses-only subset (error folds into failure there).
const (
	RollupError    = "ERROR"
	RollupFailure  = "FAILURE"
	RollupPending  = "PENDING"
	RollupSuccess  = "SUCCESS"
	RollupExpected = "EXPECTED"
)

// CommitStatus is one external report against a sha under a context.
type CommitStatus struct {
	PK          int64
	ID          int64
	RepoPK      int64
	SHA         string
	State       string // error | failure | pending | success
	Context     string
	TargetURL   *string
	Description *string
	Creator     *User
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CombinedStatus folds the latest status per context into one state, the body of
// the combined-status endpoint. TotalCount is the number of contributing
// statuses; Statuses is the latest one per context.
type CombinedStatus struct {
	State      string // failure | pending | success
	SHA        string
	TotalCount int
	Statuses   []*CommitStatus
	Repo       *Repo
}

// CheckRun is one named check against a head sha inside a suite. Status is queued,
// in_progress, or completed; Conclusion is set once completed.
type CheckRun struct {
	PK      int64
	ID      int64
	SuitePK int64
	// SuiteID is the public id of the run's suite, the value the response's
	// check_suite reference carries so it round-trips through
	// GET /check-suites/{id}.
	SuiteID          int64
	RepoPK           int64
	HeadSHA          string
	Name             string
	Status           string
	Conclusion       *string
	DetailsURL       *string
	ExternalID       *string
	OutputTitle      *string
	OutputSummary    *string
	OutputText       *string
	StartedAt        *time.Time
	CompletedAt      *time.Time
	Actions          []CheckRunAction
	AnnotationsCount int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// CheckRunAction is one requested action button a check run offers, echoed back
// exactly as the reporter wrote it.
type CheckRunAction struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Identifier  string `json:"identifier"`
}

// CheckRunAnnotation is one line-anchored note a check run attaches to a file.
// Annotations accumulate across check run updates.
type CheckRunAnnotation struct {
	PK              int64
	CheckRunPK      int64
	Path            string
	StartLine       int64
	EndLine         int64
	StartColumn     *int64
	EndColumn       *int64
	AnnotationLevel string
	Message         string
	Title           *string
	RawDetails      *string
}

// CheckSuite is the per-app container for the check runs reported against a head
// sha.
type CheckSuite struct {
	PK         int64
	ID         int64
	RepoPK     int64
	HeadSHA    string
	AppSlug    string
	Status     string
	Conclusion *string
	Runs       []*CheckRun
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// StatusCheckRollup is the combined verdict across every status and check run on
// a head sha, the value the pull request and its head commit surface.
type StatusCheckRollup struct {
	State      string // one of the Rollup* constants
	SHA        string
	Statuses   []*CommitStatus
	CheckRuns  []*CheckRun
	TotalCount int
}

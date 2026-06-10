package store

import "time"

// The row structs here are the store's exported read/write shapes for the
// credential and user tables M1 introduces. They are plain data: the query
// methods in queries_*.go scan into them and the auth and domain layers read
// them. Nullable columns are pointers so a SQL NULL is distinguishable from a
// zero value, which the User wire model depends on.

// UserRow is a row of the users table, including the profile columns 0002 adds.
type UserRow struct {
	PK              int64
	DBID            int64
	Login           string
	Type            string
	Name            *string
	Email           *string
	SiteAdmin       bool
	Company         *string
	Blog            string
	Location        *string
	Bio             *string
	Hireable        *bool
	TwitterUsername *string
	PublicRepos     int
	PublicGists     int
	Followers       int
	Following       int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// TokenRow is a row of the tokens table. UserPK and OAuthAppPK are nullable
// because installation and app credentials (later milestones) have no user, and
// PATs have no granting OAuth app.
type TokenRow struct {
	PK             int64
	UserPK         *int64
	OAuthAppPK     *int64
	InstallationPK *int64
	GitHubAppPK    *int64
	GrantJSON      *string
	TokenHash      []byte
	TokenPrefix    string
	LastEight      string
	Kind           string // pat | oauth | installation
	Scopes         string // comma-space header form, the X-OAuth-Scopes value
	Note           string
	ExpiresAt      *time.Time
	RevokedAt      *time.Time
	LastUsedAt     *time.Time
	CreatedAt      time.Time
}

// GitHubAppRow is a row of the github_apps table.
type GitHubAppRow struct {
	PK            int64
	DBID          int64
	OwnerPK       int64
	Slug          string
	Name          string
	ClientID      string
	PrivateKeyPEM []byte
	Permissions   string // JSON object
	Events        string // JSON array
	CreatedAt     time.Time
}

// InstallationRow is a row of the installations table.
type InstallationRow struct {
	PK                  int64
	DBID                int64
	AppPK               int64
	AccountPK           int64
	RepositorySelection string
	Permissions         string // JSON object
	Events              string // JSON array
	SuspendedAt         *time.Time
	CreatedAt           time.Time
}

// RepoRow is a row of the repositories table, including the settings columns
// 0003 adds. OwnerPK is the internal pk of the owning user; the public owner
// object is resolved separately. Description and Homepage are nullable; the
// boolean flags carry GitHub's per-repository feature and state settings.
type RepoRow struct {
	PK              int64
	DBID            int64
	OwnerPK         int64
	Name            string
	Description     *string
	Homepage        *string
	Private         bool
	Fork            bool
	DefaultBranch   string
	HasIssues       bool
	HasProjects     bool
	HasWiki         bool
	HasDownloads    bool
	Archived        bool
	Disabled        bool
	IsTemplate      bool
	OpenIssuesCount int
	PushedAt        *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Topics          string // JSON array, e.g. '["go","api"]'
}

// PullRow is a row of the pull_requests table, the extension a pull request
// carries on top of its issue row. IssuePK ties it to the issues row that holds
// the title, body, state, and per-repo number; the fields here are the git
// coordinates and the merge state. Mergeable and Rebaseable are pointers because
// they are NULL until the recompute_mergeability worker computes them, the
// null-then-value contract the API surfaces. HeadRepoPK, MergedAt, MergedByPK,
// MergeCommitSHA, and MergeabilityCheckedAt are nullable for the same reason a
// pull request acquires them only over its lifetime.
type PullRow struct {
	PK                    int64
	DBID                  int64
	IssuePK               int64
	RepoPK                int64
	BaseRef               string
	BaseSHA               string
	HeadRef               string
	HeadSHA               string
	HeadRepoPK            *int64
	Draft                 bool
	MaintainerCanModify   bool
	Merged                bool
	MergedAt              *time.Time
	MergedByPK            *int64
	MergeCommitSHA        *string
	Mergeable             *bool
	MergeableState        string
	Rebaseable            *bool
	Additions             int
	Deletions             int
	ChangedFiles          int
	CommitsCount          int
	MergeabilityCheckedAt *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// ReviewRow is a row of pull_request_reviews, one act of reviewing a pull
// request. State is PENDING (a draft with no submitted_at yet), APPROVED,
// CHANGES_REQUESTED, COMMENTED, or DISMISSED. SubmittedAt is nil while the review
// is still a pending draft. DismissedMessage carries the reason a review was
// later dismissed.
type ReviewRow struct {
	PK               int64
	DBID             int64
	PullPK           int64
	RepoPK           int64
	UserPK           int64
	State            string
	Body             string
	CommitID         string
	DismissedMessage *string
	SubmittedAt      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ReviewCommentRow is a row of pull_request_review_comments, a comment anchored
// to a diff line. Line/StartLine are file line numbers in the new model; Position
// is the legacy 1-based diff offset, kept for the older API shape. Side and
// StartSide are LEFT (base) or RIGHT (head). InReplyToPK threads a reply under
// the comment that started a conversation; Resolved marks that thread settled.
type ReviewCommentRow struct {
	PK                int64
	DBID              int64
	ReviewPK          int64
	PullPK            int64
	RepoPK            int64
	UserPK            int64
	Path              string
	Side              string
	Line              *int64
	StartLine         *int64
	StartSide         *string
	OriginalLine      *int64
	OriginalStartLine *int64
	Position          *int64
	OriginalPosition  *int64
	CommitID          string
	OriginalCommitID  string
	InReplyToPK       *int64
	DiffHunk          string
	SubjectType       string
	Body              string
	Resolved          bool
	ResolvedByPK      *int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// CommitStatusRow is a row of commit_statuses, one external pass/fail report
// against a sha under a context. State is error, failure, pending, or success.
type CommitStatusRow struct {
	PK          int64
	DBID        int64
	RepoPK      int64
	SHA         string
	State       string
	Context     string
	TargetURL   *string
	Description *string
	CreatorPK   *int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CheckSuiteRow is a row of check_suites, the per-app container for the check
// runs reported against a head sha. Status is queued, in_progress, or completed;
// Conclusion is set only once completed.
type CheckSuiteRow struct {
	PK         int64
	DBID       int64
	RepoPK     int64
	HeadSHA    string
	AppSlug    string
	Status     string
	Conclusion *string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// CheckRunRow is a row of check_runs, one named check against a head sha inside a
// suite. Status is queued, in_progress, or completed; Conclusion (success,
// failure, neutral, cancelled, timed_out, action_required, skipped) is set when
// the run completes.
type CheckRunRow struct {
	PK            int64
	DBID          int64
	SuitePK       int64
	RepoPK        int64
	HeadSHA       string
	Name          string
	Status        string
	Conclusion    *string
	DetailsURL    *string
	ExternalID    *string
	OutputTitle   *string
	OutputSummary *string
	OutputText    *string
	StartedAt     *time.Time
	CompletedAt   *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// PullCheckStateRow is the denormalized snapshot the recompute worker writes: a
// pull request's derived review decision and status check rollup state, so list
// views and webhook payloads read one row instead of re-aggregating.
type PullCheckStateRow struct {
	PullPK         int64
	ReviewDecision *string
	RollupState    string
	UpdatedAt      time.Time
}

// OAuthAppRow is a row of the oauth_apps table.
type OAuthAppRow struct {
	PK                int64
	ClientID          string
	ClientSecretHash  []byte
	Name              string
	OwnerPK           *int64
	DeviceFlowEnabled bool
	CallbackURL       string // registered authorization callback; "" means none registered
	CreatedAt         time.Time
}

// DeviceCodeRow is a row of the oauth_device_codes table backing the device
// flow state machine.
type DeviceCodeRow struct {
	PK             int64
	DeviceCodeHash []byte
	UserCode       string
	OAuthAppPK     *int64
	Scopes         string
	State          string // pending | approved | denied
	UserPK         *int64
	IntervalSec    int
	LastPolledAt   *time.Time
	ExpiresAt      time.Time
	CreatedAt      time.Time
}

// AuthCodeRow is a row of the oauth_auth_codes table. It backs the OAuth
// authorization-code grant (RFC 6749 §4.1). Each code is single-use and
// expires in 10 minutes. CodeHash is SHA-256(raw code).
type AuthCodeRow struct {
	PK          int64
	CodeHash    []byte
	OAuthAppPK  int64
	UserPK      int64
	RedirectURI string
	Scopes      string
	Used        bool
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

// EventRow is a row of the events table: one append-only record of an action a
// user took on a repository. It feeds both the pull-based Events API and the
// push-based webhook fan-out. IssuePK is nullable because push and repository
// events have no issue. Public is the repo-visibility-derived flag the public
// Events API filters on; Payload is the rendered Events-API JSON document.
type EventRow struct {
	PK        int64
	DBID      int64
	Event     string
	Action    string
	ActorPK   int64
	RepoPK    int64
	IssuePK   *int64
	Payload   string
	Public    bool
	CreatedAt time.Time
}

// WebhookRow is a row of the webhooks table: a repository's registration of a
// URL to POST events to. Secret is nullable and held in the clear because HMAC
// signing needs the original bytes; the API always redacts it. Events is the
// JSON array of subscribed event names ("*" means all). LastResponse is the
// JSON summary of the most recent delivery, nil until the first POST.
type WebhookRow struct {
	PK           int64
	DBID         int64
	RepoPK       int64
	Name         string
	URL          string
	ContentType  string
	Secret       *string
	InsecureSSL  bool
	Active       bool
	Events       string
	LastResponse *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// WebhookDeliveryRow is a row of the webhook_deliveries table: the recorded
// result of one POST to a webhook. StatusCode is nullable because a transport
// failure (connection refused, timeout) produces no HTTP status. Redelivery
// marks a delivery that replayed an earlier one; Success is the 2xx outcome.
type WebhookDeliveryRow struct {
	PK              int64
	DBID            int64
	WebhookPK       int64
	GUID            string
	Event           string
	Action          string
	StatusCode      *int64
	RequestURL      string
	RequestHeaders  string
	RequestBody     string
	ResponseHeaders string
	ResponseBody    string
	DurationMS      int64
	Redelivery      bool
	Success         bool
	CreatedAt       time.Time
}

// SSHKeyRow is a row of the ssh_keys table. RepoPK is non-nil for deploy keys.
type SSHKeyRow struct {
	PK          int64
	DBID        int64
	UserPK      int64
	Title       *string
	KeyType     string
	PublicKey   string
	Fingerprint string
	ReadOnly    bool
	RepoPK      *int64
	LastUsedAt  *time.Time
	CreatedAt   time.Time
}

// BranchProtectionRow is a row of the branch_protections table.
type BranchProtectionRow struct {
	PK                      int64
	RepoPK                  int64
	BranchPattern           string
	RequirePRReviews        bool
	RequiredApprovingCount  int
	DismissStaleReviews     bool
	RequireCodeOwnerReviews bool
	RequireStatusChecks     bool
	RequireBranchesUpToDate bool
	StatusCheckContexts     string // JSON array
	EnforceAdmins           bool
	RestrictionsUsers       string // JSON array
	RestrictionsTeams       string // JSON array
	RestrictionsEnabled     bool   // a restrictions object was supplied at all
	AllowForcePushes        bool
	AllowDeletions          bool
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

// TeamRow is a row of the teams table.
type TeamRow struct {
	PK          int64
	DBID        int64
	OrgPK       int64
	Name        string
	Slug        string
	Description *string
	Privacy     string
	Permission  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CollaboratorRow is a row of the collaborators table.
type CollaboratorRow struct {
	PK         int64
	RepoPK     int64
	UserPK     int64
	Permission string
}

// GistRow is a row of the gists table, optionally including the file rows.
type GistRow struct {
	PK          int64
	GistID      string
	OwnerPK     int64
	Description string
	Public      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Files       []GistFileRow
}

// GistFileRow is a row of the gist_files table.
type GistFileRow struct {
	PK       int64
	GistPK   int64
	Filename string
	Content  string
}

// GistCommentRow is a row of the gist_comments table.
type GistCommentRow struct {
	PK        int64
	GistPK    int64
	UserPK    int64
	Body      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

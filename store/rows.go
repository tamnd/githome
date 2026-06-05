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
	PK          int64
	UserPK      *int64
	OAuthAppPK  *int64
	TokenHash   []byte
	TokenPrefix string
	LastEight   string
	Kind        string // pat | oauth
	Scopes      string // comma-space header form, the X-OAuth-Scopes value
	Note        string
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
	LastUsedAt  *time.Time
	CreatedAt   time.Time
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

// OAuthAppRow is a row of the oauth_apps table.
type OAuthAppRow struct {
	PK                int64
	ClientID          string
	ClientSecretHash  []byte
	Name              string
	OwnerPK           *int64
	DeviceFlowEnabled bool
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

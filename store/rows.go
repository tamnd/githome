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

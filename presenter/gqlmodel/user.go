package gqlmodel

// User is the GraphQL User object, carrying the fields gh api and gh auth
// status select. It grows toward the full GitHub User type milestone by milestone.
type User struct {
	ID              string   // the User node ID
	Login           string   // the user's login
	Name            *string  // display name, null when unset
	Email           *string  // public email, null when unset
	Bio             *string  // profile bio, null when unset
	Company         *string  // profile company, null when unset
	Location        *string  // profile location, null when unset
	WebsiteURL      *URI     // profile blog/website URL, null when unset
	TwitterUsername *string  // Twitter/X handle without the @, null when unset
	DatabaseID      *int32   // the integer database id (REST id)
	URL             URI      // the user's profile HTML URL
	AvatarURL       URI      // the user's avatar URL; the size arg appends ?s=
	ResourcePath    URI      // the path part of the profile URL, e.g. /octocat
	Status          *UserStatus // the user's set status; always null today
	CreatedAt       DateTime // account creation instant
	UpdatedAt       DateTime // last-update instant
}

// IsNode marks User as implementing the Node interface.
func (User) IsNode() {}

// GetID satisfies the Node interface getter gqlgen requires.
func (u User) GetID() string { return u.ID }

// UserStatus is a user's set status. Githome does not model statuses, so the
// type is never instantiated; it exists for schema validation.
type UserStatus struct {
	ID                           string
	Emoji                        *string
	Message                      *string
	IndicatesLimitedAvailability bool
	CreatedAt                    DateTime
	UpdatedAt                    DateTime
	ExpiresAt                    *DateTime
}

// IsNode marks UserStatus as implementing the Node interface.
func (UserStatus) IsNode() {}

// GetID satisfies the Node interface getter gqlgen requires.
func (s UserStatus) GetID() string { return s.ID }

// Organization is the GraphQL Organization object. Githome does not model
// organizations yet, so the type is never instantiated; it exists so gh's
// `... on Organization` inline fragments validate.
type Organization struct {
	ID              string
	Login           string
	Name            *string
	Description     *string
	Email           *string
	Location        *string
	WebsiteURL      *URI
	TwitterUsername *string
	DatabaseID      *int32
	URL             URI
	AvatarURL       URI
	ResourcePath    URI
	CreatedAt       DateTime
	UpdatedAt       DateTime
}

// IsNode marks Organization as implementing the Node interface.
func (Organization) IsNode() {}

// GetID satisfies the Node interface getter gqlgen requires.
func (o Organization) GetID() string { return o.ID }

// IsActor marks Organization as implementing the Actor interface.
func (Organization) IsActor() {}

// IsRepositoryOwner marks Organization as implementing the RepositoryOwner interface.
func (Organization) IsRepositoryOwner() {}

// IsSearchResultItem marks User as a member of the SearchResultItem union.
func (User) IsSearchResultItem() {}

// IsActor marks User as implementing the Actor interface.
func (User) IsActor() {}

// IsRepositoryOwner marks User as implementing the RepositoryOwner interface.
func (User) IsRepositoryOwner() {}

// IsRequestedReviewer marks User as a member of the RequestedReviewer union.
func (User) IsRequestedReviewer() {}

// UserConnection is the Relay connection over a set of users (assignees, etc.).
type UserConnection struct {
	Nodes      []*User
	TotalCount int32
}

// UserEdge is one edge of a user connection, pairing a user with its cursor.
type UserEdge struct {
	Cursor string
	Node   *User
}

// Milestone is the GraphQL Milestone object: the milestone an issue or pull
// request is attached to. The fields are the subset gh issue view selects.
type Milestone struct {
	ID          string    // the Milestone node ID
	Number      int32     // the per-repository milestone number
	Title       string    // the milestone title
	Description *string   // null when unset
	DueOn       *DateTime // the due date, null when unset
	State       string    // "open" or "closed"
	URL         URI       // the milestone's HTML URL
}

// IsNode marks Milestone as implementing the Node interface.
func (Milestone) IsNode() {}

// GetID satisfies the Node interface getter gqlgen requires.
func (m Milestone) GetID() string { return m.ID }

// RepositoryOwner is the GraphQL RepositoryOwner interface: the owner of a
// repository, and the return type of the repositoryOwner(login) root query.
// User is the only implementer today.
type RepositoryOwner interface {
	IsRepositoryOwner()
}

// Language is a programming language detected in a repository.
type Language struct {
	Name string
}

// License is an open-source license detected in a repository.
type License struct {
	Name   string
	SpdxID *string
}

// RateLimit is the rate-limit state for the viewer.
type RateLimit struct {
	Limit     int32
	Cost      int32
	Remaining int32
	ResetAt   DateTime
	NodeCount int32
	Used      int32
}

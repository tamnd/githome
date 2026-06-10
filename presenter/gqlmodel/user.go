package gqlmodel

// User is the GraphQL User object, carrying the fields gh api and gh auth
// status select. It grows toward the full GitHub User type milestone by milestone.
type User struct {
	ID        string    // the User node ID
	Login     string    // the user's login
	Name      *string   // display name, null when unset
	Email     *string   // public email, null when unset
	Bio       *string   // profile bio, null when unset
	URL       URI       // the user's profile HTML URL
	AvatarURL URI       // the user's avatar URL
	CreatedAt DateTime  // account creation instant
	UpdatedAt DateTime  // last-update instant
}

// UserConnection is the Relay connection over a set of users (assignees, etc.).
type UserConnection struct {
	Nodes      []*User
	TotalCount int32
}

// Milestone is the GraphQL Milestone object: the milestone an issue or pull
// request is attached to. The fields are the subset gh issue view selects.
type Milestone struct {
	ID     string // the Milestone node ID
	Number int32  // the per-repository milestone number
	Title  string // the milestone title
	State  string // "open" or "closed"
	URL    URI    // the milestone's HTML URL
}

// RepositoryOwner is the owner of a repository, rendered as the minimal shape
// the repository.owner field carries. It carries only the fields that appear in
// the gh repo view response.
type RepositoryOwner struct {
	Login     string // the owner's login
	URL       URI    // the owner's HTML URL
	AvatarURL URI    // the owner's avatar URL
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

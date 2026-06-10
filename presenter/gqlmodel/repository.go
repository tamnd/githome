package gqlmodel

// Repository is the GraphQL Repository object, reduced to the fields the M2
// surface serves: the set gh repo view selects. Later milestones grow it toward
// the full GitHub Repository type. Nullable GraphQL fields use Go pointers so
// gqlgen renders null rather than a zero value.
type Repository struct {
	ID               string    // the Repository node ID
	Name             string    // the short repository name
	NameWithOwner    string    // owner login + "/" + name
	Description      *string   // null when unset
	IsPrivate        bool      // visibility
	CreatedAt        DateTime  // creation instant
	PushedAt         *DateTime // last push, null for a repository with no commits
	URL              URI       // the repository's HTML URL
	DefaultBranchRef *Ref      // the head branch, null for an empty repository
}

// Ref is a git reference reduced to its name and the object it points at.
type Ref struct {
	ID     string     // the Ref node ID
	Name   string     // the short ref name, such as main
	Target *GitObject // the object the ref names
}

// GitObject is a git object addressed by its SHA. The M2 schema models it as a
// concrete type carrying only oid, which is all gh repo view selects; the full
// GitObject interface with Commit, Tree, Blob, and Tag arrives with the GraphQL
// parity milestone.
type GitObject struct {
	Oid GitObjectID
}

package gqlmodel

// RepositoryPermission is the viewer's effective permission level on a repository.
type RepositoryPermission string

// The RepositoryPermission values.
const (
	RepositoryPermissionAdmin    RepositoryPermission = "ADMIN"
	RepositoryPermissionMaintain RepositoryPermission = "MAINTAIN"
	RepositoryPermissionWrite    RepositoryPermission = "WRITE"
	RepositoryPermissionTriage   RepositoryPermission = "TRIAGE"
	RepositoryPermissionRead     RepositoryPermission = "READ"
)

// Repository is the GraphQL Repository object. It carries the fields the gh CLI
// selects for repo view, repo list, and the pull request create flow.
// Nullable GraphQL fields use Go pointers so gqlgen renders null rather than a
// zero value.
type Repository struct {
	ID               string               // the Repository node ID
	Name             string               // the short repository name
	NameWithOwner    string               // owner login + "/" + name
	Description      *string              // null when unset
	IsPrivate        bool                 // visibility
	IsFork           bool                 // whether this is a fork
	IsArchived       bool                 // whether this is archived
	IsEmpty          bool                 // true when the repository has no commits
	IsInOrganization bool                 // true when owner is an org
	ForkCount        int32                // number of forks
	StargazerCount   int32                // number of stars
	DiskUsage        *int32               // disk usage in KB, null when unavailable
	HomepageURL      *URI                 // null when unset
	CreatedAt        DateTime             // creation instant
	UpdatedAt        DateTime             // last metadata update
	PushedAt         *DateTime            // last push, null for a repository with no commits
	URL              URI                  // the repository's HTML URL
	SSHURL           URI                  // the SSH clone URL
	HTTPSCloneURL    URI                  // the HTTPS clone URL
	ViewerPermission *RepositoryPermission // viewer's permission, null for anonymous
	AutoMergeAllowed bool                 // whether auto-merge can be enabled (always true)
	MergeCommitAllowed bool               // whether merge commits are allowed (always true)
	SquashMergeAllowed bool               // whether squash merges are allowed (always true)
	RebaseMergeAllowed bool               // whether rebase merges are allowed (always true)
	DefaultBranchRef *Ref                 // the head branch, null for an empty repository

	// RepoOwner and RepoName carry the repository coordinates for resolvers that
	// need to look up ref or language data on demand. They are not part of the
	// GraphQL schema.
	RepoOwner string
	RepoName  string
}

// IsNode marks Repository as implementing the Node interface.
func (Repository) IsNode() {}

// GetID satisfies the Node interface getter gqlgen requires.
func (r Repository) GetID() string { return r.ID }

// IsSearchResultItem marks Repository as a member of the SearchResultItem union.
func (Repository) IsSearchResultItem() {}

// RepositoryConnection is the Relay connection over a set of repositories.
type RepositoryConnection struct {
	Nodes      []*Repository
	PageInfo   *PageInfo
	TotalCount int32
}

// Ref is a git reference. The id field carries the opaque node ID clients pass
// to deleteRef. Prefix is the full prefix (refs/heads/ or refs/tags/).
type Ref struct {
	ID     string     // the Ref node ID
	Name   string     // the short ref name, such as main
	Prefix string     // the full prefix, e.g. "refs/heads/"
	Target *GitObject // the object the ref names
}

// IsNode marks Ref as implementing the Node interface.
func (Ref) IsNode() {}

// GetID satisfies the Node interface getter gqlgen requires.
func (r Ref) GetID() string { return r.ID }

// GitObject is a git object addressed by its SHA. The M2 schema models it as a
// concrete type carrying only oid, which is all gh repo view selects; the full
// GitObject interface with Commit, Tree, Blob, and Tag arrives with the GraphQL
// parity milestone.
type GitObject struct {
	Oid GitObjectID
}

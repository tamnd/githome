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

// RepositoryVisibility is a repository's visibility level.
type RepositoryVisibility string

// The RepositoryVisibility values.
const (
	RepositoryVisibilityPublic   RepositoryVisibility = "PUBLIC"
	RepositoryVisibilityPrivate  RepositoryVisibility = "PRIVATE"
	RepositoryVisibilityInternal RepositoryVisibility = "INTERNAL"
)

// Repository is the GraphQL Repository object. It carries the fields the gh CLI
// selects for repo view, repo list, and the pull request create flow.
// Nullable GraphQL fields use Go pointers so gqlgen renders null rather than a
// zero value.
type Repository struct {
	ID                 string                // the Repository node ID
	Name               string                // the short repository name
	NameWithOwner      string                // owner login + "/" + name
	Description        *string               // null when unset
	IsPrivate          bool                  // visibility
	IsFork             bool                  // whether this is a fork
	IsArchived         bool                  // whether this is archived
	IsEmpty            bool                  // true when the repository has no commits
	IsInOrganization   bool                  // true when owner is an org
	ForkCount          int32                 // number of forks
	StargazerCount     int32                 // number of stars
	DiskUsage          *int32                // disk usage in KB, null when unavailable
	HomepageURL        *URI                  // null when unset
	CreatedAt          DateTime              // creation instant
	UpdatedAt          DateTime              // last metadata update
	PushedAt           *DateTime             // last push, null for a repository with no commits
	URL                URI                   // the repository's HTML URL
	SSHURL             GitSSHRemote          // the SSH clone URL, GitHub's GitSSHRemote scalar
	DatabaseID         *int32                // the integer database id REST calls id
	Visibility         RepositoryVisibility  // PUBLIC, PRIVATE, or INTERNAL
	ViewerPermission   *RepositoryPermission // viewer's permission, null for anonymous
	ViewerCanAdminister bool                 // whether the viewer can administer the repository
	ViewerDefaultMergeMethod PullRequestMergeMethod // the viewer's default merge method
	HasIssuesEnabled   bool                  // whether the repository accepts issues
	HasWikiEnabled     bool                  // whether the repository has a wiki
	HasProjectsEnabled bool                  // whether the repository has projects
	HasDiscussionsEnabled bool               // whether the repository has discussions (always false)
	IsTemplate         bool                  // whether the repository is a template
	IsMirror           bool                  // whether the repository is a mirror (always false)
	MirrorURL          *URI                  // the mirror source URL (always null)
	DeleteBranchOnMerge bool                 // whether head branches are deleted on merge
	AutoMergeAllowed   bool                  // whether auto-merge can be enabled (always true)
	MergeCommitAllowed bool                  // whether merge commits are allowed (always true)
	SquashMergeAllowed bool                  // whether squash merges are allowed (always true)
	RebaseMergeAllowed bool                  // whether rebase merges are allowed (always true)
	DefaultBranchRef   *Ref                  // the head branch, null for an empty repository
	RepositoryTopics   *RepositoryTopicConnection // the repository's topics
	Watchers           *UserConnection       // the users watching the repository
	Languages          *LanguageConnection   // detected languages (always empty)
	IssueTemplates     []*IssueTemplate      // configured issue templates (always null)
	PullRequestTemplates []*PullRequestTemplate // configured PR templates (always null)

	// RepoOwner, RepoName, and ForkParentID carry the repository coordinates for
	// resolvers that look up ref, milestone, release, or parent data on demand.
	// They are not part of the GraphQL schema.
	RepoOwner    string
	RepoName     string
	ForkParentPK *int64 // internal pk of the parent repo, nil for a non-fork
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

// Topic is a repository topic.
type Topic struct {
	ID   string
	Name string
}

// IsNode marks Topic as implementing the Node interface.
func (Topic) IsNode() {}

// GetID satisfies the Node interface getter gqlgen requires.
func (t Topic) GetID() string { return t.ID }

// RepositoryTopic pairs a topic with the repository it is applied to.
type RepositoryTopic struct {
	ID    string
	Topic *Topic
	URL   URI
}

// IsNode marks RepositoryTopic as implementing the Node interface.
func (RepositoryTopic) IsNode() {}

// GetID satisfies the Node interface getter gqlgen requires.
func (r RepositoryTopic) GetID() string { return r.ID }

// RepositoryTopicConnection is the connection over a repository's topics.
type RepositoryTopicConnection struct {
	Nodes      []*RepositoryTopic
	PageInfo   *PageInfo
	TotalCount int32
}

// LanguageConnection is the connection over the languages in a repository.
type LanguageConnection struct {
	Edges      []*LanguageEdge
	Nodes      []*Language
	PageInfo   *PageInfo
	TotalCount int32
	TotalSize  int32
}

// LanguageEdge pairs a language with the number of bytes written in it.
type LanguageEdge struct {
	Cursor string
	Node   *Language
	Size   int32
}

// MilestoneConnection is the connection over a repository's milestones.
type MilestoneConnection struct {
	Nodes      []*Milestone
	PageInfo   *PageInfo
	TotalCount int32
}

// Release is a published release of a repository.
type Release struct {
	ID           string
	Name         *string
	TagName      string
	URL          URI
	CreatedAt    DateTime
	PublishedAt  *DateTime
	IsLatest     bool
	IsPrerelease bool
	IsDraft      bool
}

// IsNode marks Release as implementing the Node interface.
func (Release) IsNode() {}

// GetID satisfies the Node interface getter gqlgen requires.
func (r Release) GetID() string { return r.ID }

// IssueTemplate is an issue template configured in a repository.
type IssueTemplate struct {
	Name  string
	Title *string
	Body  *string
	About *string
}

// PullRequestTemplate is a pull-request template configured in a repository.
type PullRequestTemplate struct {
	Filename *string
	Body     *string
}

// Ref is a git reference. The id field carries the opaque node ID clients pass
// to deleteRef. Prefix is the full prefix (refs/heads/ or refs/tags/).
type Ref struct {
	ID     string    // the Ref node ID
	Name   string    // the short ref name, such as main
	Prefix string    // the full prefix, e.g. "refs/heads/"
	Target GitObject // the object the ref names
}

// IsNode marks Ref as implementing the Node interface.
func (Ref) IsNode() {}

// GetID satisfies the Node interface getter gqlgen requires.
func (r Ref) GetID() string { return r.ID }

// GitObject is the interface a git object addressed by its SHA implements:
// Commit, Tree, Blob, and Tag. Ref.target carries it, so `... on Commit`
// inline fragments narrow it the way GitHub's schema allows.
type GitObject interface {
	IsGitObject()
}

// abbreviateOid is the short seven-hex form GitHub's abbreviatedOid renders.
func abbreviateOid(oid GitObjectID) string {
	if len(oid) < 7 {
		return string(oid)
	}
	return string(oid[:7])
}

// Tree is a git tree object.
type Tree struct {
	ID  string // the Tree node ID
	Oid GitObjectID
}

// IsGitObject marks Tree as implementing the GitObject interface.
func (Tree) IsGitObject() {}

// IsNode marks Tree as implementing the Node interface.
func (Tree) IsNode() {}

// GetID satisfies the Node interface getter gqlgen requires.
func (t Tree) GetID() string { return t.ID }

// AbbreviatedOid is the short form of the tree's SHA.
func (t Tree) AbbreviatedOid() string { return abbreviateOid(t.Oid) }

// Blob is a git blob object.
type Blob struct {
	ID  string // the Blob node ID
	Oid GitObjectID
}

// IsGitObject marks Blob as implementing the GitObject interface.
func (Blob) IsGitObject() {}

// IsNode marks Blob as implementing the Node interface.
func (Blob) IsNode() {}

// GetID satisfies the Node interface getter gqlgen requires.
func (b Blob) GetID() string { return b.ID }

// AbbreviatedOid is the short form of the blob's SHA.
func (b Blob) AbbreviatedOid() string { return abbreviateOid(b.Oid) }

// Tag is an annotated git tag object.
type Tag struct {
	ID     string // the Tag node ID
	Oid    GitObjectID
	Name   string    // the tag name
	Target GitObject // the object the tag points at
}

// IsGitObject marks Tag as implementing the GitObject interface.
func (Tag) IsGitObject() {}

// IsNode marks Tag as implementing the Node interface.
func (Tag) IsNode() {}

// GetID satisfies the Node interface getter gqlgen requires.
func (t Tag) GetID() string { return t.ID }

// AbbreviatedOid is the short form of the tag object's SHA.
func (t Tag) AbbreviatedOid() string { return abbreviateOid(t.Oid) }

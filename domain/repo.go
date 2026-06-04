package domain

import "time"

// Repo is the domain view of a repository. It is the presenter's input for the
// Repository wire model and the handle the git-data reads work through.
//
// ID is the public database id (repositories.db_id), the value rendered as the
// REST "id". PK is the internal primary key; the git store shards bare
// repositories by it, so the service carries it for git access. The presenter
// never reads PK. OwnerPK is the owning user's internal pk, which the REST
// handler compares against the actor to decide the permissions block; it is
// also not rendered. Owner is the resolved owning account, the presenter's
// input for the embedded SimpleUser.
type Repo struct {
	PK      int64
	OwnerPK int64
	ID      int64
	Owner   *User

	Name          string
	Description   *string
	Homepage      *string
	Private       bool
	Fork          bool
	DefaultBranch string

	HasIssues    bool
	HasProjects  bool
	HasWiki      bool
	HasDownloads bool
	Archived     bool
	Disabled     bool
	IsTemplate   bool

	OpenIssuesCount int
	PushedAt        *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// FullName is the owner/name pair GitHub renders as full_name and uses to build
// the repository's URLs.
func (r *Repo) FullName() string {
	if r.Owner == nil {
		return r.Name
	}
	return r.Owner.Login + "/" + r.Name
}

package restmodel

// Organization is the org profile returned by GET /orgs/{org}. In Githome
// organizations share the users table, so the profile fields mirror User; the
// URLs and type are the org-flavored ones GitHub serves.
type Organization struct {
	Login            string  `json:"login"`
	ID               int64   `json:"id"`
	NodeID           string  `json:"node_id"`
	URL              string  `json:"url"`
	ReposURL         string  `json:"repos_url"`
	EventsURL        string  `json:"events_url"`
	HooksURL         string  `json:"hooks_url"`
	IssuesURL        string  `json:"issues_url"`
	MembersURL       string  `json:"members_url"`
	PublicMembersURL string  `json:"public_members_url"`
	AvatarURL        string  `json:"avatar_url"`
	Description      *string `json:"description"`
	Name             *string `json:"name"`
	Company          *string `json:"company"`
	Blog             string  `json:"blog"`
	Location         *string `json:"location"`
	Email            *string `json:"email"`
	TwitterUsername  *string `json:"twitter_username"`
	IsVerified       bool    `json:"is_verified"`
	HasOrgProjects   bool    `json:"has_organization_projects"`
	HasRepoProjects  bool    `json:"has_repository_projects"`
	PublicRepos      int     `json:"public_repos"`
	PublicGists      int     `json:"public_gists"`
	Followers        int     `json:"followers"`
	Following        int     `json:"following"`
	HTMLURL          string  `json:"html_url"`
	CreatedAt        Time    `json:"created_at"`
	UpdatedAt        Time    `json:"updated_at"`
	ArchivedAt       *Time   `json:"archived_at"`
	Type             string  `json:"type"`
}

// OrganizationSimple is the trimmed org shape GitHub returns inside list
// payloads such as GET /user/orgs and GET /orgs/{org}/memberships.
type OrganizationSimple struct {
	Login            string  `json:"login"`
	ID               int64   `json:"id"`
	NodeID           string  `json:"node_id"`
	URL              string  `json:"url"`
	ReposURL         string  `json:"repos_url"`
	EventsURL        string  `json:"events_url"`
	HooksURL         string  `json:"hooks_url"`
	IssuesURL        string  `json:"issues_url"`
	MembersURL       string  `json:"members_url"`
	PublicMembersURL string  `json:"public_members_url"`
	AvatarURL        string  `json:"avatar_url"`
	Description      *string `json:"description"`
}

// OrgMembership is the membership shape GitHub returns from
// GET /user/memberships/orgs and GET /user/memberships/orgs/{org}.
type OrgMembership struct {
	URL             string             `json:"url"`
	State           string             `json:"state"`
	Role            string             `json:"role"`
	OrganizationURL string             `json:"organization_url"`
	Organization    OrganizationSimple `json:"organization"`
	User            SimpleUser         `json:"user"`
}

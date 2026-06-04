package restmodel

// SimpleUser is the embedded actor representation used everywhere an actor is
// referenced (owner, author, assignee, and so on). Every field is present on
// github.com; none are omitted except the two that only appear in specific
// listing contexts. gravatar_id is modeled as *string only so a future null is
// matchable; the presenter always sets it to a pointer to "".
type SimpleUser struct {
	Login             string  `json:"login"`
	ID                int64   `json:"id"`
	NodeID            string  `json:"node_id"`
	AvatarURL         string  `json:"avatar_url"`
	GravatarID        *string `json:"gravatar_id"`
	URL               string  `json:"url"`
	HTMLURL           string  `json:"html_url"`
	FollowersURL      string  `json:"followers_url"`
	FollowingURL      string  `json:"following_url"`
	GistsURL          string  `json:"gists_url"`
	StarredURL        string  `json:"starred_url"`
	SubscriptionsURL  string  `json:"subscriptions_url"`
	OrganizationsURL  string  `json:"organizations_url"`
	ReposURL          string  `json:"repos_url"`
	EventsURL         string  `json:"events_url"`
	ReceivedEventsURL string  `json:"received_events_url"`
	Type              string  `json:"type"`
	SiteAdmin         bool    `json:"site_admin"`
	StarredAt         *Time   `json:"starred_at,omitempty"`
	UserViewType      *string `json:"user_view_type,omitempty"`
}

// User is the full profile returned by GET /users/{login} and GET /user. It
// embeds SimpleUser and adds the profile fields. The authenticated-user view
// (GET /user) additionally carries the private counters, which are omitted for
// other users.
type User struct {
	SimpleUser
	Name            *string `json:"name"`
	Company         *string `json:"company"`
	Blog            string  `json:"blog"`
	Location        *string `json:"location"`
	Email           *string `json:"email"`
	Hireable        *bool   `json:"hireable"`
	Bio             *string `json:"bio"`
	TwitterUsername *string `json:"twitter_username"`
	PublicRepos     int     `json:"public_repos"`
	PublicGists     int     `json:"public_gists"`
	Followers       int     `json:"followers"`
	Following       int     `json:"following"`
	CreatedAt       Time    `json:"created_at"`
	UpdatedAt       Time    `json:"updated_at"`

	// Authenticated-user-only fields; omitted when rendering another user.
	PrivateGists            *int  `json:"private_gists,omitempty"`
	TotalPrivateRepos       *int  `json:"total_private_repos,omitempty"`
	OwnedPrivateRepos       *int  `json:"owned_private_repos,omitempty"`
	DiskUsage               *int  `json:"disk_usage,omitempty"`
	Collaborators           *int  `json:"collaborators,omitempty"`
	TwoFactorAuthentication *bool `json:"two_factor_authentication,omitempty"`
}

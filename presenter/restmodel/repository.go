package restmodel

import "encoding/json"

// Repository is the wire shape GitHub serves for a repository. The field set,
// order of presence, types, and nullability match github.com's repository
// object as returned by GET /repos/{owner}/{repo} and embedded in listings. The
// large url family carries RFC 6570 templates (the {/sha}, {+path}, {?since}
// suffixes) exactly as GitHub emits them, so clients that expand the templates
// build the same paths.
//
// The merge options, network and subscriber counts, organization, and fork
// parent/source live at the end of the struct and render only on the
// single-repository shape, the way GitHub scopes them. Language is always
// null until language detection lands; license is always null until license
// detection lands.
type Repository struct {
	ID       int64      `json:"id"`
	NodeID   string     `json:"node_id"`
	Name     string     `json:"name"`
	FullName string     `json:"full_name"`
	Owner    SimpleUser `json:"owner"`
	Private  bool       `json:"private"`
	HTMLURL  string     `json:"html_url"`
	Desc     *string    `json:"description"`
	Fork     bool       `json:"fork"`
	URL      string     `json:"url"`

	ForksURL         string `json:"forks_url"`
	KeysURL          string `json:"keys_url"`
	CollaboratorsURL string `json:"collaborators_url"`
	TeamsURL         string `json:"teams_url"`
	HooksURL         string `json:"hooks_url"`
	IssueEventsURL   string `json:"issue_events_url"`
	EventsURL        string `json:"events_url"`
	AssigneesURL     string `json:"assignees_url"`
	BranchesURL      string `json:"branches_url"`
	TagsURL          string `json:"tags_url"`
	BlobsURL         string `json:"blobs_url"`
	GitTagsURL       string `json:"git_tags_url"`
	GitRefsURL       string `json:"git_refs_url"`
	TreesURL         string `json:"trees_url"`
	StatusesURL      string `json:"statuses_url"`
	LanguagesURL     string `json:"languages_url"`
	StargazersURL    string `json:"stargazers_url"`
	ContributorsURL  string `json:"contributors_url"`
	SubscribersURL   string `json:"subscribers_url"`
	SubscriptionURL  string `json:"subscription_url"`
	CommitsURL       string `json:"commits_url"`
	GitCommitsURL    string `json:"git_commits_url"`
	CommentsURL      string `json:"comments_url"`
	IssueCommentURL  string `json:"issue_comment_url"`
	ContentsURL      string `json:"contents_url"`
	CompareURL       string `json:"compare_url"`
	MergesURL        string `json:"merges_url"`
	ArchiveURL       string `json:"archive_url"`
	DownloadsURL     string `json:"downloads_url"`
	IssuesURL        string `json:"issues_url"`
	PullsURL         string `json:"pulls_url"`
	MilestonesURL    string `json:"milestones_url"`
	NotificationsURL string `json:"notifications_url"`
	LabelsURL        string `json:"labels_url"`
	ReleasesURL      string `json:"releases_url"`
	DeploymentsURL   string `json:"deployments_url"`

	CreatedAt Time  `json:"created_at"`
	UpdatedAt Time  `json:"updated_at"`
	PushedAt  *Time `json:"pushed_at"`

	GitURL   string `json:"git_url"`
	SSHURL   string `json:"ssh_url"`
	CloneURL string `json:"clone_url"`
	SVNURL   string `json:"svn_url"`

	Homepage        *string `json:"homepage"`
	Size            int     `json:"size"`
	StargazersCount int     `json:"stargazers_count"`
	WatchersCount   int     `json:"watchers_count"`
	Language        *string `json:"language"`

	HasIssues      bool `json:"has_issues"`
	HasProjects    bool `json:"has_projects"`
	HasDownloads   bool `json:"has_downloads"`
	HasWiki        bool `json:"has_wiki"`
	HasPages       bool `json:"has_pages"`
	HasDiscussions bool `json:"has_discussions"`

	ForksCount      int            `json:"forks_count"`
	MirrorURL       *string        `json:"mirror_url"`
	Archived        bool           `json:"archived"`
	Disabled        bool           `json:"disabled"`
	OpenIssuesCount int            `json:"open_issues_count"`
	License         *LicenseSimple `json:"license"`

	AllowForking             bool     `json:"allow_forking"`
	IsTemplate               bool     `json:"is_template"`
	WebCommitSignoffRequired bool     `json:"web_commit_signoff_required"`
	Topics                   []string `json:"topics"`
	Visibility               string   `json:"visibility"`

	Forks         int    `json:"forks"`
	OpenIssues    int    `json:"open_issues"`
	Watchers      int    `json:"watchers"`
	DefaultBranch string `json:"default_branch"`

	Permissions *RepoPermissions `json:"permissions,omitempty"`

	// The fields below appear only on single-repository responses (get,
	// create, update, fork), never on list items, matching GitHub. They are
	// pointers (or raw JSON for the always-null token) so the list shape
	// omits them entirely.

	// TempCloneToken is always null: githome has no temporary clone tokens.
	// It is json.RawMessage so the full shape can render an explicit null
	// while list items omit the key.
	TempCloneToken json.RawMessage `json:"temp_clone_token,omitempty"`

	// The merge-policy settings. GitHub returns them only when the caller
	// administers the repository.
	AllowSquashMerge    *bool `json:"allow_squash_merge,omitempty"`
	AllowMergeCommit    *bool `json:"allow_merge_commit,omitempty"`
	AllowRebaseMerge    *bool `json:"allow_rebase_merge,omitempty"`
	AllowAutoMerge      *bool `json:"allow_auto_merge,omitempty"`
	DeleteBranchOnMerge *bool `json:"delete_branch_on_merge,omitempty"`
	AllowUpdateBranch   *bool `json:"allow_update_branch,omitempty"`

	// Organization mirrors the owner when the owner is an organization.
	Organization *SimpleUser `json:"organization,omitempty"`

	// Parent is the repository this one was forked from; Source is the root
	// of the fork network. Both are list-shaped repository objects.
	Parent *Repository `json:"parent,omitempty"`
	Source *Repository `json:"source,omitempty"`

	NetworkCount     *int `json:"network_count,omitempty"`
	SubscribersCount *int `json:"subscribers_count,omitempty"`
}

// RepoPermissions is the actor's effective access on a repository. GitHub
// includes it on authenticated requests; it is omitted for anonymous ones.
type RepoPermissions struct {
	Admin    bool `json:"admin"`
	Maintain bool `json:"maintain"`
	Push     bool `json:"push"`
	Triage   bool `json:"triage"`
	Pull     bool `json:"pull"`
}

// LicenseSimple is the embedded license object. Githome does not detect licenses
// yet, so Repository.License is always null; the type exists so the value is
// matchable once detection lands.
type LicenseSimple struct {
	Key     string  `json:"key"`
	Name    string  `json:"name"`
	URL     *string `json:"url"`
	SPDXID  *string `json:"spdx_id"`
	NodeID  string  `json:"node_id"`
	HTMLURL string  `json:"html_url"`
}

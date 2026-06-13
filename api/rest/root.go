package rest

import (
	"net/http"

	"github.com/go-mizu/mizu"
)

// apiRoot is GitHub's root hypermedia document: the *_url template map clients
// like Octokit's hypermedia mode read to derive every other endpoint instead of
// hardcoding paths. Field order matches the GitHub REST API root.
type apiRoot struct {
	CurrentUserURL                   string `json:"current_user_url"`
	CurrentUserAuthorizationsHTMLURL string `json:"current_user_authorizations_html_url"`
	AuthorizationsURL                string `json:"authorizations_url"`
	CodeSearchURL                    string `json:"code_search_url"`
	CommitSearchURL                  string `json:"commit_search_url"`
	EmailsURL                        string `json:"emails_url"`
	EmojisURL                        string `json:"emojis_url"`
	EventsURL                        string `json:"events_url"`
	FeedsURL                         string `json:"feeds_url"`
	FollowersURL                     string `json:"followers_url"`
	FollowingURL                     string `json:"following_url"`
	GistsURL                         string `json:"gists_url"`
	IssueSearchURL                   string `json:"issue_search_url"`
	IssuesURL                        string `json:"issues_url"`
	KeysURL                          string `json:"keys_url"`
	LabelSearchURL                   string `json:"label_search_url"`
	NotificationsURL                 string `json:"notifications_url"`
	OrganizationURL                  string `json:"organization_url"`
	OrganizationRepositoriesURL      string `json:"organization_repositories_url"`
	OrganizationTeamsURL             string `json:"organization_teams_url"`
	PublicGistsURL                   string `json:"public_gists_url"`
	RateLimitURL                     string `json:"rate_limit_url"`
	RepositoryURL                    string `json:"repository_url"`
	RepositorySearchURL              string `json:"repository_search_url"`
	CurrentUserRepositoriesURL       string `json:"current_user_repositories_url"`
	StarredURL                       string `json:"starred_url"`
	StarredGistsURL                  string `json:"starred_gists_url"`
	TopicSearchURL                   string `json:"topic_search_url"`
	UserURL                          string `json:"user_url"`
	UserOrganizationsURL             string `json:"user_organizations_url"`
	UserRepositoriesURL              string `json:"user_repositories_url"`
	UserSearchURL                    string `json:"user_search_url"`
}

// handleAPIRoot serves GET at the API root. The body is the same template map
// the GitHub REST API root returns, with every URL rebuilt on the configured
// hosts. It is computed once at mount time: nothing in it varies per request.
func handleAPIRoot(d Deps) mizu.Handler {
	api := d.URLs.APIBase()
	doc := apiRoot{
		CurrentUserURL:                   api + "/user",
		CurrentUserAuthorizationsHTMLURL: d.URLs.HTMLBase() + "/settings/connections/applications{/client_id}",
		AuthorizationsURL:                api + "/authorizations",
		CodeSearchURL:                    api + "/search/code?q={query}{&page,per_page,sort,order}",
		CommitSearchURL:                  api + "/search/commits?q={query}{&page,per_page,sort,order}",
		EmailsURL:                        api + "/user/emails",
		EmojisURL:                        api + "/emojis",
		EventsURL:                        api + "/events",
		FeedsURL:                         api + "/feeds",
		FollowersURL:                     api + "/user/followers",
		FollowingURL:                     api + "/user/following{/target}",
		GistsURL:                         api + "/gists{/gist_id}",
		IssueSearchURL:                   api + "/search/issues?q={query}{&page,per_page,sort,order}",
		IssuesURL:                        api + "/issues",
		KeysURL:                          api + "/user/keys",
		LabelSearchURL:                   api + "/search/labels?q={query}&repository_id={repository_id}{&page,per_page}",
		NotificationsURL:                 api + "/notifications",
		OrganizationURL:                  api + "/orgs/{org}",
		OrganizationRepositoriesURL:      api + "/orgs/{org}/repos{?type,page,per_page,sort}",
		OrganizationTeamsURL:             api + "/orgs/{org}/teams",
		PublicGistsURL:                   api + "/gists/public",
		RateLimitURL:                     api + "/rate_limit",
		RepositoryURL:                    api + "/repos/{owner}/{repo}",
		RepositorySearchURL:              api + "/search/repositories?q={query}{&page,per_page,sort,order}",
		CurrentUserRepositoriesURL:       api + "/user/repos{?type,page,per_page,sort}",
		StarredURL:                       api + "/user/starred{/owner}{/repo}",
		StarredGistsURL:                  api + "/gists/starred",
		TopicSearchURL:                   api + "/search/topics?q={query}{&page,per_page}",
		UserURL:                          api + "/users/{user}",
		UserOrganizationsURL:             api + "/user/orgs",
		UserRepositoriesURL:              api + "/users/{user}/repos{?type,page,per_page,sort}",
		UserSearchURL:                    api + "/search/users?q={query}{&page,per_page,sort,order}",
	}
	return func(c *mizu.Ctx) error {
		writeJSON(c.Writer(), http.StatusOK, doc)
		return nil
	}
}

// Package presenter renders domain values into the exact wire shapes GitHub
// emits. The urlbuilder defined here owns every URL that appears in a response,
// so links are always built from the configured host and the two API surfaces
// cannot disagree about a resource's URLs.
package presenter

import (
	"fmt"
	"net/url"

	"github.com/tamnd/githome/config"
)

// URLBuilder constructs the absolute URLs that responses embed. It is created
// once from the resolved configuration and is safe for concurrent use.
type URLBuilder struct {
	api     *url.URL
	html    *url.URL
	graphql *url.URL
	sshHost string
	sshPort int
}

// NewURLBuilder builds a URLBuilder from the resolved config URLs.
func NewURLBuilder(u config.URLs) *URLBuilder {
	return &URLBuilder{
		api:     u.API,
		html:    u.HTML,
		graphql: u.GraphQL,
		sshHost: u.SSHHost,
		sshPort: u.SSHPort,
	}
}

// APIBase returns the API root, e.g. https://git.example.com/api/v3.
func (b *URLBuilder) APIBase() string { return b.api.String() }

// HTMLBase returns the site root, e.g. https://git.example.com.
func (b *URLBuilder) HTMLBase() string { return b.html.String() }

// GraphQLEndpoint returns the GraphQL endpoint URL.
func (b *URLBuilder) GraphQLEndpoint() string { return b.graphql.String() }

// API joins path segments onto the API base.
func (b *URLBuilder) API(segments ...string) string {
	return b.api.JoinPath(segments...).String()
}

// HTML joins path segments onto the site base.
func (b *URLBuilder) HTML(segments ...string) string {
	return b.html.JoinPath(segments...).String()
}

// UserAPI returns the API URL for a user, e.g. {api}/users/{login}.
func (b *URLBuilder) UserAPI(login string) string { return b.API("users", login) }

// UserHTML returns the site URL for a user, e.g. {html}/{login}.
func (b *URLBuilder) UserHTML(login string) string { return b.HTML(login) }

// RepoAPI returns the API URL for a repository.
func (b *URLBuilder) RepoAPI(owner, repo string) string { return b.API("repos", owner, repo) }

// RepoHTML returns the site URL for a repository.
func (b *URLBuilder) RepoHTML(owner, repo string) string { return b.HTML(owner, repo) }

// RepoGitHTTP returns the smart-HTTP clone URL, e.g. {html}/{owner}/{repo}.git.
func (b *URLBuilder) RepoGitHTTP(owner, repo string) string {
	return b.HTML(owner) + "/" + repo + ".git"
}

// RepoGitSSH returns the SSH clone URL, omitting the port when it is the
// default 22, matching GitHub's scp-like form.
func (b *URLBuilder) RepoGitSSH(owner, repo string) string {
	if b.sshPort == 0 || b.sshPort == 22 {
		return fmt.Sprintf("git@%s:%s/%s.git", b.sshHost, owner, repo)
	}
	return fmt.Sprintf("ssh://git@%s:%d/%s/%s.git", b.sshHost, b.sshPort, owner, repo)
}

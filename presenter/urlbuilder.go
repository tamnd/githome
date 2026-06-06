// Package presenter renders domain values into the exact wire shapes GitHub
// emits. The urlbuilder defined here owns every URL that appears in a response,
// so links are always built from the configured host and the two API surfaces
// cannot disagree about a resource's URLs.
package presenter

import (
	"fmt"
	"net/url"
	"strconv"

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

// PageLink returns the absolute URL a Link header rel points at: the given
// request path on the configured API host, carrying every query parameter
// through unchanged except page, which is set to the target. Building it on the
// API host (never the inbound Host header) keeps the link on the Githome host
// the same way every other embedded URL is.
func (b *URLBuilder) PageLink(path, rawQuery string, page int) string {
	u := url.URL{Scheme: b.api.Scheme, Host: b.api.Host, Path: path}
	q, _ := url.ParseQuery(rawQuery)
	q.Set("page", strconv.Itoa(page))
	u.RawQuery = q.Encode()
	return u.String()
}

// CursorLink returns the URL for the next page identified by an opaque cursor.
// It carries per_page through from the original query and drops the page and
// cursor parameters so the link is a clean cursor reference.
func (b *URLBuilder) CursorLink(path, rawQuery, cursor string) string {
	u := url.URL{Scheme: b.api.Scheme, Host: b.api.Host, Path: path}
	q, _ := url.ParseQuery(rawQuery)
	q.Del("page")
	q.Set("cursor", cursor)
	u.RawQuery = q.Encode()
	return u.String()
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

// RepoGitProto returns the anonymous git-protocol URL, e.g.
// git://{host}/{owner}/{repo}.git, built from the site host.
func (b *URLBuilder) RepoGitProto(owner, repo string) string {
	return "git://" + b.html.Host + "/" + owner + "/" + repo + ".git"
}

// RepoGitSSH returns the SSH clone URL, omitting the port when it is the
// default 22, matching GitHub's scp-like form.
func (b *URLBuilder) RepoGitSSH(owner, repo string) string {
	if b.sshPort == 0 || b.sshPort == 22 {
		return fmt.Sprintf("git@%s:%s/%s.git", b.sshHost, owner, repo)
	}
	return fmt.Sprintf("ssh://git@%s:%d/%s/%s.git", b.sshHost, b.sshPort, owner, repo)
}

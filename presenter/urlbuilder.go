// Package presenter renders domain values into the exact wire shapes GitHub
// emits. The urlbuilder defined here owns every URL that appears in a response,
// so links are always built from the configured host and the two API surfaces
// cannot disagree about a resource's URLs.
package presenter

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

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

	// apiPrefix and htmlPrefix are the rendered bases without a trailing
	// slash, so the hot join path is one append instead of a url.URL round
	// trip per embedded link.
	apiPrefix  string
	htmlPrefix string
}

// NewURLBuilder builds a URLBuilder from the resolved config URLs.
func NewURLBuilder(u config.URLs) *URLBuilder {
	return &URLBuilder{
		api:        u.API,
		html:       u.HTML,
		graphql:    u.GraphQL,
		sshHost:    u.SSHHost,
		sshPort:    u.SSHPort,
		apiPrefix:  strings.TrimRight(u.API.String(), "/"),
		htmlPrefix: strings.TrimRight(u.HTML.String(), "/"),
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
	if s, ok := fastJoin(b.apiPrefix, segments); ok {
		return s
	}
	return b.api.JoinPath(segments...).String()
}

// HTML joins path segments onto the site base.
func (b *URLBuilder) HTML(segments ...string) string {
	if s, ok := fastJoin(b.htmlPrefix, segments); ok {
		return s
	}
	return b.html.JoinPath(segments...).String()
}

// fastJoin appends segments to a pre-rendered prefix in one allocation. It
// handles only segments whose bytes survive both URL escaping and path
// cleaning unchanged, which covers every login, repository name, and fixed
// route word; anything else (an empty segment, a dot segment, a character
// JoinPath would escape or collapse) reports false so the caller takes the
// url.URL route that defines the behavior.
func fastJoin(prefix string, segments []string) (string, bool) {
	n := len(prefix)
	for _, seg := range segments {
		if seg == "" || seg == "." || seg == ".." {
			return "", false
		}
		for i := 0; i < len(seg); i++ {
			if !urlPlainByte(seg[i]) {
				return "", false
			}
		}
		n += 1 + len(seg)
	}
	buf := make([]byte, 0, n)
	buf = append(buf, prefix...)
	for _, seg := range segments {
		buf = append(buf, '/')
		buf = append(buf, seg...)
	}
	return string(buf), true
}

// suffixLinks renders base+suffix for every suffix into one shared backing
// string and fills out with the slices, so a family of related links costs a
// single allocation instead of one concat each. out must be at least as long
// as suffixes; callers pass a stack array sliced to size.
func suffixLinks(base string, suffixes, out []string) {
	n := 0
	for _, suf := range suffixes {
		n += len(base) + len(suf)
	}
	var sb strings.Builder
	sb.Grow(n)
	for _, suf := range suffixes {
		sb.WriteString(base)
		sb.WriteString(suf)
	}
	s := sb.String()
	at := 0
	for i, suf := range suffixes {
		end := at + len(base) + len(suf)
		out[i] = s[at:end]
		at = end
	}
}

// urlPlainByte reports whether c renders identically through url.PathEscape
// and path.Clean: the RFC 3986 unreserved set minus the dot-segment and
// separator machinery handled by fastJoin's segment checks.
func urlPlainByte(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	case c == '-' || c == '.' || c == '_' || c == '~':
		return true
	}
	return false
}

// PageLink returns the absolute URL a Link header rel points at: the given
// request path on the configured API host, carrying every query parameter
// through unchanged except page, which is set to the target, and cursor, which
// is dropped because a page-number link addresses a position, not a keyset.
// Building it on the API host (never the inbound Host header) keeps the link
// on the Githome host the same way every other embedded URL is.
func (b *URLBuilder) PageLink(path, rawQuery string, page int) string {
	u := url.URL{Scheme: b.api.Scheme, Host: b.api.Host, Path: path}
	q, _ := url.ParseQuery(rawQuery)
	q.Set("page", strconv.Itoa(page))
	q.Del("cursor")
	u.RawQuery = q.Encode()
	return u.String()
}

// CursorLink returns the URL for the next page identified by an opaque cursor.
// It carries per_page through from the original query and replaces the page and
// cursor parameters. page is the page number the cursor lands on; carrying it
// alongside the cursor lets the follow-up request know where it is, so it can
// offer rel="prev" and rel="first" page-number links, and lets clients that
// parse page out of Link URLs (go-github fills NextPage this way) keep working
// on the cursor path. A page below 1 drops the parameter.
func (b *URLBuilder) CursorLink(path, rawQuery, cursor string, page int) string {
	u := url.URL{Scheme: b.api.Scheme, Host: b.api.Host, Path: path}
	q, _ := url.ParseQuery(rawQuery)
	if page > 0 {
		q.Set("page", strconv.Itoa(page))
	} else {
		q.Del("page")
	}
	q.Set("cursor", cursor)
	u.RawQuery = q.Encode()
	return u.String()
}

// SinceLink returns the URL for the next page of an id-cursor listing such as
// GET /users: it carries every query parameter through unchanged except since,
// which is set to the last id seen, and page, which is dropped because an
// id-cursor addresses a position rather than a page number.
func (b *URLBuilder) SinceLink(path, rawQuery string, since int64) string {
	u := url.URL{Scheme: b.api.Scheme, Host: b.api.Host, Path: path}
	q, _ := url.ParseQuery(rawQuery)
	q.Set("since", strconv.FormatInt(since, 10))
	q.Del("page")
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

package domain

import "errors"

// ErrSearchScopeRequired is returned by the code search when the query names no
// repository or owner to scope the walk. GitHub rejects an unscoped code search
// the same way, since it cannot grep every repository on the host; the REST
// layer maps this to 422.
var ErrSearchScopeRequired = errors.New("domain: code search requires a repo, user, or org qualifier")

// IssueHit pairs a matched issue with the repository it belongs to, so the
// presenter can build the issue's URLs (which hang off the owner/name path)
// without a second lookup. Cross-repository search returns issues from many
// repositories, none of which is implied by the request path.
type IssueHit struct {
	Issue *Issue
	Repo  *Repo
}

// CodeResult is one matching file from a code search: the repository it lives
// in, its path within the head tree, and the blob's object id. The presenter
// renders the GitHub code-search item (name, path, sha, urls, repository) from
// it.
type CodeResult struct {
	Repo *Repo
	Path string
	Name string
	SHA  string
}

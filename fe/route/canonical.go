package route

import (
	"net/http"
	"strings"
)

// CanonicalRepoPath rebuilds a repository URL under its canonical owner and
// name. The store resolves /{owner}/{repo} case-insensitively, so a request
// for /Octocat/Hello finds octocat/hello; github.com answers that with a 301
// to the canonical casing rather than serving the page at every spelling
// (spec 02 §5.2). The same rewrite serves the rename redirect (§5.3): the old
// name resolves to the moved repository and the URL is rebuilt under where it
// lives now.
//
// escapedPath and rawQuery come straight off the request URL; reqOwner and
// reqRepo are the decoded path parameters the router matched. Only the first
// two path segments are replaced: the ref and file path that follow are
// case-sensitive and pass through untouched, query string included. ok is
// false when the request already uses the canonical spelling, or when the
// canonical pair is incomplete; the caller serves the page in place then.
func CanonicalRepoPath(escapedPath, rawQuery, reqOwner, reqRepo, owner, name string) (string, bool) {
	if owner == "" || name == "" {
		return "", false
	}
	if reqOwner == owner && reqRepo == name {
		return "", false
	}
	tail := strings.TrimPrefix(escapedPath, "/")
	for range 2 {
		if i := strings.IndexByte(tail, '/'); i >= 0 {
			tail = tail[i+1:]
		} else {
			tail = ""
		}
	}
	target := Repo(owner, name)
	if tail != "" {
		target += "/" + tail
	}
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	return target, true
}

// CanonicalRepoTarget is CanonicalRepoPath fed from a request, with the method
// gate every resolver shares: only GET and HEAD are canonicalized. A POST to a
// wrong-cased path operates on the repository the resolver already found, and
// a 301 would make the browser replay it as a GET, dropping the body.
func CanonicalRepoTarget(r *http.Request, reqOwner, reqRepo, owner, name string) (string, bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return "", false
	}
	return CanonicalRepoPath(r.URL.EscapedPath(), r.URL.RawQuery, reqOwner, reqRepo, owner, name)
}

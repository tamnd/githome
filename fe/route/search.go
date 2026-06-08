package route

// The search URL builders are the one place the web front turns a search request
// into a URL, so the header search box, the type rail, the sort menu, and the
// pager all link through the same two functions and can never drift from the
// routes that serve them. They follow the package rule: pure string functions
// with no router or domain dependency. The global box posts to /search; the
// in-repo box posts to /{owner}/{repo}/search and inherits the repo scope. See
// implementation/02 section 5 and implementation/12 section 2.

// Search is the global results page, /search. rawQuery is the already-encoded
// ?q=/?type=/?sort= string; an empty rawQuery yields the bare search landing.
func Search(rawQuery string) string {
	u := "/search"
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	return u
}

// RepoSearch is the in-repo results page, /{owner}/{repo}/search. The repo scope
// is implicit in the path, so the builder injects repo:{owner}/{name} before it
// runs the query; rawQuery carries only the viewer's own ?q= and the facets.
func RepoSearch(owner, name, rawQuery string) string {
	u := Repo(owner, name) + "/search"
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	return u
}

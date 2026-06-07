package route

import (
	"net/url"
	"strconv"
)

// The issue URL builders are the one place the web front turns an (owner, repo,
// number) tuple into a human-facing issues URL, so a link in a template and the
// route that serves it can never drift. They live beside the code-browsing
// builders in this package and follow the same rule: pure string functions with
// no router or domain dependency. The presenter owns the REST issue URLs; these
// own the HTML routes. See implementation/02 section 5 and implementation/08.

// Issues is the issues index, /{owner}/{repo}/issues. The optional rawQuery is the
// already-encoded ?q=/?page= filter string; an empty rawQuery yields the bare
// index URL the default-filter view canonicalizes to.
func Issues(owner, name, rawQuery string) string {
	u := Repo(owner, name) + "/issues"
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	return u
}

// Issue is one issue's detail page, /{owner}/{repo}/issues/{number}.
func Issue(owner, name string, number int64) string {
	return Repo(owner, name) + "/issues/" + strconv.FormatInt(number, 10)
}

// IssueComment is the permalink to a comment on an issue,
// /{owner}/{repo}/issues/{number}#issuecomment-{id}. The fragment is what the
// no-JS comment POST redirects to so a reload lands on the appended comment.
func IssueComment(owner, name string, number, commentID int64) string {
	return Issue(owner, name, number) + "#issuecomment-" + strconv.FormatInt(commentID, 10)
}

// NewIssue is the blank new-issue form, /{owner}/{repo}/issues/new.
func NewIssue(owner, name string) string {
	return Repo(owner, name) + "/issues/new"
}

// IssuesQuery builds the index URL for a literal ?q= filter value, encoding the
// query string itself so a value with spaces or quotes stays a single q
// parameter. It is the canonical target the filter tabs and label chips link to.
func IssuesQuery(owner, name, q string) string {
	if q == "" {
		return Issues(owner, name, "")
	}
	return Issues(owner, name, url.Values{"q": {q}}.Encode())
}

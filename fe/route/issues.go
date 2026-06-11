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

// Labels is the repository's label list, /{owner}/{repo}/labels.
func Labels(owner, name string) string {
	return Repo(owner, name) + "/labels"
}

// Milestones is the milestone list, /{owner}/{repo}/milestones. state selects
// the closed tab; empty or "open" yields the bare URL the default tab uses.
func Milestones(owner, name, state string) string {
	u := Repo(owner, name) + "/milestones"
	if state != "" && state != "open" {
		u += "?state=" + url.QueryEscape(state)
	}
	return u
}

// Milestone is one milestone's page, /{owner}/{repo}/milestone/{number},
// github.com's singular form. closed selects the closed-issues tab.
func Milestone(owner, name string, number int64, closed bool) string {
	u := Repo(owner, name) + "/milestone/" + strconv.FormatInt(number, 10)
	if closed {
		u += "?closed=1"
	}
	return u
}

// The form-target builders below name the POST endpoints the no-JS forms submit
// to. Every primary mutation has a plain HTML form whose action is one of these,
// so the page works with JavaScript disabled; the htmx path posts to the same
// URL. They mirror the routes mountIssues registers (implementation/08 section
// 2.3, 9).

// IssueComments is the new-comment POST target, /{owner}/{repo}/issues/{number}/comments.
func IssueComments(owner, name string, number int64) string {
	return Issue(owner, name, number) + "/comments"
}

// IssueState is the close/reopen POST target,
// /{owner}/{repo}/issues/{number}/state. The form carries the target state and an
// optional comment body so a viewer can close with a comment in one submit.
func IssueState(owner, name string, number int64) string {
	return Issue(owner, name, number) + "/state"
}

// IssueTitle is the edit-title POST target, /{owner}/{repo}/issues/{number}/title.
func IssueTitle(owner, name string, number int64) string {
	return Issue(owner, name, number) + "/title"
}

// IssueEdit is the sidebar edit POST target,
// /{owner}/{repo}/issues/{number}/edit, which replaces the labels, assignees, or
// milestone through the EditIssue patch.
func IssueEdit(owner, name string, number int64) string {
	return Issue(owner, name, number) + "/edit"
}

// IssueReactions is the reaction-toggle POST target for the issue body,
// /{owner}/{repo}/issues/{number}/reactions. The form carries the reaction
// content.
func IssueReactions(owner, name string, number int64) string {
	return Issue(owner, name, number) + "/reactions"
}

// CommentReactions is the reaction-toggle POST target for a comment,
// /{owner}/{repo}/issues/{number}/comments/{id}/reactions.
func CommentReactions(owner, name string, number, commentID int64) string {
	return IssueComments(owner, name, number) + "/" + strconv.FormatInt(commentID, 10) + "/reactions"
}

// CommentEdit is the edit POST target for a comment,
// /{owner}/{repo}/issues/{number}/comments/{id}.
func CommentEdit(owner, name string, number, commentID int64) string {
	return IssueComments(owner, name, number) + "/" + strconv.FormatInt(commentID, 10)
}

// CommentDelete is the delete POST target for a comment,
// /{owner}/{repo}/issues/{number}/comments/{id}/delete (a POST, since an HTML
// form cannot issue DELETE without JavaScript).
func CommentDelete(owner, name string, number, commentID int64) string {
	return CommentEdit(owner, name, number, commentID) + "/delete"
}

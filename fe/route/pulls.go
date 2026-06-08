package route

import (
	"net/url"
	"strconv"
)

// The pull-request URL builders are the one place the web front turns an
// (owner, repo, number) tuple into a human-facing pulls URL, so a link in a
// template and the route that serves it can never drift. They sit beside the
// issue builders and follow the same rule: pure string functions with no router
// or domain dependency. GitHub's pull URLs are singular under /pull/{n} but the
// index is plural at /pulls, and these keep that split. See implementation/02
// section 5 and implementation/09.

// Pulls is the pull-request index, /{owner}/{repo}/pulls. The optional rawQuery
// is the already-encoded ?q=/?page= filter string; an empty rawQuery yields the
// bare index URL the default filter canonicalizes to.
func Pulls(owner, name, rawQuery string) string {
	u := Repo(owner, name) + "/pulls"
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	return u
}

// PullsQuery builds the index URL for a literal ?q= filter value, encoding the
// query string itself so a value with spaces or quotes stays a single q
// parameter. It is the canonical target the state tabs link to.
func PullsQuery(owner, name, q string) string {
	if q == "" {
		return Pulls(owner, name, "")
	}
	return Pulls(owner, name, url.Values{"q": {q}}.Encode())
}

// Pull is the pull-request Conversation tab, /{owner}/{repo}/pull/{number}, the
// canonical PR page the four tabs hang off.
func Pull(owner, name string, number int64) string {
	return Repo(owner, name) + "/pull/" + strconv.FormatInt(number, 10)
}

// PullCommits is the Commits tab, /{owner}/{repo}/pull/{number}/commits.
func PullCommits(owner, name string, number int64) string {
	return Pull(owner, name, number) + "/commits"
}

// PullFiles is the Files-changed tab, /{owner}/{repo}/pull/{number}/files, the
// code-review surface where the shared diff component renders.
func PullFiles(owner, name string, number int64) string {
	return Pull(owner, name, number) + "/files"
}

// PullComment is the permalink to a comment on a PR's Conversation timeline,
// /{owner}/{repo}/pull/{number}#issuecomment-{id}. A PR shares the issue number
// space, so its conversation comments carry the same issuecomment anchor the
// issues timeline uses; the no-JS comment POST redirects here.
func PullComment(owner, name string, number, commentID int64) string {
	return Pull(owner, name, number) + "#issuecomment-" + strconv.FormatInt(commentID, 10)
}

// The form-target builders below name the POST endpoints the no-JS forms submit
// to. Every mutation has a plain HTML form whose action is one of these, so the
// page works with JavaScript disabled; the htmx path posts to the same URL.

// PullComments is the new-comment POST target on the Conversation tab,
// /{owner}/{repo}/pull/{number}/comments.
func PullComments(owner, name string, number int64) string {
	return Pull(owner, name, number) + "/comments"
}

// PullState is the close/reopen POST target on the Conversation tab,
// /{owner}/{repo}/pull/{number}/state. It is the PR's own state toggle rather than
// the issue's, so closing or reopening a pull request lands back on the PR page,
// not the issue view of the same number.
func PullState(owner, name string, number int64) string {
	return Pull(owner, name, number) + "/state"
}

// PullMerge is the merge POST target, /{owner}/{repo}/pull/{number}/merge. The
// form carries the merge method, the optional commit title and message, and the
// expected head SHA for optimistic concurrency.
func PullMerge(owner, name string, number int64) string {
	return Pull(owner, name, number) + "/merge"
}

// PullMergeBox is the merge-box poll fragment GET target,
// /{owner}/{repo}/pull/{number}/partials/merge-box. While the box is computing
// mergeability the component re-fetches this on a backoff; with JS off it is just
// the same partial the full page already shows.
func PullMergeBox(owner, name string, number int64) string {
	return Pull(owner, name, number) + "/partials/merge-box"
}

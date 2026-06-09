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

// The review-surface builders below name the code-review endpoints (F5). The inline
// thread machinery lives under the singular /pull/{number} prefix so it shares the
// PR's resolve and 404 gate; the anchors point at the Files tab where the threads
// render, the same place github.com anchors a discussion comment.

// PullReviewComments is the new-inline-comment POST target,
// /{owner}/{repo}/pull/{number}/review-comments. The form carries the anchor
// (path, side, line) and the head commit id the comment pins to; the domain
// validates the anchor against that commit's diff.
func PullReviewComments(owner, name string, number int64) string {
	return Pull(owner, name, number) + "/review-comments"
}

// PullReviewReply is the reply POST target for an existing thread,
// /{owner}/{repo}/pull/{number}/review-comments/{root}/replies, where root is the
// thread's first comment.
func PullReviewReply(owner, name string, number, rootID int64) string {
	return Pull(owner, name, number) + "/review-comments/" + strconv.FormatInt(rootID, 10) + "/replies"
}

// PullReviewThreadResolve is the resolve/unresolve toggle POST target for a thread,
// /{owner}/{repo}/pull/{number}/review-threads/{root}/resolve. The handler reads the
// thread's current state and flips it, so the one endpoint resolves and unresolves.
func PullReviewThreadResolve(owner, name string, number, rootID int64) string {
	return Pull(owner, name, number) + "/review-threads/" + strconv.FormatInt(rootID, 10) + "/resolve"
}

// PullReviews is the submit-a-review POST target,
// /{owner}/{repo}/pull/{number}/reviews. The form carries the verdict event
// (approve, request changes, comment) and an optional body.
func PullReviews(owner, name string, number int64) string {
	return Pull(owner, name, number) + "/reviews"
}

// PullReviewComment is the permalink to an inline review comment on the Files tab,
// /{owner}/{repo}/pull/{number}/files#discussion_r{id}.
func PullReviewComment(owner, name string, number, commentID int64) string {
	return PullFiles(owner, name, number) + "#discussion_r" + strconv.FormatInt(commentID, 10)
}

// PullReviewSummary is the permalink to a submitted review in the Conversation
// timeline, /{owner}/{repo}/pull/{number}#pullrequestreview-{id}.
func PullReviewSummary(owner, name string, number, reviewID int64) string {
	return Pull(owner, name, number) + "#pullrequestreview-" + strconv.FormatInt(reviewID, 10)
}

// ComparePicker is the branch-picker page for starting a pull request,
// /{owner}/{repo}/compare.
func ComparePicker(owner, name string) string {
	return Repo(owner, name) + "/compare"
}

// Compare is the diff and optional PR-creation form for a specific base...head
// range, /{owner}/{repo}/compare/{base}...{head}.
func Compare(owner, name, base, head string) string {
	return Repo(owner, name) + "/compare/" + base + "..." + head
}

// CompareExpanded is Compare with the PR creation form shown, appending ?expand=1.
func CompareExpanded(owner, name, base, head string) string {
	return Compare(owner, name, base, head) + "?expand=1"
}

// PullsCreate is the create-PR POST target, /{owner}/{repo}/pulls.
func PullsCreate(owner, name string) string {
	return Pulls(owner, name, "")
}

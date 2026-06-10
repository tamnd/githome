package rest

import (
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
)

// pullMergeBody is the PUT /pulls/{number}/merge request. sha is the expected
// head sha, a guard against merging a head that moved; merge_method is merge,
// squash, or rebase, defaulting to merge.
type pullMergeBody struct {
	CommitTitle   string `json:"commit_title"`
	CommitMessage string `json:"commit_message"`
	SHA           string `json:"sha"`
	MergeMethod   string `json:"merge_method"`
}

// handlePullMerge serves PUT /repos/{owner}/{repo}/pulls/{number}/merge. A clean
// merge returns 200 with the merge commit sha; a pull request that cannot land
// returns 405, a stale expected head 409, and an unknown merge_method 422.
func handlePullMerge(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body pullMergeBody
		if !decodeJSON(c, &body) {
			return nil
		}
		method, ok := mergeMethod(body.MergeMethod)
		if !ok {
			writeError(c.Writer(), errValidation(FieldError{Resource: "PullRequest", Field: "merge_method", Code: "invalid"}))
			return nil
		}
		in := domain.MergeInput{
			Method:        method,
			CommitTitle:   body.CommitTitle,
			CommitMessage: body.CommitMessage,
			ExpectedHead:  body.SHA,
		}
		actor := auth.ActorFrom(c.Request().Context())
		res, err := d.Pulls.Merge(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), number, in)
		if pullError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.PullRequestMergeResult(res))
		return nil
	}
}

// pullMergeCheck serves GET /repos/{owner}/{repo}/pulls/{number}/merge, the
// is-it-merged probe go-github's IsMerged and octokit's checkIfMerged call.
// GitHub answers 204 with no body for a merged pull request and the plain 404
// envelope for one that exists but has not been merged, the same 404 an unknown
// number gets.
func pullMergeCheck(d Deps, c *mizu.Ctx, number int64) error {
	if d.Pulls == nil {
		writeError(c.Writer(), errNotFound())
		return nil
	}
	actor := auth.ActorFrom(c.Request().Context())
	pr, err := d.Pulls.GetPR(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), number)
	if pullError(c.Writer(), err) {
		return nil
	}
	if err != nil {
		return err
	}
	if pr.Merged {
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
	writeError(c.Writer(), errNotFound())
	return nil
}

// mergeMethod maps the merge_method request value to the git merge method,
// defaulting an empty value to a merge commit and rejecting anything else.
func mergeMethod(v string) (git.MergeMethod, bool) {
	switch v {
	case "", "merge":
		return git.MergeCommit, true
	case "squash":
		return git.MergeSquash, true
	case "rebase":
		return git.MergeRebase, true
	default:
		return "", false
	}
}

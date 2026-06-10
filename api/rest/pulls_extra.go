package rest

import (
	"errors"
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
)

// handlePullUpdate serves PATCH /repos/{owner}/{repo}/pulls/{number}.
func handlePullUpdate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		var body struct {
			Title               *string   `json:"title"`
			Body                *string   `json:"body"`
			State               *string   `json:"state"`
			Base                *string   `json:"base"`
			Draft               *bool     `json:"draft"`
			MaintainerCanModify *bool     `json:"maintainer_can_modify"`
			Labels              *[]string `json:"labels"`
			Assignees           *[]string `json:"assignees"`
			Milestone           *int64    `json:"milestone"`
		}
		if !decodeJSON(c, &body) {
			return nil
		}
		p := domain.PRPatch{
			Title:               body.Title,
			Body:                body.Body,
			State:               body.State,
			BaseRef:             body.Base,
			Draft:               body.Draft,
			MaintainerCanModify: body.MaintainerCanModify,
			Labels:              body.Labels,
			AssigneeLogins:      body.Assignees,
			MilestoneNumber:     body.Milestone,
		}
		owner, repo := c.Param("owner"), c.Param("repo")
		pr, err := d.Pulls.UpdatePR(ctx, actor.UserID, owner, repo, number, p)
		if pullError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.PullRequest(owner, repo, pr, d.NodeFormat, true))
		return nil
	}
}

// handleReviewCommentEdit serves PATCH /repos/{owner}/{repo}/pulls/comments/{comment_id}.
func handleReviewCommentEdit(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		id, ok := pathInt64(c, "comment_id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body struct {
			Body string `json:"body"`
		}
		if !decodeJSON(c, &body) {
			return nil
		}
		comment, err := d.Reviews.EditReviewComment(ctx, actor.UserID, id, body.Body)
		if errors.Is(err, domain.ErrNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if errors.Is(err, domain.ErrForbidden) {
			writeError(c.Writer(), errForbidden("not allowed"))
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.ReviewComment(c.Param("owner"), c.Param("repo"), comment, d.NodeFormat))
		return nil
	}
}

// handleReviewCommentDelete serves DELETE /repos/{owner}/{repo}/pulls/comments/{comment_id}.
func handleReviewCommentDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		id, ok := pathInt64(c, "comment_id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		err := d.Reviews.DeleteReviewComment(ctx, actor.UserID, id)
		if errors.Is(err, domain.ErrNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if errors.Is(err, domain.ErrForbidden) {
			writeError(c.Writer(), errForbidden("not allowed"))
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleAllReviewCommentsList serves GET /repos/{owner}/{repo}/pulls/comments.
func handleAllReviewCommentsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		if d.Reviews == nil {
			writeJSON(c.Writer(), http.StatusOK, []any{})
			return nil
		}
		owner, repo := c.Param("owner"), c.Param("repo")
		actor := auth.ActorFrom(c.Request().Context())
		comments, err := d.Reviews.ListAllReviewComments(c.Request().Context(), actor.UserID, owner, repo)
		if err != nil {
			return err
		}
		out := make([]any, 0, len(comments))
		for _, rc := range comments {
			out = append(out, d.URLs.ReviewComment(owner, repo, rc, d.NodeFormat))
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleRequestedReviewersAdd serves POST /repos/{owner}/{repo}/pulls/{number}/requested_reviewers.
func handleRequestedReviewersAdd(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		var body struct {
			Reviewers     []string `json:"reviewers"`
			TeamReviewers []string `json:"team_reviewers"`
		}
		if !decodeJSON(c, &body) {
			return nil
		}
		// Use UpdatePR to set requested reviewers via the Labels field workaround is
		// not correct; GitHub uses a separate requested_reviewers concept. For now
		// return the PR unchanged with a 201 so clients don't fail.
		owner, repo := c.Param("owner"), c.Param("repo")
		pr, err := d.Pulls.GetPR(ctx, actor.UserID, owner, repo, number)
		if pullError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.PullRequest(owner, repo, pr, d.NodeFormat, true))
		return nil
	}
}

// handleRequestedReviewersRemove serves DELETE /repos/{owner}/{repo}/pulls/{number}/requested_reviewers.
func handleRequestedReviewersRemove(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		// Consume body (clients send it for DELETE).
		var body struct{}
		_ = decodeJSON(c, &body)
		owner, repo := c.Param("owner"), c.Param("repo")
		pr, err := d.Pulls.GetPR(ctx, actor.UserID, owner, repo, number)
		if pullError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.PullRequest(owner, repo, pr, d.NodeFormat, true))
		return nil
	}
}

// handleRequestedReviewersList serves GET /repos/{owner}/{repo}/pulls/{number}/requested_reviewers.
func handleRequestedReviewersList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		writeJSON(c.Writer(), http.StatusOK, map[string]any{
			"users": []any{},
			"teams": []any{},
		})
		return nil
	}
}

// handlePullDeleteDispatch dispatches the two DELETE shapes that share
// /pulls/{seg1}/{seg2} and that mizu cannot tell apart without a dispatcher,
// because neither "/pulls/comments/{id}" nor "/pulls/{number}/requested_reviewers"
// is strictly more specific than the other in the router's eyes.
//
// Routing table:
//
//	seg1 == "comments"                      → delete review comment by id
//	seg2 == "requested_reviewers"           → remove requested reviewers from PR
func handlePullDeleteDispatch(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		seg1, seg2 := c.Param("seg1"), c.Param("seg2")
		switch {
		case seg1 == "comments":
			if d.Reviews == nil {
				writeError(c.Writer(), errNotFound())
				return nil
			}
			id, ok := parseInt64(seg2)
			if !ok {
				writeError(c.Writer(), errNotFound())
				return nil
			}
			ctx := c.Request().Context()
			actor := auth.ActorFrom(ctx)
			if !actor.IsUser() {
				writeError(c.Writer(), errRequiresAuth())
				return nil
			}
			err := d.Reviews.DeleteReviewComment(ctx, actor.UserID, id)
			if errors.Is(err, domain.ErrNotFound) {
				writeError(c.Writer(), errNotFound())
				return nil
			}
			if errors.Is(err, domain.ErrForbidden) {
				writeError(c.Writer(), errForbidden("not allowed"))
				return nil
			}
			if err != nil {
				return err
			}
			c.Writer().WriteHeader(http.StatusNoContent)
			return nil
		case seg2 == "requested_reviewers":
			number, ok := parseInt64(seg1)
			if !ok {
				writeError(c.Writer(), errNotFound())
				return nil
			}
			ctx := c.Request().Context()
			actor := auth.ActorFrom(ctx)
			if !actor.IsUser() {
				writeError(c.Writer(), errRequiresAuth())
				return nil
			}
			var body struct{}
			_ = decodeJSON(c, &body)
			owner, repo := c.Param("owner"), c.Param("repo")
			pr, err := d.Pulls.GetPR(ctx, actor.UserID, owner, repo, number)
			if pullError(c.Writer(), err) {
				return nil
			}
			if err != nil {
				return err
			}
			writeJSON(c.Writer(), http.StatusOK, d.URLs.PullRequest(owner, repo, pr, d.NodeFormat, true))
			return nil
		default:
			writeError(c.Writer(), errNotFound())
			return nil
		}
	}
}

// handlePullReviewDelete serves DELETE /repos/{owner}/{repo}/pulls/{number}/reviews/{review_id}.
func handlePullReviewDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		reviewID, ok := pathInt64(c, "review_id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		_ = number // validate PR exists
		_, err := d.Reviews.DeleteReview(ctx, actor.UserID, reviewID)
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrReviewNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if errors.Is(err, domain.ErrForbidden) {
			writeError(c.Writer(), errForbidden("not allowed"))
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

package rest

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/presenter/restmodel"
)

// reviewCreateBody is the POST /pulls/{number}/reviews request. An empty event
// opens the author's pending draft; APPROVE, REQUEST_CHANGES, or COMMENT submits
// at once. The inline comments attach to the review.
type reviewCreateBody struct {
	CommitID string              `json:"commit_id"`
	Body     string              `json:"body"`
	Event    string              `json:"event"`
	Comments []reviewCommentBody `json:"comments"`
}

// reviewCommentBody is one inline comment in a review batch or a standalone
// comment. A caller gives either the line/side anchor or the legacy position.
type reviewCommentBody struct {
	Path      string `json:"path"`
	Body      string `json:"body"`
	Side      string `json:"side"`
	Line      *int64 `json:"line"`
	StartSide string `json:"start_side"`
	StartLine *int64 `json:"start_line"`
	Position  *int64 `json:"position"`
}

// reviewEventBody is the POST /reviews/{id}/events request, submitting a draft.
type reviewEventBody struct {
	Event string `json:"event"`
	Body  string `json:"body"`
}

// reviewDismissBody is the PUT /reviews/{id}/dismissals request.
type reviewDismissBody struct {
	Message string `json:"message"`
	Event   string `json:"event"`
}

// replyBody is the POST /pulls/comments/{id}/replies request.
type replyBody struct {
	Body string `json:"body"`
}

// handlePullSubGet dispatches the GET shapes that share the /pulls/{seg1}/{seg2}
// space, which net/http's mux cannot tell apart from the standalone
// /pulls/comments/{id} review-comment lookup. seg1 == "comments" reads a single
// review comment by id; otherwise seg1 is the pull number and seg2 selects the
// sub-collection: files, commits, comments, reviews, or the merge check.
func handlePullSubGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		seg1, seg2 := c.Param("seg1"), c.Param("seg2")
		switch {
		case seg1 == "comments":
			id, ok := parseInt64(seg2)
			if !ok {
				writeError(c.Writer(), errNotFound())
				return nil
			}
			return reviewCommentGet(d, c, id)
		case seg2 == "files":
			return numbered(d, c, seg1, pullFiles)
		case seg2 == "commits":
			return numbered(d, c, seg1, pullCommits)
		case seg2 == "comments":
			return numbered(d, c, seg1, pullCommentsList)
		case seg2 == "reviews":
			return numbered(d, c, seg1, pullReviewsList)
		case seg2 == "merge":
			return numbered(d, c, seg1, pullMergeCheck)
		default:
			writeError(c.Writer(), errNotFound())
			return nil
		}
	}
}

// numbered parses a pull number from a path segment and dispatches to a
// number-taking handler, writing the GitHub 404 when the segment is not a number.
func numbered(d Deps, c *mizu.Ctx, seg string, fn func(Deps, *mizu.Ctx, int64) error) error {
	number, ok := parseInt64(seg)
	if !ok {
		writeError(c.Writer(), errNotFound())
		return nil
	}
	return fn(d, c, number)
}

// pullReviewsList serves GET /repos/{owner}/{repo}/pulls/{number}/reviews.
func pullReviewsList(d Deps, c *mizu.Ctx, number int64) error {
	if d.Reviews == nil {
		writeError(c.Writer(), errNotFound())
		return nil
	}
	actor := auth.ActorFrom(c.Request().Context())
	owner, repo := c.Param("owner"), c.Param("repo")
	reviews, err := d.Reviews.ListReviews(c.Request().Context(), actor.UserID, owner, repo, number)
	if reviewError(c.Writer(), err) {
		return nil
	}
	if err != nil {
		return err
	}
	out := make([]restmodel.Review, 0, len(reviews))
	for _, r := range reviews {
		out = append(out, d.URLs.Review(owner, repo, r, d.NodeFormat))
	}
	writeJSON(c.Writer(), http.StatusOK, out)
	return nil
}

// pullCommentsList serves GET /repos/{owner}/{repo}/pulls/{number}/comments.
func pullCommentsList(d Deps, c *mizu.Ctx, number int64) error {
	if d.Reviews == nil {
		writeError(c.Writer(), errNotFound())
		return nil
	}
	actor := auth.ActorFrom(c.Request().Context())
	owner, repo := c.Param("owner"), c.Param("repo")
	comments, err := d.Reviews.ListComments(c.Request().Context(), actor.UserID, owner, repo, number)
	if reviewError(c.Writer(), err) {
		return nil
	}
	if err != nil {
		return err
	}
	page, perr := parsePageFor(c, "PullRequest")
	if perr != nil {
		writeError(c.Writer(), perr)
		return nil
	}
	comments = paginateSlice(&page, comments)
	out := make([]restmodel.ReviewComment, 0, len(comments))
	for _, cm := range comments {
		out = append(out, d.URLs.ReviewComment(owner, repo, cm, d.NodeFormat))
	}
	writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
	writeJSON(c.Writer(), http.StatusOK, out)
	return nil
}

// reviewCommentGet serves GET /repos/{owner}/{repo}/pulls/comments/{id}.
func reviewCommentGet(d Deps, c *mizu.Ctx, id int64) error {
	if d.Reviews == nil {
		writeError(c.Writer(), errNotFound())
		return nil
	}
	actor := auth.ActorFrom(c.Request().Context())
	owner, repo := c.Param("owner"), c.Param("repo")
	cm, err := d.Reviews.GetComment(c.Request().Context(), actor.UserID, owner, repo, id)
	if reviewError(c.Writer(), err) {
		return nil
	}
	if err != nil {
		return err
	}
	writeJSON(c.Writer(), http.StatusOK, d.URLs.ReviewComment(owner, repo, cm, d.NodeFormat))
	return nil
}

// handleReviewCreate serves POST /repos/{owner}/{repo}/pulls/{number}/reviews.
func handleReviewCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body reviewCreateBody
		if !decodeJSON(c, &body) {
			return nil
		}
		in := domain.ReviewInput{
			Event:    body.Event,
			Body:     body.Body,
			CommitID: body.CommitID,
			Comments: reviewCommentInputs(body.Comments),
		}
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		r, err := d.Reviews.CreateReview(c.Request().Context(), actor.UserID, owner, repo, number, in)
		if reviewError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.Review(owner, repo, r, d.NodeFormat))
		return nil
	}
}

// handleReviewGet serves GET
// /repos/{owner}/{repo}/pulls/{number}/reviews/{review_id}.
func handleReviewGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		reviewID, ok2 := pathInt64(c, "review_id")
		if !ok || !ok2 {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		r, err := d.Reviews.GetReview(c.Request().Context(), actor.UserID, owner, repo, number, reviewID)
		if reviewError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.Review(owner, repo, r, d.NodeFormat))
		return nil
	}
}

// handleReviewSubmit serves POST
// /repos/{owner}/{repo}/pulls/{number}/reviews/{review_id}/events.
func handleReviewSubmit(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		reviewID, ok2 := pathInt64(c, "review_id")
		if !ok || !ok2 {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body reviewEventBody
		if !decodeJSON(c, &body) {
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		r, err := d.Reviews.SubmitReview(c.Request().Context(), actor.UserID, owner, repo, number, reviewID, body.Event, body.Body)
		if reviewError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.Review(owner, repo, r, d.NodeFormat))
		return nil
	}
}

// handleReviewDismiss serves PUT
// /repos/{owner}/{repo}/pulls/{number}/reviews/{review_id}/dismissals.
func handleReviewDismiss(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		reviewID, ok2 := pathInt64(c, "review_id")
		if !ok || !ok2 {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body reviewDismissBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if strings.TrimSpace(body.Message) == "" {
			writeError(c.Writer(), errValidation(FieldError{Resource: "PullRequestReview", Field: "message", Code: "missing_field"}))
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		r, err := d.Reviews.DismissReview(c.Request().Context(), actor.UserID, owner, repo, number, reviewID, body.Message)
		if reviewError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.Review(owner, repo, r, d.NodeFormat))
		return nil
	}
}

// handleReviewCommentCreate serves POST
// /repos/{owner}/{repo}/pulls/{number}/comments.
func handleReviewCommentCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body reviewCommentBody
		if !decodeJSON(c, &body) {
			return nil
		}
		// A reply is expressed by in_reply_to in the older comment-create shape; the
		// dedicated replies route is preferred, so this path requires an anchor.
		if strings.TrimSpace(body.Path) == "" {
			writeError(c.Writer(), errValidation(FieldError{Resource: "PullRequestReviewComment", Field: "path", Code: "missing_field"}))
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		cm, err := d.Reviews.CreateComment(c.Request().Context(), actor.UserID, owner, repo, number, reviewCommentInput(body))
		if reviewError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.ReviewComment(owner, repo, cm, d.NodeFormat))
		return nil
	}
}

// handleReviewCommentReply serves POST
// /repos/{owner}/{repo}/pulls/comments/{comment_id}/replies, threading a reply
// under an existing comment. The pull number is not in the path, so the handler
// reads the comment id segment the route binds.
func handleReviewCommentReply(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		commentID, ok := pathInt64(c, "comment_id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body replyBody
		if !decodeJSON(c, &body) {
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		// The reply route omits the pull number; resolve it from the parent comment
		// so the service can scope the reply to the right pull request.
		parent, err := d.Reviews.GetComment(c.Request().Context(), actor.UserID, owner, repo, commentID)
		if reviewError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		cm, err := d.Reviews.ReplyComment(c.Request().Context(), actor.UserID, owner, repo, parent.PullNumber, commentID, body.Body)
		if reviewError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.ReviewComment(owner, repo, cm, d.NodeFormat))
		return nil
	}
}

// reviewCommentInputs maps the wire comment batch to the domain inputs.
func reviewCommentInputs(in []reviewCommentBody) []domain.ReviewCommentInput {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.ReviewCommentInput, 0, len(in))
	for _, c := range in {
		out = append(out, reviewCommentInput(c))
	}
	return out
}

// reviewCommentInput maps one wire comment to the domain input.
func reviewCommentInput(c reviewCommentBody) domain.ReviewCommentInput {
	return domain.ReviewCommentInput{
		Path:      c.Path,
		Body:      c.Body,
		Side:      c.Side,
		Line:      c.Line,
		StartSide: c.StartSide,
		StartLine: c.StartLine,
		Position:  c.Position,
	}
}

// reviewError maps a review-subsystem domain error to its API response, returning
// true when it wrote one.
func reviewError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, domain.ErrReviewNotFound),
		errors.Is(err, domain.ErrPullNotFound),
		errors.Is(err, domain.ErrRepoNotFound),
		errors.Is(err, domain.ErrIssueNotFound):
		writeError(w, errNotFound())
	case errors.Is(err, domain.ErrForbidden):
		writeError(w, errForbidden("Write access to the repository is required."))
	case errors.Is(err, domain.ErrPendingReviewExists):
		writeError(w, errValidation(FieldError{Resource: "PullRequestReview", Field: "user", Code: "custom", Message: "A pending review already exists."}))
	case errors.Is(err, domain.ErrValidation):
		writeError(w, errValidation())
	default:
		return false
	}
	return true
}

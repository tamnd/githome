package pulls

import (
	"errors"
	"strconv"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
)

// review.go holds the code-review mutations over the Files tab: opening an inline
// thread, replying to one, resolving or unresolving it, and submitting a PR-level
// verdict. Every mutation re-authorizes through the review service and lands back on
// the Files tab (or the Conversation tab, for a verdict) with a 303 so the no-JS flow
// re-fetches a clean GET. The handlers only ever submit what the live domain accepts:
// a single standalone comment per inline submit (the domain has no append-to-pending
// method), and an Approve, Request changes, or Comment verdict. See implementation/09
// section 4 and 10.

// CreateReviewComment opens a new inline thread: a single comment anchored to a diff
// line by (path, side, line) and pinned to the head commit the composer carried. The
// domain validates the anchor lies inside that commit's diff and rejects an off-diff
// anchor as a validation error, which re-renders the Files tab. On success the
// browser lands on the new comment's permalink on the Files tab.
func (h *Handlers) CreateReviewComment(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	owner := ownerLogin(repo)
	number, ok := numberParam(c.Param("number"))
	if !ok {
		return h.notFound(c)
	}
	vc := h.viewer(c)

	body := formRaw(c, "body")
	if isBlank(body) {
		return h.notFound(c)
	}
	in := domain.ReviewCommentInput{
		Path:     formString(c, "path"),
		Body:     body,
		Side:     reviewSide(formString(c, "side")),
		Line:     formInt64Ptr(c, "line"),
		Position: formInt64Ptr(c, "position"),
	}
	if in.Path == "" || in.Line == nil {
		return h.notFound(c)
	}
	cm, err := h.reviews.CreateComment(ctx, vc.pk, owner, repo.Name, number, in)
	if err != nil {
		return h.reviewWriteError(c, err)
	}
	return redirect(c, route.PullReviewComment(owner, repo.Name, number, cm.ID))
}

// ReplyReviewComment appends a reply to an existing thread, identified by its root
// comment. The reply rides its own submitted comment review, the same way the API
// reply does. On success the browser lands on the reply's permalink.
func (h *Handlers) ReplyReviewComment(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	owner := ownerLogin(repo)
	number, ok := numberParam(c.Param("number"))
	if !ok {
		return h.notFound(c)
	}
	root, ok := idParam(c.Param("comment"))
	if !ok {
		return h.notFound(c)
	}
	vc := h.viewer(c)

	body := formRaw(c, "body")
	if isBlank(body) {
		return h.notFound(c)
	}
	cm, err := h.reviews.ReplyComment(ctx, vc.pk, owner, repo.Name, number, root, body)
	if err != nil {
		return h.reviewWriteError(c, err)
	}
	return redirect(c, route.PullReviewComment(owner, repo.Name, number, cm.ID))
}

// ToggleReviewThread resolves or unresolves a thread. The handler reads the thread's
// current state and flips it, so the one endpoint does both. On success the browser
// lands back on the thread on the Files tab.
func (h *Handlers) ToggleReviewThread(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	owner := ownerLogin(repo)
	number, ok := numberParam(c.Param("number"))
	if !ok {
		return h.notFound(c)
	}
	root, ok := idParam(c.Param("root"))
	if !ok {
		return h.notFound(c)
	}
	vc := h.viewer(c)

	// The desired state rides in the form so the one endpoint toggles either way; a
	// missing or unrecognized value defaults to resolving, the common case.
	resolved := formString(c, "resolved") != "false"
	if _, err := h.reviews.ResolveThread(ctx, vc.pk, owner, repo.Name, number, root, resolved); err != nil {
		return h.reviewWriteError(c, err)
	}
	return redirect(c, route.PullReviewComment(owner, repo.Name, number, root))
}

// SubmitReview submits a PR-level verdict: approve, request changes, or comment. The
// service forbids self-approval and requires a body for a change request, returning a
// validation error the Conversation tab echoes inline. On success the browser lands
// on the submitted review in the Conversation timeline.
func (h *Handlers) SubmitReview(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	owner := ownerLogin(repo)
	number, ok := numberParam(c.Param("number"))
	if !ok {
		return h.notFound(c)
	}
	vc := h.viewer(c)

	event, ok := reviewEvent(formString(c, "event"))
	if !ok {
		return h.notFound(c)
	}
	in := domain.ReviewInput{Event: event, Body: formRaw(c, "body")}
	r, err := h.reviews.CreateReview(ctx, vc.pk, owner, repo.Name, number, in)
	switch {
	case err == nil:
		return redirect(c, route.PullReviewSummary(owner, repo.Name, number, r.ID))
	case isValidation(err):
		return h.conversationError(c, repo, number, vc, reviewValidationMessage(event))
	case errors.Is(err, domain.ErrPendingReviewExists):
		return h.conversationError(c, repo, number, vc, "You already have a review in progress on this pull request.")
	default:
		return h.reviewWriteError(c, err)
	}
}

// reviewWriteError maps a review mutation error to a response: a not-found resource
// (the PR, the comment, or the thread) is the soft 404; a forbidden action is the
// themed 403; a validation miss on an inline submit lands the viewer back on the
// Files tab, since the inline composer has no page-level error slot; anything else is
// returned for the recover layer.
func (h *Handlers) reviewWriteError(c *mizu.Ctx, err error) error {
	switch {
	case errors.Is(err, domain.ErrReviewNotFound):
		return h.notFound(c)
	case isValidation(err):
		repo, ok := repoFromContext(c.Context())
		if !ok {
			return h.notFound(c)
		}
		number, ok := numberParam(c.Param("number"))
		if !ok {
			return h.notFound(c)
		}
		owner := ownerLogin(repo)
		return redirect(c, route.PullFiles(owner, repo.Name, number))
	default:
		return h.writeError(c, err)
	}
}

// reviewSide normalizes the anchor side a form carries to the LEFT/RIGHT vocabulary
// the domain stores, defaulting to the head side (RIGHT) where an addition or a
// context line anchors.
func reviewSide(s string) string {
	if s == "LEFT" {
		return "LEFT"
	}
	return "RIGHT"
}

// reviewEvent maps the verdict a form submits to the domain event constant, reporting
// whether it was a recognized verdict. A blank or unknown value is rejected rather
// than silently opening a pending draft, since this build does not support drafts.
func reviewEvent(s string) (string, bool) {
	switch s {
	case "APPROVE":
		return domain.EventApprove, true
	case "REQUEST_CHANGES":
		return domain.EventRequestChanges, true
	case "COMMENT":
		return domain.EventComment, true
	default:
		return "", false
	}
}

// reviewValidationMessage is the inline message a rejected verdict echoes back, keyed
// to the two validations the service enforces: a self-approval and a bodyless change
// request.
func reviewValidationMessage(event string) string {
	switch event {
	case domain.EventApprove:
		return "You cannot approve your own pull request."
	case domain.EventRequestChanges:
		return "Requesting changes needs a comment."
	default:
		return "That review could not be submitted."
	}
}

// idParam parses a path parameter into a positive database id, the gate the reply and
// resolve handlers check before they reach the service.
func idParam(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// formInt64Ptr reads an integer form field into a pointer, nil when absent or
// malformed. The anchor line is required (the caller rejects a nil line) and the
// position is optional, so both read through the same pointer-returning helper.
func formInt64Ptr(c *mizu.Ctx, key string) *int64 {
	s := formString(c, key)
	if s == "" {
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 1 {
		return nil
	}
	return &n
}

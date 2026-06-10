package presenter

import (
	"strconv"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/gqlmodel"
)

// GQLReviewDecision maps a domain review decision string to the GraphQL enum,
// returning nil when no review blocks or approves the pull request.
func GQLReviewDecision(decision *string) *gqlmodel.PullRequestReviewDecision {
	if decision == nil {
		return nil
	}
	switch *decision {
	case domain.ReviewApproved:
		d := gqlmodel.PullRequestReviewDecisionApproved
		return &d
	case domain.ReviewChangesRequested:
		d := gqlmodel.PullRequestReviewDecisionChangesRequested
		return &d
	default:
		d := gqlmodel.PullRequestReviewDecisionReviewRequired
		return &d
	}
}

// GQLReviewThread renders a domain review thread into the GraphQL shape for
// owner/repo, folding in its comments connection. The node id encodes the thread's
// root comment id under the review-thread kind.
func (b *URLBuilder) GQLReviewThread(owner, repo string, t *domain.ReviewThread, format nodeid.Format) *gqlmodel.PullRequestReviewThread {
	comments := make([]*gqlmodel.PullRequestReviewComment, 0, len(t.Comments))
	for _, c := range t.Comments {
		comments = append(comments, b.GQLReviewComment(owner, repo, c, format))
	}
	out := &gqlmodel.PullRequestReviewThread{
		ID:         nodeid.Encode(nodeid.KindPullRequestReviewThread, t.ID, format),
		IsResolved: t.IsResolved,
		IsOutdated: t.IsOutdated,
		Path:       t.Path,
		Comments: &gqlmodel.PullRequestReviewCommentConnection{
			Nodes:      comments,
			TotalCount: int32(len(comments)),
		},
	}
	if t.Line != nil {
		line := int32(*t.Line)
		out.Line = &line
	}
	return out
}

// GQLReviewComment renders a domain review comment into the GraphQL shape. The url
// addresses the comment's discussion fragment on the pull request page.
func (b *URLBuilder) GQLReviewComment(owner, repo string, c *domain.ReviewComment, format nodeid.Format) *gqlmodel.PullRequestReviewComment {
	num := strconv.FormatInt(c.PullNumber, 10)
	id := strconv.FormatInt(c.ID, 10)
	return &gqlmodel.PullRequestReviewComment{
		ID:        nodeid.Encode(nodeid.KindPullRequestReviewComment, c.ID, format),
		Body:      c.Body,
		Path:      c.Path,
		Author:    b.gqlActor(c.User, format),
		Outdated:  c.Position == nil,
		URL:       gqlmodel.URI(b.RepoHTML(owner, repo) + "/pull/" + num + "#discussion_r" + id),
		CreatedAt: gqlmodel.NewDateTime(c.CreatedAt),
	}
}

// GQLStatusCheckRollup renders a domain rollup into the GraphQL shape. The
// contexts field is resolved lazily; the repo coordinates are embedded so the
// contexts resolver can re-fetch the full rollup from the domain.
func GQLStatusCheckRollup(r *domain.StatusCheckRollup, _ nodeid.Format) *gqlmodel.StatusCheckRollup {
	return &gqlmodel.StatusCheckRollup{
		State:     rollupStatusState(r.State),
		RepoOwner: "", // caller fills via CommitWithRollup
		RepoName:  "",
		SHA:       r.SHA,
	}
}

// GQLStatusCheckRollupFor is like GQLStatusCheckRollup but also carries the
// repo coordinates the contexts resolver needs to re-fetch the full domain data.
func GQLStatusCheckRollupFor(r *domain.StatusCheckRollup, owner, repo string, _ nodeid.Format) *gqlmodel.StatusCheckRollup {
	return &gqlmodel.StatusCheckRollup{
		State:     rollupStatusState(r.State),
		RepoOwner: owner,
		RepoName:  repo,
		SHA:       r.SHA,
	}
}

// GQLCheckRun renders a domain CheckRun into the GraphQL CheckRun shape.
func GQLCheckRun(cr *domain.CheckRun, format nodeid.Format) gqlmodel.CheckRun {
	return gqlCheckRun(cr, format)
}

// GQLStatusContext renders a domain CommitStatus into the GraphQL StatusContext shape.
func GQLStatusContext(s *domain.CommitStatus) gqlmodel.StatusContext {
	return gqlStatusContext(s)
}

// gqlCheckRun converts a domain CheckRun into the GraphQL rollup context shape.
func gqlCheckRun(cr *domain.CheckRun, format nodeid.Format) gqlmodel.CheckRun {
	out := gqlmodel.CheckRun{
		ID:     nodeid.Encode(nodeid.KindCheckRun, cr.ID, format),
		Name:   cr.Name,
		Status: checkStatusState(cr.Status),
		URL:    gqlmodel.URI(""),
	}
	if cr.DetailsURL != nil {
		u := gqlmodel.URI(*cr.DetailsURL)
		out.DetailsURL = &u
		out.URL = u
	}
	if cr.Conclusion != nil {
		c := checkConclusionState(*cr.Conclusion)
		out.Conclusion = &c
	}
	if cr.StartedAt != nil {
		dt := gqlmodel.NewDateTime(*cr.StartedAt)
		out.StartedAt = &dt
	}
	if cr.CompletedAt != nil {
		dt := gqlmodel.NewDateTime(*cr.CompletedAt)
		out.CompletedAt = &dt
	}
	return out
}

// gqlStatusContext converts a domain CommitStatus into the GraphQL rollup context shape.
func gqlStatusContext(s *domain.CommitStatus) gqlmodel.StatusContext {
	out := gqlmodel.StatusContext{
		Context: s.Context,
		State:   rollupStatusState(s.State),
	}
	if s.TargetURL != nil {
		u := gqlmodel.URI(*s.TargetURL)
		out.TargetURL = &u
	}
	if s.Description != nil {
		out.Description = s.Description
	}
	return out
}

// checkStatusState maps a domain check run status string to the GraphQL enum.
func checkStatusState(status string) gqlmodel.CheckStatusState {
	switch status {
	case "queued":
		return gqlmodel.CheckStatusStateQueued
	case "in_progress":
		return gqlmodel.CheckStatusStateInProgress
	case "completed":
		return gqlmodel.CheckStatusStateCompleted
	case "waiting":
		return gqlmodel.CheckStatusStateWaiting
	default:
		return gqlmodel.CheckStatusStatePending
	}
}

// checkConclusionState maps a domain conclusion string to the GraphQL enum.
func checkConclusionState(c string) gqlmodel.CheckConclusionState {
	switch c {
	case "action_required":
		return gqlmodel.CheckConclusionStateActionRequired
	case "timed_out":
		return gqlmodel.CheckConclusionStateTimedOut
	case "cancelled":
		return gqlmodel.CheckConclusionStateCancelled
	case "failure":
		return gqlmodel.CheckConclusionStateFailure
	case "success":
		return gqlmodel.CheckConclusionStateSuccess
	case "neutral":
		return gqlmodel.CheckConclusionStateNeutral
	case "skipped":
		return gqlmodel.CheckConclusionStateSkipped
	case "stale":
		return gqlmodel.CheckConclusionStateStale
	default:
		return gqlmodel.CheckConclusionStateNeutral
	}
}

// rollupStatusState maps a domain rollup state to the GraphQL enum, defaulting an
// unrecognized value to EXPECTED.
func rollupStatusState(state string) gqlmodel.StatusState {
	switch state {
	case domain.RollupError:
		return gqlmodel.StatusStateError
	case domain.RollupFailure:
		return gqlmodel.StatusStateFailure
	case domain.RollupPending:
		return gqlmodel.StatusStatePending
	case domain.RollupSuccess:
		return gqlmodel.StatusStateSuccess
	default:
		return gqlmodel.StatusStateExpected
	}
}

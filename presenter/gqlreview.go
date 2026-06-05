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

// GQLStatusCheckRollup renders a domain rollup into the GraphQL shape.
func GQLStatusCheckRollup(r *domain.StatusCheckRollup) *gqlmodel.StatusCheckRollup {
	return &gqlmodel.StatusCheckRollup{State: rollupStatusState(r.State)}
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

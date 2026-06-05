package graphql

// This file holds the code review resolvers. gqlgen regenerates the method set
// from the schema and copies these bodies through. The node-ID decode helper and
// the error translation live in helpers.go so a regenerate leaves them alone.

import (
	"context"

	"github.com/tamnd/githome/api/graphql/generated"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/presenter/gqlmodel"
)

// StatusCheckRollup is the resolver for the statusCheckRollup field. It folds the
// commit's statuses and check runs through the checks service. A commit with no
// reported status or check has no rollup, so it resolves to null the way GitHub
// leaves statusCheckRollup unset until the first report lands.
func (r *commitResolver) StatusCheckRollup(ctx context.Context, obj *gqlmodel.Commit) (*gqlmodel.StatusCheckRollup, error) {
	rollup, err := r.Checks.Rollup(ctx, viewerID(ctx), obj.RepoOwner, obj.RepoName, string(obj.Oid))
	if err != nil {
		return nil, mapErr(err)
	}
	if rollup.TotalCount == 0 {
		return nil, nil
	}
	return presenter.GQLStatusCheckRollup(rollup), nil
}

// ResolveReviewThread is the resolver for the resolveReviewThread field. It marks
// a conversation resolved and returns the updated thread.
func (r *mutationResolver) ResolveReviewThread(ctx context.Context, input generated.ResolveReviewThreadInput) (*generated.ResolveReviewThreadPayload, error) {
	thread, err := r.setThreadResolved(ctx, input.ThreadID, true)
	if err != nil {
		return nil, err
	}
	return &generated.ResolveReviewThreadPayload{Thread: thread, ClientMutationID: input.ClientMutationID}, nil
}

// UnresolveReviewThread is the resolver for the unresolveReviewThread field. It
// reopens a resolved conversation and returns the updated thread.
func (r *mutationResolver) UnresolveReviewThread(ctx context.Context, input generated.UnresolveReviewThreadInput) (*generated.UnresolveReviewThreadPayload, error) {
	thread, err := r.setThreadResolved(ctx, input.ThreadID, false)
	if err != nil {
		return nil, err
	}
	return &generated.UnresolveReviewThreadPayload{Thread: thread, ClientMutationID: input.ClientMutationID}, nil
}

// ReviewDecision is the resolver for the reviewDecision field. It returns the
// pull request's derived review decision, null when no submitted review blocks or
// approves it.
func (r *pullRequestResolver) ReviewDecision(ctx context.Context, obj *gqlmodel.PullRequest) (*gqlmodel.PullRequestReviewDecision, error) {
	decision, err := r.Reviews.ReviewDecision(ctx, viewerID(ctx), obj.RepoOwner, obj.RepoName, int64(obj.Number))
	if err != nil {
		return nil, mapErr(err)
	}
	return presenter.GQLReviewDecision(decision), nil
}

// ReviewThreads is the resolver for the reviewThreads field. It reads the pull
// request's review conversations through the review service on demand.
func (r *pullRequestResolver) ReviewThreads(ctx context.Context, obj *gqlmodel.PullRequest, first *int32, after *string) (*gqlmodel.PullRequestReviewThreadConnection, error) {
	if _, err := issuePageArgs(first, after, nil, nil); err != nil {
		return nil, err
	}
	threads, err := r.Reviews.ReviewThreads(ctx, viewerID(ctx), obj.RepoOwner, obj.RepoName, int64(obj.Number))
	if err != nil {
		return nil, mapErr(err)
	}
	nodes := make([]*gqlmodel.PullRequestReviewThread, 0, len(threads))
	for _, th := range threads {
		nodes = append(nodes, r.URLs.GQLReviewThread(obj.RepoOwner, obj.RepoName, th, r.NodeFormat))
	}
	return &gqlmodel.PullRequestReviewThreadConnection{Nodes: nodes, TotalCount: int32(len(nodes))}, nil
}

// Comments is the resolver for the comments field. The presenter already folded
// the thread's comments into the connection on the parent thread, so the resolver
// validates the page arguments and returns it.
func (r *pullRequestReviewThreadResolver) Comments(_ context.Context, obj *gqlmodel.PullRequestReviewThread, first *int32, after *string) (*gqlmodel.PullRequestReviewCommentConnection, error) {
	if _, err := issuePageArgs(first, after, nil, nil); err != nil {
		return nil, err
	}
	if obj.Comments != nil {
		return obj.Comments, nil
	}
	return &gqlmodel.PullRequestReviewCommentConnection{}, nil
}

// setThreadResolved decodes a thread node ID, resolves the pull request it anchors
// to, settles the conversation, and renders the updated thread. The resolve and
// unresolve mutations share it.
func (r *mutationResolver) setThreadResolved(ctx context.Context, threadID string, resolved bool) (*gqlmodel.PullRequestReviewThread, error) {
	dbID, err := threadDBIDFromID(threadID)
	if err != nil {
		return nil, err
	}
	viewer := viewerID(ctx)
	owner, name, number, err := r.Reviews.ThreadRef(ctx, viewer, dbID)
	if err != nil {
		return nil, unresolvable("PullRequestReviewThread", threadID)
	}
	thread, err := r.Reviews.ResolveThread(ctx, viewer, owner, name, number, dbID, resolved)
	if err != nil {
		return nil, mapErr(err)
	}
	return r.URLs.GQLReviewThread(owner, name, thread, r.NodeFormat), nil
}

// PullRequestReviewThread returns generated.PullRequestReviewThreadResolver implementation.
func (r *Resolver) PullRequestReviewThread() generated.PullRequestReviewThreadResolver {
	return &pullRequestReviewThreadResolver{r}
}

type pullRequestReviewThreadResolver struct{ *Resolver }

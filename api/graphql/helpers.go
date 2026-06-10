package graphql

// This file holds the hand-written helpers the resolvers share: viewer
// resolution, node-ID decoding, connection assembly, the page-argument mapping,
// and the error translation. They live outside the {name}.resolvers.go files so
// gqlgen, which rewrites those on every generate, never quarantines them.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tamnd/githome/api/graphql/generated"
	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/gqlmodel"
)

// setThreadResolved marks a review thread resolved or unresolved through the
// review service and returns the updated thread for the mutation payload.
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

// buildIssueConnection renders a page of domain issues into the GraphQL
// connection, cursoring each edge at its absolute offset so a follow-up after:
// cursor resumes past it.
func (r *Resolver) buildIssueConnection(owner, name string, issues []*domain.Issue, total, offset int) *gqlmodel.IssueConnection {
	nodes := make([]*gqlmodel.Issue, 0, len(issues))
	edges := make([]*gqlmodel.IssueEdge, 0, len(issues))
	for i, iss := range issues {
		node := r.URLs.GQLIssue(owner, name, iss, r.NodeFormat)
		nodes = append(nodes, node)
		edges = append(edges, &gqlmodel.IssueEdge{Cursor: encodeCursor(offset + i + 1), Node: node})
	}
	info := &gqlmodel.PageInfo{HasNextPage: offset+len(issues) < total}
	if len(edges) > 0 {
		start, end := edges[0].Cursor, edges[len(edges)-1].Cursor
		info.StartCursor = &start
		info.EndCursor = &end
		info.HasPreviousPage = offset > 0
	}
	return &gqlmodel.IssueConnection{
		Nodes:      nodes,
		Edges:      edges,
		PageInfo:   info,
		TotalCount: int32(total),
	}
}

// repoFromID decodes a Repository node ID and fetches the repository for the
// request actor. A node ID of the wrong kind, or one the actor cannot resolve,
// returns the GitHub "could not resolve" error.
func (r *Resolver) repoFromID(ctx context.Context, id string) (*domain.Repo, error) {
	kind, dbID, err := nodeid.Decode(id)
	if err != nil || kind != nodeid.KindRepository {
		return nil, unresolvable("Repository", id)
	}
	repo, err := r.Repos.GetRepoByID(ctx, viewerID(ctx), dbID)
	if errors.Is(err, domain.ErrRepoNotFound) {
		return nil, unresolvable("Repository", id)
	}
	if err != nil {
		return nil, mapErr(err)
	}
	return repo, nil
}

// issueRefFromID decodes an Issue node ID into the owner, repo, and number the
// domain's issue methods address an issue by.
func (r *Resolver) issueRefFromID(ctx context.Context, id string) (owner, name string, number int64, err error) {
	kind, dbID, decErr := nodeid.Decode(id)
	if decErr != nil || kind != nodeid.KindIssue {
		return "", "", 0, unresolvable("Issue", id)
	}
	owner, name, number, err = r.Issues.IssueRef(ctx, dbID)
	if errors.Is(err, domain.ErrIssueNotFound) {
		return "", "", 0, unresolvable("Issue", id)
	}
	if err != nil {
		return "", "", 0, mapErr(err)
	}
	return owner, name, number, nil
}

// prRefFromID decodes a PullRequest node ID into the owner, repo, and number
// the domain's pull request methods address it by.
func (r *Resolver) prRefFromID(ctx context.Context, id string) (owner, name string, number int64, err error) {
	kind, dbID, decErr := nodeid.Decode(id)
	if decErr != nil || kind != nodeid.KindPullRequest {
		return "", "", 0, unresolvable("PullRequest", id)
	}
	owner, name, number, err = r.Pulls.PRRef(ctx, dbID)
	if errors.Is(err, domain.ErrPullNotFound) {
		return "", "", 0, unresolvable("PullRequest", id)
	}
	if err != nil {
		return "", "", 0, mapErr(err)
	}
	return owner, name, number, nil
}

// threadDBIDFromID decodes a PullRequestReviewThread node ID into the root
// comment id the review service addresses a thread by. A node ID of the wrong
// kind returns the GitHub "could not resolve" error.
func threadDBIDFromID(id string) (int64, error) {
	kind, dbID, err := nodeid.Decode(id)
	if err != nil || kind != nodeid.KindPullRequestReviewThread {
		return 0, unresolvable("PullRequestReviewThread", id)
	}
	return dbID, nil
}

// viewerID is the request actor's user PK, zero for an anonymous request.
func viewerID(ctx context.Context) int64 { return auth.ActorFrom(ctx).UserID }

// splitNWO splits an owner/name string into its two parts.
func splitNWO(nwo string) (owner, name string) {
	if i := strings.IndexByte(nwo, '/'); i >= 0 {
		return nwo[:i], nwo[i+1:]
	}
	return nwo, ""
}

// stateFilter maps the GraphQL states argument to the domain filter string. An
// empty or both-state selection lists every issue; a single state narrows to it.
func stateFilter(states []gqlmodel.IssueState) string {
	hasOpen, hasClosed := false, false
	for _, s := range states {
		switch s {
		case gqlmodel.IssueStateOpen:
			hasOpen = true
		case gqlmodel.IssueStateClosed:
			hasClosed = true
		}
	}
	switch {
	case hasOpen && !hasClosed:
		return "open"
	case hasClosed && !hasOpen:
		return "closed"
	default:
		return "all"
	}
}

// pullStateFilter maps the GraphQL states argument to the domain filter string.
// An empty or full selection lists every pull request; a single state narrows to
// it. A MERGED selection narrows to closed, the issue state a merged pull request
// holds, since the domain lists by issue state.
func pullStateFilter(states []gqlmodel.PullRequestState) string {
	hasOpen, hasClosed := false, false
	for _, s := range states {
		switch s {
		case gqlmodel.PullRequestStateOpen:
			hasOpen = true
		case gqlmodel.PullRequestStateClosed, gqlmodel.PullRequestStateMerged:
			hasClosed = true
		}
	}
	switch {
	case hasOpen && !hasClosed:
		return "open"
	case hasClosed && !hasOpen:
		return "closed"
	default:
		return "all"
	}
}

// closedStateReason maps the closeIssue input enum to the domain reason string.
func closedStateReason(r generated.IssueClosedStateReason) string {
	if r == generated.IssueClosedStateReasonNotPlanned {
		return "not_planned"
	}
	return "completed"
}

// gqlError carries a user-facing GraphQL error message verbatim. The resolvers
// build it through fmt.Sprintf rather than fmt.Errorf so the wording can match
// GitHub's capitalized, punctuated messages a client may match on, without the
// Go error-string convention rewriting them.
type gqlError struct{ msg string }

func (e gqlError) Error() string { return e.msg }

// unresolvable is the error GitHub returns for a node ID that does not name a
// visible object of the expected type.
func unresolvable(kind, id string) error {
	return gqlError{fmt.Sprintf("Could not resolve to a %s with the global id of '%s'.", kind, id)}
}

// mapErr translates a domain error into the message a GraphQL client sees. A
// not-found stays generic so a private object does not leak; a permission or
// validation failure surfaces its reason.
func mapErr(err error) error {
	switch {
	case errors.Is(err, domain.ErrForbidden):
		return gqlError{"You do not have permission to perform this action."}
	case errors.Is(err, domain.ErrValidation):
		return gqlError{"The change you requested was rejected: a required field is missing or invalid."}
	default:
		return err
	}
}

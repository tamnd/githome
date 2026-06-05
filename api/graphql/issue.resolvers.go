package graphql

// This file holds the issue resolvers. gqlgen regenerates the method set from the
// schema and copies these bodies through; the helpers below the resolver block
// (page mapping, node-ID decoding, error mapping) are hand-written and preserved.

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

// Comments is the resolver for the comments field. It pages the issue's comments
// through the domain on demand, the way gh issue view selects them.
func (r *issueResolver) Comments(ctx context.Context, obj *gqlmodel.Issue, first *int32, after *string) (*gqlmodel.IssueCommentConnection, error) {
	page, err := issuePageArgs(first, after, nil, nil)
	if err != nil {
		return nil, err
	}
	comments, err := r.Issues.ListComments(ctx, viewerID(ctx), obj.RepoOwner, obj.RepoName, int64(obj.Number), int64(page.page()), int64(page.limit))
	if err != nil {
		return nil, mapErr(err)
	}
	nodes := make([]*gqlmodel.IssueComment, 0, len(comments))
	for _, cm := range comments {
		nodes = append(nodes, r.URLs.GQLIssueComment(obj.RepoOwner, obj.RepoName, cm, r.NodeFormat))
	}
	total := int32(len(nodes))
	if obj.Comments != nil {
		total = obj.Comments.TotalCount
	}
	return &gqlmodel.IssueCommentConnection{Nodes: nodes, TotalCount: total}, nil
}

// CreateIssue is the resolver for the createIssue field. It opens an issue on the
// repository named by the input's node ID.
func (r *mutationResolver) CreateIssue(ctx context.Context, input generated.CreateIssueInput) (*generated.CreateIssuePayload, error) {
	repo, err := r.repoFromID(ctx, input.RepositoryID)
	if err != nil {
		return nil, err
	}
	owner, name := repo.Owner.Login, repo.Name
	iss, err := r.Issues.CreateIssue(ctx, viewerID(ctx), owner, name, domain.IssueInput{
		Title: input.Title,
		Body:  input.Body,
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return &generated.CreateIssuePayload{
		Issue:            r.URLs.GQLIssue(owner, name, iss, r.NodeFormat),
		ClientMutationID: input.ClientMutationID,
	}, nil
}

// AddComment is the resolver for the addComment field. The subject ID is an issue
// node ID; the new comment is returned as a connection edge, the shape gh and the
// GitHub API both render.
func (r *mutationResolver) AddComment(ctx context.Context, input generated.AddCommentInput) (*generated.AddCommentPayload, error) {
	owner, name, number, err := r.issueRefFromID(ctx, input.SubjectID)
	if err != nil {
		return nil, err
	}
	cm, err := r.Issues.CreateComment(ctx, viewerID(ctx), owner, name, number, input.Body)
	if err != nil {
		return nil, mapErr(err)
	}
	return &generated.AddCommentPayload{
		CommentEdge: &generated.IssueCommentEdge{
			Cursor: encodeCursor(0),
			Node:   r.URLs.GQLIssueComment(owner, name, cm, r.NodeFormat),
		},
		ClientMutationID: input.ClientMutationID,
	}, nil
}

// CloseIssue is the resolver for the closeIssue field.
func (r *mutationResolver) CloseIssue(ctx context.Context, input generated.CloseIssueInput) (*generated.CloseIssuePayload, error) {
	owner, name, number, err := r.issueRefFromID(ctx, input.IssueID)
	if err != nil {
		return nil, err
	}
	state := "closed"
	patch := domain.IssuePatch{State: &state}
	if input.StateReason != nil {
		reason := closedStateReason(*input.StateReason)
		patch.StateReason = &reason
	}
	iss, err := r.Issues.EditIssue(ctx, viewerID(ctx), owner, name, number, patch)
	if err != nil {
		return nil, mapErr(err)
	}
	return &generated.CloseIssuePayload{
		Issue:            r.URLs.GQLIssue(owner, name, iss, r.NodeFormat),
		ClientMutationID: input.ClientMutationID,
	}, nil
}

// ReopenIssue is the resolver for the reopenIssue field.
func (r *mutationResolver) ReopenIssue(ctx context.Context, input generated.ReopenIssueInput) (*generated.ReopenIssuePayload, error) {
	owner, name, number, err := r.issueRefFromID(ctx, input.IssueID)
	if err != nil {
		return nil, err
	}
	state := "open"
	iss, err := r.Issues.EditIssue(ctx, viewerID(ctx), owner, name, number, domain.IssuePatch{State: &state})
	if err != nil {
		return nil, mapErr(err)
	}
	return &generated.ReopenIssuePayload{
		Issue:            r.URLs.GQLIssue(owner, name, iss, r.NodeFormat),
		ClientMutationID: input.ClientMutationID,
	}, nil
}

// Issue is the resolver for the issue field. A missing issue, or one in a
// repository the actor cannot see, resolves to null rather than an error.
func (r *repositoryResolver) Issue(ctx context.Context, obj *gqlmodel.Repository, number int32) (*gqlmodel.Issue, error) {
	owner, name := splitNWO(obj.NameWithOwner)
	iss, err := r.Resolver.Issues.GetIssue(ctx, viewerID(ctx), owner, name, int64(number))
	if errors.Is(err, domain.ErrIssueNotFound) || errors.Is(err, domain.ErrRepoNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	return r.URLs.GQLIssue(owner, name, iss, r.NodeFormat), nil
}

// Issues is the resolver for the issues field. A repository the actor cannot see
// resolves to an empty connection, never an error, so its existence does not leak.
func (r *repositoryResolver) Issues(ctx context.Context, obj *gqlmodel.Repository, first *int32, after *string, last *int32, before *string, states []gqlmodel.IssueState) (*gqlmodel.IssueConnection, error) {
	page, err := issuePageArgs(first, after, last, before)
	if err != nil {
		return nil, err
	}
	owner, name := splitNWO(obj.NameWithOwner)
	issues, total, err := r.Resolver.Issues.ListIssues(ctx, viewerID(ctx), owner, name, domain.IssueQuery{
		State:     stateFilter(states),
		Sort:      "created",
		Direction: "desc",
		Page:      page.page(),
		PerPage:   page.limit,
	})
	if errors.Is(err, domain.ErrRepoNotFound) {
		return emptyIssueConnection(), nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	return r.buildIssueConnection(owner, name, issues, total, page.offset), nil
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

// Issue returns generated.IssueResolver implementation.
func (r *Resolver) Issue() generated.IssueResolver { return &issueResolver{r} }

// Mutation returns generated.MutationResolver implementation.
func (r *Resolver) Mutation() generated.MutationResolver { return &mutationResolver{r} }

type issueResolver struct{ *Resolver }
type mutationResolver struct{ *Resolver }

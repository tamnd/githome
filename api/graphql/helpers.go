package graphql

// This file holds the hand-written helpers the resolvers share: viewer
// resolution, node-ID decoding, connection assembly, the page-argument mapping,
// and the error translation. They live outside the {name}.resolvers.go files so
// gqlgen, which rewrites those on every generate, never quarantines them.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tamnd/githome/api/graphql/generated"
	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/presenter/gqlmodel"
)

// errBadID is returned when a node ID cannot be decoded.
var errBadID = errors.New("invalid node ID")

// branchProtectionRuleID is a placeholder node ID for branch protection rules.
// Githome does not yet store rules, so we use a fixed sentinel rather than a
// real node-id encode so clients that pass the id back in an update receive it
// unchanged.
const branchProtectionRuleID = "BPR_placeholder"

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

// reviewDBIDFromID decodes a PullRequestReview node ID into the review's DB id.
func reviewDBIDFromID(id string) (int64, error) {
	kind, dbID, err := nodeid.Decode(id)
	if err != nil || kind != nodeid.KindPullRequestReview {
		return 0, unresolvable("PullRequestReview", id)
	}
	return dbID, nil
}

// labelableRefFromID decodes a LabelableNode (Issue or PullRequest) node ID
// into owner, repo, number coordinates for an Issue or PullRequest.
func (r *Resolver) labelableRefFromID(ctx context.Context, id string) (owner, name string, number int64, isPR bool, err error) {
	kind, dbID, decErr := nodeid.Decode(id)
	if decErr != nil {
		return "", "", 0, false, unresolvable("Issue or PullRequest", id)
	}
	switch kind {
	case nodeid.KindIssue:
		owner, name, number, err = r.Issues.IssueRef(ctx, dbID)
		if errors.Is(err, domain.ErrIssueNotFound) {
			return "", "", 0, false, unresolvable("Issue", id)
		}
		return owner, name, number, false, mapErr(err)
	case nodeid.KindPullRequest:
		owner, name, number, err = r.Pulls.PRRef(ctx, dbID)
		if errors.Is(err, domain.ErrPullNotFound) {
			return "", "", 0, true, unresolvable("PullRequest", id)
		}
		return owner, name, number, true, mapErr(err)
	default:
		return "", "", 0, false, unresolvable("Issue or PullRequest", id)
	}
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

// mapMergeErr translates merge-specific domain errors into the GraphQL error
// messages a client expects, matching GitHub's phrasing where possible.
func mapMergeErr(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotMergeable):
		return gqlError{"Pull request is not mergeable"}
	case errors.Is(err, domain.ErrHeadMismatch):
		return gqlError{"Head sha mismatch"}
	case errors.Is(err, domain.ErrInvalidMergeMethod):
		return gqlError{"Merge method is invalid"}
	default:
		return mapErr(err)
	}
}

// labelNamesFromIDs decodes a slice of label node IDs into label names, skipping
// any ID that does not decode to a known label.
func (r *Resolver) labelNamesFromIDs(ctx context.Context, ids []string) ([]string, error) {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		kind, dbID, err := nodeid.Decode(id)
		if err != nil || kind != nodeid.KindLabel {
			continue
		}
		name, err := r.Issues.LabelNameByDBID(ctx, dbID)
		if err != nil {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

// userLoginsFromIDs decodes a slice of user node IDs into user logins, skipping
// any ID that does not decode to a known user.
func (r *Resolver) userLoginsFromIDs(ctx context.Context, ids []string) ([]string, error) {
	logins := make([]string, 0, len(ids))
	for _, id := range ids {
		kind, pk, err := nodeid.Decode(id)
		if err != nil || kind != nodeid.KindUser {
			continue
		}
		login, err := r.Issues.UserLoginByPK(ctx, pk)
		if err != nil {
			continue
		}
		logins = append(logins, login)
	}
	return logins, nil
}

// labelableFromIssue converts the updated domain issue into the GraphQL
// LabelableNode shape. For issues it renders directly; for PRs it re-fetches so
// the PullRequest shape (with base/head refs etc.) is returned.
func (r *Resolver) labelableFromIssue(ctx context.Context, owner, name string, number int64, isPR bool, iss *domain.Issue) (generated.LabelableNode, error) {
	if !isPR {
		return r.URLs.GQLIssue(owner, name, iss, r.NodeFormat), nil
	}
	pr, err := r.Pulls.GetPR(ctx, viewerID(ctx), owner, name, number)
	if err != nil {
		return nil, mapErr(err)
	}
	return r.URLs.GQLPullRequest(owner, name, pr, r.NodeFormat), nil
}

// assignableFromIssue converts the updated domain issue into the GraphQL
// AssignableNode shape. Mirrors labelableFromIssue.
func (r *Resolver) assignableFromIssue(ctx context.Context, owner, name string, number int64, isPR bool, iss *domain.Issue) (generated.AssignableNode, error) {
	if !isPR {
		return r.URLs.GQLIssue(owner, name, iss, r.NodeFormat), nil
	}
	pr, err := r.Pulls.GetPR(ctx, viewerID(ctx), owner, name, number)
	if err != nil {
		return nil, mapErr(err)
	}
	return r.URLs.GQLPullRequest(owner, name, pr, r.NodeFormat), nil
}

// resolveNode decodes a node ID and fetches the matching domain object,
// returning a generated.Node or nil when the ID does not resolve.
func (r *Resolver) resolveNode(ctx context.Context, id string) (generated.Node, error) {
	kind, dbID, err := nodeid.Decode(id)
	if err != nil {
		return nil, nil
	}
	viewer := viewerID(ctx)
	switch kind {
	case nodeid.KindRepository:
		repo, err := r.Repos.GetRepoByID(ctx, viewer, dbID)
		if errors.Is(err, domain.ErrRepoNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var branch *git.Branch
		if b, e := r.Repos.DefaultBranchRef(repo); e == nil {
			branch = &b
		}
		out := r.URLs.GQLRepository(repo, branch, r.NodeFormat)
		return out, nil
	case nodeid.KindIssue:
		owner, name, number, err := r.Issues.IssueRef(ctx, dbID)
		if errors.Is(err, domain.ErrIssueNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		iss, err := r.Issues.GetIssue(ctx, viewer, owner, name, number)
		if errors.Is(err, domain.ErrIssueNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return r.URLs.GQLIssue(owner, name, iss, r.NodeFormat), nil
	case nodeid.KindPullRequest:
		pr, err := r.Pulls.GetPRByID(ctx, viewer, dbID)
		if errors.Is(err, domain.ErrPullNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return r.URLs.GQLPullRequest(pr.Repo.Owner.Login, pr.Repo.Name, pr, r.NodeFormat), nil
	case nodeid.KindUser:
		u, err := r.Users.Viewer(ctx, dbID)
		if err != nil {
			return nil, nil
		}
		return r.URLs.GQLUser(u, r.NodeFormat), nil
	default:
		return nil, nil
	}
}

// listReposForLogin resolves a list of repositories for a user identified by
// login, applying visibility rules for the viewer.
func (r *Resolver) listReposForLogin(ctx context.Context, login string, first *int32) (*gqlmodel.RepositoryConnection, error) {
	u, err := r.Users.ByLogin(ctx, login)
	if errors.Is(err, domain.ErrUserNotFound) {
		return &gqlmodel.RepositoryConnection{PageInfo: &gqlmodel.PageInfo{}}, nil
	}
	if err != nil {
		return nil, err
	}
	repos, err := r.Repos.ListRepos(ctx, viewerID(ctx), u.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	limit := len(repos)
	if first != nil && int(*first) < limit {
		limit = int(*first)
	}
	nodes := make([]*gqlmodel.Repository, 0, limit)
	for i, repo := range repos {
		if i >= limit {
			break
		}
		var branch *git.Branch
		if b, e := r.Repos.DefaultBranchRef(repo); e == nil {
			branch = &b
		}
		out := r.URLs.GQLRepository(repo, branch, r.NodeFormat)
		nodes = append(nodes, &out)
	}
	return &gqlmodel.RepositoryConnection{
		Nodes:      nodes,
		PageInfo:   &gqlmodel.PageInfo{},
		TotalCount: int32(len(repos)),
	}, nil
}

// gqlReview renders a domain review into the generated PullRequestReview wire type.
func gqlReview(rv *domain.Review, urls *presenter.URLBuilder, owner, repo string, format nodeid.Format) *generated.PullRequestReview {
	pullNum := strconv.FormatInt(rv.PullNumber, 10)
	reviewID := strconv.FormatInt(rv.ID, 10)
	htmlURL := urls.RepoHTML(owner, repo) + "/pull/" + pullNum + "#pullrequestreview-" + reviewID
	r := &generated.PullRequestReview{
		ID:    nodeid.Encode(nodeid.KindPullRequestReview, rv.ID, format),
		State: generated.PullRequestReviewState(rv.State),
		Body:  rv.Body,
		URL:   gqlmodel.URI(htmlURL),
	}
	if rv.User != nil {
		r.Author = urls.GQLUser(rv.User, format)
	}
	if rv.SubmittedAt != nil {
		dt := gqlmodel.NewDateTime(*rv.SubmittedAt)
		r.SubmittedAt = &dt
	}
	return r
}

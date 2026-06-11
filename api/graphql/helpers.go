package graphql

// This file holds the hand-written helpers the resolvers share: viewer
// resolution, node-ID decoding, connection assembly, the page-argument mapping,
// and the error translation. They live outside the {name}.resolvers.go files so
// gqlgen, which rewrites those on every generate, never quarantines them.

import (
	"context"
	"errors"
	"sort"
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
// connection. Each edge's cursor carries its absolute offset plus the issue's
// number, so a follow-up after: cursor resumes past it with a keyset seek.
func (r *Resolver) buildIssueConnection(owner, name string, issues []*domain.Issue, total, offset int) *gqlmodel.IssueConnection {
	nodes := make([]*gqlmodel.Issue, 0, len(issues))
	edges := make([]*gqlmodel.IssueEdge, 0, len(issues))
	for i, iss := range issues {
		node := r.URLs.GQLIssue(owner, name, iss, r.NodeFormat)
		nodes = append(nodes, node)
		edges = append(edges, &gqlmodel.IssueEdge{Cursor: encodeCursorSeek(offset+i+1, iss.Number), Node: node})
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
// visible object of the expected type. It carries the NOT_FOUND type gh's
// GraphQLError.Match reads.
func unresolvable(kind, id string) error {
	return notFoundf("Could not resolve to a %s with the global id of '%s'.", kind, id)
}

// mapErr translates a domain error into the message a GraphQL client sees. A
// not-found stays generic so a private object does not leak; a permission or
// validation failure surfaces its reason.
func mapErr(err error) error {
	switch {
	case errors.Is(err, domain.ErrForbidden):
		return typedError{msg: "You do not have permission to perform this action.", errType: "FORBIDDEN"}
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
// returning a generated.Node or nil when the ID does not resolve. Git refs
// carry a repo-scoped git-object encoding rather than a (kind, dbid) pair, so
// they decode through the git-object codec first.
func (r *Resolver) resolveNode(ctx context.Context, id string) (generated.Node, error) {
	if tag, repoID, name, gErr := nodeid.DecodeGitObject(id); gErr == nil {
		if tag != "ref" {
			return nil, nil
		}
		return r.resolveRefNode(ctx, repoID, name)
	}
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
	case nodeid.KindLabel:
		labelName, owner, name, err := r.Issues.LabelRepoRef(ctx, dbID)
		if errors.Is(err, domain.ErrLabelNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		l, err := r.Issues.GetLabel(ctx, viewer, owner, name, labelName)
		if errors.Is(err, domain.ErrLabelNotFound) || errors.Is(err, domain.ErrRepoNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return r.URLs.GQLLabel(l, r.NodeFormat), nil
	case nodeid.KindMilestone:
		number, owner, name, err := r.Issues.MilestoneRepoRef(ctx, dbID)
		if errors.Is(err, domain.ErrMilestoneNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		m, err := r.Issues.GetMilestone(ctx, viewer, owner, name, number)
		if errors.Is(err, domain.ErrMilestoneNotFound) || errors.Is(err, domain.ErrRepoNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return r.URLs.GQLMilestone(owner, name, m, r.NodeFormat), nil
	case nodeid.KindIssueComment:
		owner, name, err := r.Issues.CommentRepoRef(ctx, dbID)
		if errors.Is(err, domain.ErrCommentNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		cm, err := r.Issues.GetComment(ctx, viewer, owner, name, dbID)
		if errors.Is(err, domain.ErrCommentNotFound) || errors.Is(err, domain.ErrRepoNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return r.URLs.GQLIssueComment(owner, name, cm, r.NodeFormat), nil
	case nodeid.KindPullRequestReview:
		owner, name, number, err := r.Reviews.ReviewRef(ctx, viewer, dbID)
		if errors.Is(err, domain.ErrReviewNotFound) || errors.Is(err, domain.ErrRepoNotFound) || errors.Is(err, domain.ErrPullNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		rv, err := r.Reviews.GetReview(ctx, viewer, owner, name, number, dbID)
		if errors.Is(err, domain.ErrReviewNotFound) || errors.Is(err, domain.ErrRepoNotFound) || errors.Is(err, domain.ErrPullNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return gqlReview(rv, r.URLs, owner, name, r.NodeFormat), nil
	case nodeid.KindPullRequestReviewThread:
		owner, name, number, err := r.Reviews.ThreadRef(ctx, viewer, dbID)
		if errors.Is(err, domain.ErrCommentNotFound) || errors.Is(err, domain.ErrRepoNotFound) || errors.Is(err, domain.ErrPullNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		threads, err := r.Reviews.ReviewThreads(ctx, viewer, owner, name, number)
		if err != nil {
			return nil, err
		}
		for _, t := range threads {
			if t.ID == dbID {
				return r.URLs.GQLReviewThread(owner, name, t, r.NodeFormat), nil
			}
		}
		return nil, nil
	case nodeid.KindCheckRun:
		owner, name, err := r.Checks.CheckRunRef(ctx, dbID)
		if errors.Is(err, domain.ErrCheckNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		cr, err := r.Checks.GetCheckRun(ctx, viewer, owner, name, dbID)
		if errors.Is(err, domain.ErrCheckNotFound) || errors.Is(err, domain.ErrRepoNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		out := presenter.GQLCheckRun(cr, r.NodeFormat)
		return out, nil
	default:
		return nil, nil
	}
}

// resolveRefNode resolves a ref node id's (repo, qualified name) pair to the
// Ref shape, or nil when the repository or the ref does not resolve for the
// viewer.
func (r *Resolver) resolveRefNode(ctx context.Context, repoID int64, qualifiedName string) (generated.Node, error) {
	repo, err := r.Repos.GetRepoByID(ctx, viewerID(ctx), repoID)
	if errors.Is(err, domain.ErrRepoNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ref, err := r.Repos.GetRef(repo, qualifiedName)
	if err != nil {
		return nil, nil
	}
	shortName := qualifiedName
	if i := strings.LastIndex(qualifiedName, "/"); i >= 0 {
		shortName = qualifiedName[i+1:]
	}
	return presenter.GQLRef(repo.ID, qualifiedName, shortName, ref.Target), nil
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
		ID:                nodeid.Encode(nodeid.KindPullRequestReview, rv.ID, format),
		State:             generated.PullRequestReviewState(rv.State),
		Body:              rv.Body,
		URL:               gqlmodel.URI(htmlURL),
		AuthorAssociation: presenter.GQLAuthorAssociation(owner, rv.User),
		ReactionGroups:    []*gqlmodel.ReactionGroup{}, // Githome does not store reactions
	}
	if rv.User != nil {
		r.Author = urls.GQLUser(rv.User, format)
	}
	if rv.CommitID != "" {
		r.Commit = &gqlmodel.Commit{
			Oid:       gqlmodel.GitObjectID(rv.CommitID),
			RepoOwner: owner,
			RepoName:  repo,
		}
	}
	if rv.SubmittedAt != nil {
		dt := gqlmodel.NewDateTime(*rv.SubmittedAt)
		r.SubmittedAt = &dt
	}
	return r
}

// buildReviewConnection windows a review listing and renders it into the
// GraphQL connection with Relay page info.
func (r *Resolver) buildReviewConnection(revs []*domain.Review, page issuePage, owner, repo string) *generated.PullRequestReviewConnection {
	total := len(revs)
	start, end := page.window(total)
	nodes := make([]*generated.PullRequestReview, 0, end-start)
	for _, rv := range revs[start:end] {
		nodes = append(nodes, gqlReview(rv, r.URLs, owner, repo, r.NodeFormat))
	}
	return &generated.PullRequestReviewConnection{
		Nodes:      nodes,
		PageInfo:   pageInfoFor(start, end-start, total),
		TotalCount: int32(total),
	}
}

// emptyProjectCardConnection is the always-empty classic-projects connection
// the issue and pull request projectCards fields resolve to.
func emptyProjectCardConnection() *generated.ProjectCardConnection {
	return &generated.ProjectCardConnection{
		Nodes:      []*generated.ProjectCard{},
		PageInfo:   &gqlmodel.PageInfo{},
		TotalCount: 0,
	}
}

// latestReviewsOf keeps the most recent submitted review per reviewer, in the
// order the underlying listing returned them (oldest first). Pending drafts are
// not part of the latest set, matching GitHub's latestReviews.
func latestReviewsOf(revs []*domain.Review) []*domain.Review {
	out := make([]*domain.Review, 0, len(revs))
	seen := map[string]int{}
	for _, rv := range revs {
		if rv.State == domain.ReviewPending {
			continue
		}
		login := ""
		if rv.User != nil {
			login = rv.User.Login
		}
		if i, ok := seen[login]; ok {
			out[i] = rv
			continue
		}
		seen[login] = len(out)
		out = append(out, rv)
	}
	return out
}

// commentsWindow reads the absolute comment window [start, end) for an issue
// or pull request through the offset-paged listing. The listing is page
// aligned, so it reads the aligned page (or the two pages) covering the window
// and trims to it. gh sends comments(last: 1) for the issueCommentLast
// fragment, which lands here as the window [total-1, total).
func (r *Resolver) commentsWindow(ctx context.Context, owner, name string, number int64, start, end int) ([]*domain.Comment, error) {
	if end <= start {
		return nil, nil
	}
	size := end - start
	pageIdx := start / size // zero-based index of the aligned page
	rows, err := r.Issues.ListComments(ctx, viewerID(ctx), owner, name, number, int64(pageIdx+1), int64(size))
	if err != nil {
		return nil, err
	}
	skip := start - pageIdx*size
	if skip > 0 {
		next, err := r.Issues.ListComments(ctx, viewerID(ctx), owner, name, number, int64(pageIdx+2), int64(size))
		if err != nil {
			return nil, err
		}
		rows = append(rows, next...)
	}
	lo, hi := skip, skip+size
	if lo > len(rows) {
		lo = len(rows)
	}
	if hi > len(rows) {
		hi = len(rows)
	}
	return rows[lo:hi], nil
}

// issueListQuery maps the GraphQL issue list arguments onto the domain query.
// gh issue list sends its assignee, author, label, and milestone filters
// through filterBy; the labels argument is the older spelling some clients
// still send. mentioned, since, and viewerSubscribed are not modeled, so they
// narrow nothing rather than erroring.
func issueListQuery(states []gqlmodel.IssueState, filterBy *generated.IssueFilters, orderBy *generated.IssueOrder, labels []string, page issuePage) domain.IssueQuery {
	q := domain.IssueQuery{
		State:     stateFilter(states),
		Sort:      "created",
		Direction: "desc",
		Labels:    labels,
		Page:      page.page(),
		PerPage:   page.limit,
	}
	if filterBy != nil {
		if filterBy.Assignee != nil {
			q.AssigneeLogin = *filterBy.Assignee
		}
		if filterBy.CreatedBy != nil {
			q.CreatorLogin = *filterBy.CreatedBy
		}
		if len(filterBy.Labels) > 0 {
			q.Labels = append(append([]string{}, q.Labels...), filterBy.Labels...)
		}
		if len(filterBy.States) > 0 {
			q.State = stateFilter(filterBy.States)
		}
		// GitHub's filter carries the milestone number as a string in either
		// field; a title that does not parse as a number matches nothing here.
		for _, ms := range []*string{filterBy.MilestoneNumber, filterBy.Milestone} {
			if ms == nil {
				continue
			}
			if n, err := strconv.ParseInt(*ms, 10, 64); err == nil {
				q.MilestoneNumber = &n
				break
			}
		}
	}
	if orderBy != nil {
		switch orderBy.Field {
		case generated.IssueOrderFieldUpdatedAt:
			q.Sort = "updated"
		case generated.IssueOrderFieldComments:
			q.Sort = "comments"
		}
		if orderBy.Direction == generated.OrderDirectionAsc {
			q.Direction = "asc"
		}
	}
	return q
}

// defaultPROrder reports whether the requested order is the listing's native
// newest-first order, which needs no in-memory sort.
func defaultPROrder(o *generated.IssueOrder) bool {
	return o == nil || (o.Field == generated.IssueOrderFieldCreatedAt && o.Direction == generated.OrderDirectionDesc)
}

// prScanCap bounds the filtered pull request scan so a head/base/label filter
// on a huge repository stays a few page reads rather than a table walk.
const prScanCap = 1000

// scanPullRequests reads the repository's pull requests newest first, page by
// page, and keeps the ones matching the list filters. gh's filtered listings
// (pr view <branch>, pr list --label) want the newest matches, which the scan
// reads first; matches past the cap are not found.
func (r *Resolver) scanPullRequests(ctx context.Context, owner, name string, states []gqlmodel.PullRequestState, head, base *string, labels []string) ([]*domain.PullRequest, error) {
	var matched []*domain.PullRequest
	for pageN, seen := 1, 0; seen < prScanCap; pageN++ {
		prs, total, err := r.Pulls.ListPRs(ctx, viewerID(ctx), owner, name, domain.PRQuery{
			State:   pullStateFilter(states),
			Page:    pageN,
			PerPage: maxPageSize,
		})
		if err != nil {
			return nil, err
		}
		for _, pr := range prs {
			if prMatches(pr, states, head, base, labels) {
				matched = append(matched, pr)
			}
		}
		seen += len(prs)
		if len(prs) < maxPageSize || seen >= total {
			break
		}
	}
	return matched, nil
}

// prMatches reports whether a pull request passes the list filters: the exact
// requested states (MERGED vs CLOSED, which the coarse store filter folds
// together), the head and base branch names, and every requested label.
func prMatches(pr *domain.PullRequest, states []gqlmodel.PullRequestState, head, base *string, labels []string) bool {
	if !prStateMatches(pr, states) {
		return false
	}
	if head != nil && pr.Head.Ref != *head {
		return false
	}
	if base != nil && pr.Base.Ref != *base {
		return false
	}
	for _, want := range labels {
		found := false
		for _, l := range pr.Labels {
			if strings.EqualFold(l.Name, want) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// prStateMatches reports whether the pull request is in one of the requested
// GraphQL states. An empty list matches everything.
func prStateMatches(pr *domain.PullRequest, states []gqlmodel.PullRequestState) bool {
	if len(states) == 0 {
		return true
	}
	for _, s := range states {
		switch s {
		case gqlmodel.PullRequestStateOpen:
			if pr.State == "open" {
				return true
			}
		case gqlmodel.PullRequestStateClosed:
			if pr.State == "closed" && !pr.Merged {
				return true
			}
		case gqlmodel.PullRequestStateMerged:
			if pr.Merged {
				return true
			}
		}
	}
	return false
}

// sortPullRequests orders the scanned set by the requested field in place. The
// scan reads newest first, so the native CREATED_AT DESC order needs nothing.
func sortPullRequests(prs []*domain.PullRequest, orderBy *generated.IssueOrder) {
	if defaultPROrder(orderBy) {
		return
	}
	asc := orderBy.Direction == generated.OrderDirectionAsc
	cmp := func(i, j int) int {
		switch orderBy.Field {
		case generated.IssueOrderFieldUpdatedAt:
			return prs[i].UpdatedAt.Compare(prs[j].UpdatedAt)
		case generated.IssueOrderFieldComments:
			return prs[i].CommentsCount - prs[j].CommentsCount
		default:
			return prs[i].CreatedAt.Compare(prs[j].CreatedAt)
		}
	}
	sort.SliceStable(prs, func(i, j int) bool {
		if asc {
			return cmp(i, j) < 0
		}
		return cmp(i, j) > 0
	})
}

// sortLabels orders a label listing in place. The store hands labels back in
// name order, which is also GitHub's default, so absent or NAME ASC ordering
// needs nothing.
func sortLabels(ls []*domain.Label, orderBy *generated.LabelOrder) {
	if orderBy == nil {
		return
	}
	asc := orderBy.Direction == generated.OrderDirectionAsc
	if orderBy.Field == generated.LabelOrderFieldName && asc {
		return
	}
	cmp := func(i, j int) int {
		if orderBy.Field == generated.LabelOrderFieldCreatedAt {
			return ls[i].CreatedAt.Compare(ls[j].CreatedAt)
		}
		return strings.Compare(ls[i].Name, ls[j].Name)
	}
	sort.SliceStable(ls, func(i, j int) bool {
		if asc {
			return cmp(i, j) < 0
		}
		return cmp(i, j) > 0
	})
}

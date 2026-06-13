package graphql_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"

	graphqlapi "github.com/tamnd/githome/api/graphql"
	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// R02-28: the other contract tests hand-reduce gh's documents to the handful of
// fields a single assertion needs, so the areas where the P0s lived —
// commits(last:1), ...on User author fragments, hasIssuesEnabled, filterBy,
// latestReviews, and nested pageInfo — were never exercised end to end. The
// documents below are the literal operations the gh CLI sends (matching its
// operation names and structure), scoped to Githome's supported compat surface.
// Each test replays one and asserts the server returns zero `errors`, the only
// contract gh itself enforces before it reads the data.

// ghPullRequestByNumber is gh pr view's document. It exercises the inline
// `... on User` author fragment over the Actor interface, commits(last:1) with
// its nested pageInfo, latestReviews, reviewDecision, the review-request union,
// and the files connection's pageInfo — the selections the reduced PullView
// document dropped.
const ghPullRequestByNumber = `query PullRequestByNumber($owner: String!, $repo: String!, $pr_number: Int!) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $pr_number) {
      id
      number
      title
      state
      body
      isDraft
      maintainerCanModify
      mergeable
      mergeStateStatus
      additions
      deletions
      changedFiles
      baseRefName
      headRefName
      isCrossRepository
      createdAt
      updatedAt
      closedAt
      locked
      author {
        login
        ... on User { id }
      }
      authorAssociation
      milestone { number title }
      reactionGroups { content users { totalCount } }
      labels(first: 10) { nodes { name color } }
      assignees(first: 10) { nodes { login } }
      reviewRequests(first: 10) {
        totalCount
        nodes { requestedReviewer { ... on User { login } ... on Team { name } } }
      }
      reviewDecision
      latestReviews(first: 100) {
        nodes { author { login } state }
      }
      commits(last: 1) {
        totalCount
        nodes { commit { oid messageHeadline message abbreviatedOid } }
        pageInfo { hasNextPage endCursor }
      }
      files(first: 100) {
        totalCount
        nodes { path additions deletions }
        pageInfo { hasNextPage endCursor }
      }
      projectCards(first: 0) { totalCount }
    }
  }
}`

// ghIssueByNumber is gh issue view's document, exercising the author inline
// fragment, the comments connection with nested pageInfo, labels, and the
// reaction-group rollup.
const ghIssueByNumber = `query IssueByNumber($owner: String!, $repo: String!, $issue_number: Int!) {
  repository(owner: $owner, name: $repo) {
    hasIssuesEnabled
    issue(number: $issue_number) {
      id
      number
      title
      state
      stateReason
      body
      url
      createdAt
      updatedAt
      closedAt
      locked
      author {
        login
        ... on User { id }
      }
      labels(first: 10) { nodes { name color } }
      assignees(first: 10) { nodes { login } }
      milestone { number title }
      reactionGroups { content users { totalCount } }
      comments(first: 100) {
        totalCount
        nodes { author { login } body createdAt }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}`

// ghIssueList is gh issue list's document: an ordered, state-filtered issue
// connection narrowed by filterBy, with the edge cursors and nested pageInfo
// the command pages on.
const ghIssueList = `query IssueList($owner: String!, $repo: String!, $limit: Int!, $states: [IssueState!], $labels: [String!], $assignee: String) {
  repository(owner: $owner, name: $repo) {
    hasIssuesEnabled
    issues(first: $limit, orderBy: {field: CREATED_AT, direction: DESC}, states: $states, labels: $labels, filterBy: {assignee: $assignee}) {
      totalCount
      nodes {
        number
        title
        state
        author { login ... on User { id } }
        labels(first: 5) { nodes { name } }
      }
      edges { cursor node { number } }
      pageInfo { hasNextPage hasPreviousPage startCursor endCursor }
    }
  }
}`

// ghPullRequestList is gh pr list's document: the open pull request connection
// filtered by base ref with edges, cursors, and nested pageInfo.
const ghPullRequestList = `query PullRequestList($owner: String!, $repo: String!, $limit: Int!, $states: [PullRequestState!], $baseBranch: String, $labels: [String!]) {
  repository(owner: $owner, name: $repo) {
    pullRequests(first: $limit, orderBy: {field: CREATED_AT, direction: DESC}, states: $states, baseRefName: $baseBranch, labels: $labels) {
      totalCount
      nodes {
        number
        title
        state
        isDraft
        mergeable
        baseRefName
        headRefName
        author { login ... on User { id } }
        reviewDecision
        latestReviews(first: 100) { nodes { author { login } state } }
      }
      edges { cursor node { number } }
      pageInfo { hasNextPage hasPreviousPage startCursor endCursor }
    }
  }
}`

// ghPullRequestForBranch is the document gh runs to find the pull request a
// branch opens, the headRefName lookup behind gh pr view with no argument.
const ghPullRequestForBranch = `query PullRequestForBranch($owner: String!, $repo: String!, $headRefName: String!, $states: [PullRequestState!]) {
  repository(owner: $owner, name: $repo) {
    pullRequests(headRefName: $headRefName, states: $states, first: 30) {
      totalCount
      nodes {
        number
        state
        baseRefName
        headRefName
        isCrossRepository
        headRepositoryOwner { login }
        author { login ... on User { id } }
      }
    }
  }
}`

// ghIssueStatus is gh issue status's document: three aliased issue connections
// — assigned, mentioned, and authored — over filterBy, plus hasIssuesEnabled,
// the shape that broke when filterBy was missing.
const ghIssueStatus = `query IssueStatus($owner: String!, $repo: String!, $viewer: String!, $per_page: Int = 100) {
  repository(owner: $owner, name: $repo) {
    hasIssuesEnabled
    assigned: issues(filterBy: {assignee: $viewer, states: [OPEN]}, first: $per_page, orderBy: {field: UPDATED_AT, direction: DESC}) {
      totalCount
      nodes { number title state author { login } }
    }
    mentioned: issues(filterBy: {mentioned: $viewer, states: [OPEN]}, first: $per_page, orderBy: {field: UPDATED_AT, direction: DESC}) {
      totalCount
      nodes { number title state }
    }
    authored: issues(filterBy: {createdBy: $viewer, states: [OPEN]}, first: $per_page, orderBy: {field: UPDATED_AT, direction: DESC}) {
      totalCount
      nodes { number title state }
    }
  }
}`

// ghRepoNetwork is the document gh runs to resolve a repository and its fork
// parent: the repo fragment with owner, parent, defaultBranchRef, and
// viewerPermission gh's fork and clone flows read.
const ghRepoNetwork = `query RepositoryNetwork($owner: String!, $repo: String!) {
  repository(owner: $owner, name: $repo) {
    id
    name
    owner { login }
    isPrivate
    isFork
    viewerPermission
    defaultBranchRef { name target { oid } }
    parent { name owner { login } }
  }
}`

// ghDocServer seeds a repository carrying both a pull request with a submitted
// review and a plain issue, so every literal gh document below resolves against
// real data rather than an empty repository.
func ghDocServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	ctx := context.Background()

	st, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "githome.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	owner := &store.UserRow{Login: "octocat", Type: "User"}
	if err := st.InsertUser(ctx, owner); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	reviewer := &store.UserRow{Login: "hubot", Type: "User"}
	if err := st.InsertUser(ctx, reviewer); err != nil {
		t.Fatalf("insert reviewer: %v", err)
	}
	token := seedGraphQLToken(t, st, owner.PK)

	repo := &store.RepoRow{OwnerPK: owner.PK, Name: "hello", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("insert repo: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	pullBareRepo(t, gitStore, repo.PK)

	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	pullSvc := domain.NewPRService(st, repoSvc, issueSvc, gitStore)
	reviewSvc := domain.NewReviewService(st, repoSvc, pullSvc, issueSvc, gitStore)
	checksSvc := domain.NewChecksService(st, repoSvc, issueSvc, gitStore)

	prBody := "It adds a feature."
	if _, err := pullSvc.CreatePR(ctx, owner.PK, "octocat", "hello", domain.PRInput{
		Title: "Add a feature", Body: &prBody, Base: "main", Head: "feature",
	}); err != nil {
		t.Fatalf("seed pull: %v", err)
	}
	iss, err := st.GetIssueByNumber(ctx, repo.PK, 1)
	if err != nil {
		t.Fatalf("GetIssueByNumber: %v", err)
	}
	if err := pullSvc.RecomputeMergeability(ctx, iss.PK); err != nil {
		t.Fatalf("RecomputeMergeability: %v", err)
	}

	// A submitted review so latestReviews and reviewDecision resolve real data.
	line := int64(1)
	if _, err := reviewSvc.CreateReview(ctx, reviewer.PK, "octocat", "hello", 1, domain.ReviewInput{
		Event: domain.EventApprove,
		Body:  "Looks good to me.",
		Comments: []domain.ReviewCommentInput{
			{Path: "feature.txt", Body: "Nice addition.", Side: "RIGHT", Line: &line},
		},
	}); err != nil {
		t.Fatalf("seed review: %v", err)
	}

	// A plain issue (number 2) so IssueByNumber resolves a real node.
	issueBody := "Something is off."
	if _, err := issueSvc.CreateIssue(ctx, owner.PK, "octocat", "hello", domain.IssueInput{
		Title: "A plain issue", Body: &issueBody,
	}); err != nil {
		t.Fatalf("seed issue: %v", err)
	}

	authSvc := auth.NewService(st, "https://git.test.internal")
	t.Cleanup(authSvc.Close)

	h := graphqlapi.NewHandler(graphqlapi.Deps{
		Auth:       authSvc,
		Repos:      repoSvc,
		Issues:     issueSvc,
		Pulls:      pullSvc,
		Reviews:    reviewSvc,
		Checks:     checksSvc,
		Users:      domain.NewUserService(st),
		URLs:       presenter.NewURLBuilder(graphqlURLs(t)),
		NodeFormat: nodeid.FormatNew,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, token
}

// assertNoGraphQLErrors fails when a GraphQL response carries any errors, the
// single contract gh enforces before it trusts the data.
func assertNoGraphQLErrors(t *testing.T, op string, got []byte) {
	t.Helper()
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Path    []any  `json:"path"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("%s: unmarshal response: %v\n%s", op, err, got)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("%s returned errors: %v\n%s", op, env.Errors, got)
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		t.Fatalf("%s returned null data\n%s", op, got)
	}
}

// TestGHLiteralDocuments replays each literal gh document against a seeded
// repository and asserts the server answers every one with zero errors. The
// table keeps the gh operation name beside its variables so a failure names the
// command whose document regressed.
func TestGHLiteralDocuments(t *testing.T) {
	srv, token := ghDocServer(t)
	const owner, repo = "octocat", "hello"

	cases := []struct {
		op    string
		query string
		vars  map[string]any
	}{
		{"PullRequestByNumber", ghPullRequestByNumber, map[string]any{"owner": owner, "repo": repo, "pr_number": 1}},
		{"IssueByNumber", ghIssueByNumber, map[string]any{"owner": owner, "repo": repo, "issue_number": 2}},
		{"IssueList", ghIssueList, map[string]any{"owner": owner, "repo": repo, "limit": 30, "states": []string{"OPEN"}, "labels": nil, "assignee": nil}},
		{"PullRequestList", ghPullRequestList, map[string]any{"owner": owner, "repo": repo, "limit": 30, "states": []string{"OPEN"}, "baseBranch": nil, "labels": nil}},
		{"PullRequestForBranch", ghPullRequestForBranch, map[string]any{"owner": owner, "repo": repo, "headRefName": "feature", "states": []string{"OPEN"}}},
		{"IssueStatus", ghIssueStatus, map[string]any{"owner": owner, "repo": repo, "viewer": "octocat"}},
		{"RepositoryNetwork", ghRepoNetwork, map[string]any{"owner": owner, "repo": repo}},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			got := post(t, srv, token, c.query, c.vars)
			assertNoGraphQLErrors(t, c.op, got)
		})
	}
}

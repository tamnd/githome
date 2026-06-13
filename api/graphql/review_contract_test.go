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

// The reviewDecision document gh pr view selects to show whether the pull request
// is approved or blocked.
const reviewDecisionQuery = `query Decision($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) { reviewDecision }
  }
}`

// The reviewThreads document gh pr view selects to show the conversations on a
// pull request, each a root comment and its replies.
const reviewThreadsQuery = `query Threads($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 10) {
        totalCount
        nodes {
          id
          isResolved
          isOutdated
          path
          line
          comments(first: 10) {
            totalCount
            nodes { body path outdated author { login } }
          }
        }
      }
    }
  }
}`

// The status rollup document gh pr status selects: the head commit's combined
// state, read off the pull request's commits.
const statusRollupQuery = `query Rollup($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      commits(first: 10) {
        nodes { commit { statusCheckRollup { state } } }
      }
    }
  }
}`

// The field pack gh pr view layers onto the pull request: the head coordinates,
// the review summary connections, and the always-empty project cards. The
// fragments mirror gh v2.63.0's PullRequest fields (api/queries_pr.go).
const pullFieldPackQuery = `query FieldPack($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      fullDatabaseId
      isCrossRepository
      maintainerCanModify
      authorAssociation
      reactionGroups { content users { totalCount } }
      mergedBy { login }
      headRepositoryOwner { id login ... on User { name } }
      headRepository { id name }
      autoMergeRequest { authorEmail commitBody commitHeadline mergeMethod enabledAt enabledBy { login } }
      reviewRequests(first: 100) {
        nodes { requestedReviewer { __typename ... on User { login } ... on Team { organization { login } name slug } } }
        pageInfo { hasNextPage endCursor }
        totalCount
      }
      latestReviews(first: 100) {
        nodes { author { login } authorAssociation submittedAt body state commit { oid } }
        pageInfo { hasNextPage endCursor }
        totalCount
      }
      reviews(first: 100) {
        nodes { author { login } authorAssociation state reactionGroups { content } }
        pageInfo { hasNextPage endCursor }
        totalCount
      }
      projectCards(first: 100) { nodes { project { name } column { name } } totalCount }
      commits(first: 10) { pageInfo { hasNextPage endCursor } totalCount }
      files(first: 10) { pageInfo { hasNextPage endCursor } totalCount }
    }
  }
}`

const resolveThreadMutation = `mutation Resolve($threadId: ID!) {
  resolveReviewThread(input: {threadId: $threadId}) {
    thread { isResolved path }
  }
}`

const unresolveThreadMutation = `mutation Unresolve($threadId: ID!) {
  unresolveReviewThread(input: {threadId: $threadId}) {
    thread { isResolved path }
  }
}`

type reviewGQLFixture struct {
	srv         *httptest.Server
	ownerToken  string
	reviewToken string
	ctx         context.Context
}

// reviewGraphQLServer seeds octocat as the repository owner and hubot as the
// reviewer, opens a feature pull request, approves it with one inline comment so a
// review thread exists, and reports a green status on the head. The owner cannot
// approve their own pull request, so the reviewer drives the approval the way a
// real review lands.
func reviewGraphQLServer(t *testing.T) reviewGQLFixture {
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
	ownerToken := seedGraphQLToken(t, st, owner.PK)
	reviewToken := seedGraphQLToken(t, st, reviewer.PK)

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

	body := "It adds a feature."
	if _, err := pullSvc.CreatePR(ctx, owner.PK, "octocat", "hello", domain.PRInput{
		Title: "Add a feature", Body: &body, Base: "main", Head: "feature",
	}); err != nil {
		t.Fatalf("seed pull: %v", err)
	}
	// Run the mergeability recompute the worker would, so the cached
	// commits_count and changed_files columns hold the real totals the
	// count-only commits and files connections answer from.
	iss, err := st.GetIssueByNumber(ctx, repo.PK, 1)
	if err != nil {
		t.Fatalf("GetIssueByNumber: %v", err)
	}
	if err := pullSvc.RecomputeMergeability(ctx, iss.PK); err != nil {
		t.Fatalf("RecomputeMergeability: %v", err)
	}

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

	if _, err := checksSvc.CreateStatus(ctx, owner.PK, "octocat", "hello", "feature", domain.StatusInput{
		State: "success", Context: "ci/test", Description: "build passed",
	}); err != nil {
		t.Fatalf("seed status: %v", err)
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
	return reviewGQLFixture{srv: srv, ownerToken: ownerToken, reviewToken: reviewToken, ctx: ctx}
}

// seedGraphQLToken issues a classic PAT for a user and returns its plaintext.
func seedGraphQLToken(t *testing.T, st *store.Store, userPK int64) string {
	t.Helper()
	g, err := auth.GenerateToken(auth.PrefixClassicPAT)
	if err != nil {
		t.Fatal(err)
	}
	hash := g.Hash
	if err := st.InsertToken(context.Background(), &store.TokenRow{
		UserPK: &userPK, TokenHash: hash[:], TokenPrefix: auth.PrefixClassicPAT,
		LastEight: g.Last8, Kind: "pat", Scopes: "repo",
	}); err != nil {
		t.Fatalf("insert token: %v", err)
	}
	return g.Plaintext
}

// TestReviewDecision confirms an approved pull request reports APPROVED, the
// decision gh pr view reads off the reviews.
func TestReviewDecision(t *testing.T) {
	fx := reviewGraphQLServer(t)
	got := post(t, fx.srv, fx.ownerToken, reviewDecisionQuery, map[string]any{"owner": "octocat", "name": "hello", "number": 1})
	assertGolden(t, "review_decision.golden.json", got)
}

// TestReviewThreads confirms the review threads document resolves the inline
// conversation with its comment, author, and anchor against the recorded golden.
func TestReviewThreads(t *testing.T) {
	fx := reviewGraphQLServer(t)
	got := post(t, fx.srv, fx.ownerToken, reviewThreadsQuery, map[string]any{"owner": "octocat", "name": "hello", "number": 1})
	assertGolden(t, "review_threads.golden.json", got)
}

// TestStatusCheckRollup confirms the head commit's rollup folds the green status
// into a SUCCESS state.
func TestStatusCheckRollup(t *testing.T) {
	fx := reviewGraphQLServer(t)
	got := post(t, fx.srv, fx.ownerToken, statusRollupQuery, map[string]any{"owner": "octocat", "name": "hello", "number": 1})
	assertGolden(t, "status_rollup.golden.json", got)
}

// TestPullFieldPack confirms the rest of gh pr view's PullRequest field set
// resolves with no errors: head coordinates, the review summary connections,
// the always-empty project cards, and page info on the nested connections.
func TestPullFieldPack(t *testing.T) {
	fx := reviewGraphQLServer(t)
	got := post(t, fx.srv, fx.ownerToken, pullFieldPackQuery, map[string]any{"owner": "octocat", "name": "hello", "number": 1})
	assertGolden(t, "pull_field_pack.golden.json", got)
}

// TestResolveAndUnresolveThread confirms the resolve and unresolve mutations flip
// a conversation's resolved flag back and forth.
func TestResolveAndUnresolveThread(t *testing.T) {
	fx := reviewGraphQLServer(t)
	threadID := firstThreadID(t, fx)

	got := post(t, fx.srv, fx.ownerToken, resolveThreadMutation, map[string]any{"threadId": threadID})
	if resolved := threadResolved(t, got, "resolveReviewThread"); !resolved {
		t.Fatalf("resolve left thread unresolved: %s", got)
	}

	got = post(t, fx.srv, fx.ownerToken, unresolveThreadMutation, map[string]any{"threadId": threadID})
	if resolved := threadResolved(t, got, "unresolveReviewThread"); resolved {
		t.Fatalf("unresolve left thread resolved: %s", got)
	}
}

// TestResolveThreadUnauthorized confirms a node id of the wrong kind resolves as
// unresolvable rather than settling an unrelated object.
func TestResolveThreadUnauthorized(t *testing.T) {
	fx := reviewGraphQLServer(t)
	bogus := nodeid.Encode(nodeid.KindIssue, 1, nodeid.FormatNew)
	got := post(t, fx.srv, fx.ownerToken, resolveThreadMutation, map[string]any{"threadId": bogus})
	var env struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.Errors) == 0 {
		t.Fatalf("expected an error for a non-thread node id, got %s", got)
	}
}

// firstThreadID reads the node id of the pull request's first review thread.
func firstThreadID(t *testing.T, fx reviewGQLFixture) string {
	t.Helper()
	got := post(t, fx.srv, fx.ownerToken, reviewThreadsQuery, map[string]any{"owner": "octocat", "name": "hello", "number": 1})
	var env struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []struct {
							ID string `json:"id"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal threads: %v, body %s", err, got)
	}
	nodes := env.Data.Repository.PullRequest.ReviewThreads.Nodes
	if len(nodes) == 0 {
		t.Fatalf("no review threads in %s", got)
	}
	return nodes[0].ID
}

// threadResolved pulls the isResolved flag out of a resolve or unresolve payload.
func threadResolved(t *testing.T, body []byte, field string) bool {
	t.Helper()
	var env struct {
		Data map[string]struct {
			Thread struct {
				IsResolved bool `json:"isResolved"`
			} `json:"thread"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal payload: %v, body %s", err, body)
	}
	return env.Data[field].Thread.IsResolved
}

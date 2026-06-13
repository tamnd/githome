package graphql_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// graphqlID posts a query expecting a single string id at data.<root>...id and
// returns it. It fails the test on any GraphQL error so a shape change surfaces
// loudly rather than as an empty id.
func graphqlID(t *testing.T, srv *httptest.Server, token, query string, vars map[string]any, path ...string) string {
	t.Helper()
	got := post(t, srv, token, query, vars)
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal id: %v\n%s", err, got)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("id query returned errors: %v\n%s", env.Errors, got)
	}
	cur := env.Data
	for _, key := range path {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(cur, &obj); err != nil {
			t.Fatalf("descend %q: %v\n%s", key, err, cur)
		}
		cur = obj[key]
	}
	var id string
	if err := json.Unmarshal(cur, &id); err != nil {
		t.Fatalf("read id string: %v\n%s", err, cur)
	}
	if id == "" {
		t.Fatalf("empty id at path %v\n%s", path, got)
	}
	return id
}

// errorMessages decodes the GraphQL errors[] array into messages, and reports
// the top-level type each error carries (GitHub puts NOT_FOUND/UNPROCESSABLE
// there, lifted out of extensions).
func errorMessages(t *testing.T, body []byte) []struct{ Message, Type string } {
	t.Helper()
	var env struct {
		Errors []struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal errors: %v\n%s", err, body)
	}
	out := make([]struct{ Message, Type string }, 0, len(env.Errors))
	for _, e := range env.Errors {
		out = append(out, struct{ Message, Type string }{e.Message, e.Type})
	}
	return out
}

const userIDQuery = `query U($login: String!) { user(login: $login) { id } }`
const issueAuthorIDQuery = `query A($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) { author { ... on User { id } } }
  }
}`
const repoIDQuery = `query R($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) { id }
}`
const prIDByNumberQuery = `query P($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) { pullRequest(number: $number) { id } }
}`

const addLabelsMutation = `mutation AddLabels($labelable: ID!, $labels: [ID!]!) {
  addLabelsToLabelable(input: {labelableId: $labelable, labelIds: $labels}) {
    labelable { ... on Issue { number } }
  }
}`

const updateIssueMilestoneMutation = `mutation UpdateMs($id: ID!, $ms: ID!) {
  updateIssue(input: {id: $id, milestoneId: $ms}) { issue { number } }
}`

// TestStrictIDResolution confirms R02-26: a node ID that does not name the
// expected kind of object errors with GitHub's NOT_FOUND message instead of
// being silently skipped, so a typo'd label or a wrong-kind milestone never
// reports a success that changed nothing.
func TestStrictIDResolution(t *testing.T) {
	srv, token := issueServer(t)
	issueID := issueNodeID(t, srv, token, 1)
	octocatID := graphqlID(t, srv, token, issueAuthorIDQuery,
		map[string]any{"owner": "octocat", "name": "hello", "number": 1}, "repository", "issue", "author", "id")

	// An issue id is not a label id: addLabels must reject it, not drop it.
	got := post(t, srv, token, addLabelsMutation, map[string]any{
		"labelable": issueID, "labels": []string{issueID},
	})
	errs := errorMessages(t, got)
	if len(errs) == 0 {
		t.Fatalf("addLabels with a non-label id reported success: %s", got)
	}
	if !strings.Contains(errs[0].Message, "Could not resolve to a Label") {
		t.Errorf("error message = %q, want a Could-not-resolve-to-a-Label message", errs[0].Message)
	}
	if errs[0].Type != "NOT_FOUND" {
		t.Errorf("error type = %q, want NOT_FOUND", errs[0].Type)
	}

	// A user id is not a milestone id: updateIssue must check the kind, not set
	// a milestone off a matching integer.
	got = post(t, srv, token, updateIssueMilestoneMutation, map[string]any{
		"id": issueID, "ms": octocatID,
	})
	errs = errorMessages(t, got)
	if len(errs) == 0 {
		t.Fatalf("updateIssue with a user id as milestone reported success: %s", got)
	}
	if !strings.Contains(errs[0].Message, "Could not resolve to a Milestone") {
		t.Errorf("error message = %q, want a Could-not-resolve-to-a-Milestone message", errs[0].Message)
	}
	if errs[0].Type != "NOT_FOUND" {
		t.Errorf("error type = %q, want NOT_FOUND", errs[0].Type)
	}
}

const enableAutoMergeMutation = `mutation Auto($id: ID!) {
  enablePullRequestAutoMerge(input: {pullRequestId: $id}) { pullRequest { number } }
}`

const createBranchProtectionMutation = `mutation BPR($repo: ID!) {
  createBranchProtectionRule(input: {repositoryId: $repo, pattern: "main"}) {
    branchProtectionRule { id }
  }
}`

// TestUnsupportedMutationsErrorHonestly confirms R02-26: mutations Githome does
// not implement return GitHub's UNPROCESSABLE error rather than a fake success
// that would have a client believe a change took effect.
func TestUnsupportedMutationsErrorHonestly(t *testing.T) {
	fx := reviewGraphQLServer(t)
	prID := graphqlID(t, fx.srv, fx.ownerToken, prIDByNumberQuery,
		map[string]any{"owner": "octocat", "name": "hello", "number": 1}, "repository", "pullRequest", "id")
	repoID := graphqlID(t, fx.srv, fx.ownerToken, repoIDQuery,
		map[string]any{"owner": "octocat", "name": "hello"}, "repository", "id")

	got := post(t, fx.srv, fx.ownerToken, enableAutoMergeMutation, map[string]any{"id": prID})
	errs := errorMessages(t, got)
	if len(errs) == 0 || errs[0].Type != "UNPROCESSABLE" {
		t.Fatalf("enablePullRequestAutoMerge should report UNPROCESSABLE, got %s", got)
	}

	got = post(t, fx.srv, fx.ownerToken, createBranchProtectionMutation, map[string]any{"repo": repoID})
	errs = errorMessages(t, got)
	if len(errs) == 0 || errs[0].Type != "UNPROCESSABLE" {
		t.Fatalf("createBranchProtectionRule should report UNPROCESSABLE, got %s", got)
	}
}

const requestReviewsMutation = `mutation Req($pr: ID!, $users: [ID!]!) {
  requestReviews(input: {pullRequestId: $pr, userIds: $users, union: true}) {
    requestedReviewersEdge { node { login } }
    pullRequest { number }
  }
}`

const reviewRequestsQuery = `query RR($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewRequests(first: 10) {
        totalCount
        nodes { requestedReviewer { __typename ... on User { login } } }
      }
    }
  }
}`

// TestRequestReviewsPersists confirms R02-26: requestReviews actually records
// the requested reviewer through the pull service — the payload edge names the
// reviewer and a follow-up query sees them in reviewRequests — rather than
// reporting a success that persisted nothing.
func TestRequestReviewsPersists(t *testing.T) {
	fx := reviewGraphQLServer(t)
	prID := graphqlID(t, fx.srv, fx.ownerToken, prIDByNumberQuery,
		map[string]any{"owner": "octocat", "name": "hello", "number": 1}, "repository", "pullRequest", "id")
	hubotID := graphqlID(t, fx.srv, fx.ownerToken, userIDQuery, map[string]any{"login": "hubot"}, "user", "id")

	got := post(t, fx.srv, fx.ownerToken, requestReviewsMutation, map[string]any{
		"pr": prID, "users": []string{hubotID},
	})
	var env struct {
		Data struct {
			RequestReviews struct {
				RequestedReviewersEdge struct {
					Node struct {
						Login string `json:"login"`
					} `json:"node"`
				} `json:"requestedReviewersEdge"`
			} `json:"requestReviews"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal requestReviews: %v\n%s", err, got)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("requestReviews returned errors: %v\n%s", env.Errors, got)
	}
	if env.Data.RequestReviews.RequestedReviewersEdge.Node.Login != "hubot" {
		t.Errorf("requestedReviewersEdge login = %q, want hubot",
			env.Data.RequestReviews.RequestedReviewersEdge.Node.Login)
	}

	// The reviewer must survive the mutation: reading the pull request back must
	// list hubot among the requested reviewers.
	got = post(t, fx.srv, fx.ownerToken, reviewRequestsQuery,
		map[string]any{"owner": "octocat", "name": "hello", "number": 1})
	var rr struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewRequests struct {
						TotalCount int `json:"totalCount"`
						Nodes      []struct {
							RequestedReviewer struct {
								Typename string `json:"__typename"`
								Login    string `json:"login"`
							} `json:"requestedReviewer"`
						} `json:"nodes"`
					} `json:"reviewRequests"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got, &rr); err != nil {
		t.Fatalf("unmarshal reviewRequests: %v\n%s", err, got)
	}
	nodes := rr.Data.Repository.PullRequest.ReviewRequests.Nodes
	found := false
	for _, n := range nodes {
		if n.RequestedReviewer.Login == "hubot" {
			found = true
		}
	}
	if !found {
		t.Errorf("hubot not found in persisted reviewRequests: %s", got)
	}
}

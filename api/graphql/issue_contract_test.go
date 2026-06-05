package graphql_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	graphqlapi "github.com/tamnd/githome/api/graphql"
	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/jsondiff"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// The issue documents gh issue view and gh issue list send, reduced to the
// fields those commands select. The contract is that each resolves field for
// field against its recorded golden.
const issueViewQuery = `query IssueView($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      id
      number
      title
      body
      state
      url
      author { login url }
      labels(first: 10) { totalCount nodes { name color } }
      comments(first: 10) { totalCount nodes { body author { login } } }
    }
  }
}`

const issueListQuery = `query IssueList($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    issues(first: 10, states: [OPEN]) {
      totalCount
      nodes { number title state }
      edges { cursor node { number } }
      pageInfo { hasNextPage hasPreviousPage startCursor endCursor }
    }
  }
}`

const createIssueMutation = `mutation CreateIssue($repo: ID!, $title: String!, $body: String) {
  createIssue(input: {repositoryId: $repo, title: $title, body: $body}) {
    issue { number title body state author { login } }
  }
}`

const closeIssueMutation = `mutation CloseIssue($id: ID!) {
  closeIssue(input: {issueId: $id, stateReason: COMPLETED}) {
    issue { number state stateReason closed }
  }
}`

const reopenIssueMutation = `mutation ReopenIssue($id: ID!) {
  reopenIssue(input: {issueId: $id}) {
    issue { number state closed }
  }
}`

const addCommentMutation = `mutation AddComment($id: ID!, $body: String!) {
  addComment(input: {subjectId: $id, body: $body}) {
    commentEdge { node { body author { login } } }
  }
}`

// issueServer seeds a store with octocat, a PAT, the hello repo, and two open
// issues, the first carrying a comment. It returns the running server and the
// token the documents authenticate with.
func issueServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	ctx := context.Background()

	st, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "githome.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	u := &store.UserRow{Login: "octocat", Type: "User"}
	if err := st.InsertUser(ctx, u); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	g, err := auth.GenerateToken(auth.PrefixClassicPAT)
	if err != nil {
		t.Fatal(err)
	}
	hash := g.Hash
	if err := st.InsertToken(ctx, &store.TokenRow{
		UserPK: &u.PK, TokenHash: hash[:], TokenPrefix: auth.PrefixClassicPAT,
		LastEight: g.Last8, Kind: "pat", Scopes: "repo",
	}); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	desc := "the hello repo"
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	repo := &store.RepoRow{OwnerPK: u.PK, Name: "hello", Description: &desc, DefaultBranch: "master", PushedAt: &when}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("insert repo: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	commitOne(t, gitStore.Dir(repo.PK), when)

	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)

	body1 := "the first issue body"
	if _, err := issueSvc.CreateIssue(ctx, u.PK, "octocat", "hello", domain.IssueInput{Title: "first issue", Body: &body1}); err != nil {
		t.Fatalf("seed issue 1: %v", err)
	}
	if _, err := issueSvc.CreateComment(ctx, u.PK, "octocat", "hello", 1, "thanks for the report"); err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	body2 := "the second issue body"
	if _, err := issueSvc.CreateIssue(ctx, u.PK, "octocat", "hello", domain.IssueInput{Title: "second issue", Body: &body2}); err != nil {
		t.Fatalf("seed issue 2: %v", err)
	}

	authSvc := auth.NewService(st, "https://git.test.internal")
	t.Cleanup(authSvc.Close)

	h := graphqlapi.NewHandler(graphqlapi.Deps{
		Auth:       authSvc,
		Repos:      repoSvc,
		Issues:     issueSvc,
		URLs:       presenter.NewURLBuilder(graphqlURLs(t)),
		NodeFormat: nodeid.FormatNew,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, g.Plaintext
}

// repoNodeID fetches the hello repo's node ID through the schema, the way a
// client resolves an ID before passing it to a mutation.
func repoNodeID(t *testing.T, srv *httptest.Server, token string) string {
	t.Helper()
	got := post(t, srv, token, `query($o:String!,$n:String!){ repository(owner:$o,name:$n){ id } }`,
		map[string]any{"o": "octocat", "n": "hello"})
	return nodeFromPath(t, got, "repository", "id")
}

// issueNodeID fetches the node ID of an issue by number.
func issueNodeID(t *testing.T, srv *httptest.Server, token string, number int) string {
	t.Helper()
	got := post(t, srv, token, `query($o:String!,$n:String!,$num:Int!){ repository(owner:$o,name:$n){ issue(number:$num){ id } } }`,
		map[string]any{"o": "octocat", "n": "hello", "num": number})
	var env struct {
		Data struct {
			Repository struct {
				Issue struct {
					ID string `json:"id"`
				} `json:"issue"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal issue id: %v, body %s", err, got)
	}
	if env.Data.Repository.Issue.ID == "" {
		t.Fatalf("empty issue id, body %s", got)
	}
	return env.Data.Repository.Issue.ID
}

func nodeFromPath(t *testing.T, body []byte, field, key string) string {
	t.Helper()
	var env struct {
		Data map[string]map[string]string `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal %s.%s: %v, body %s", field, key, err, body)
	}
	v := env.Data[field][key]
	if v == "" {
		t.Fatalf("empty %s.%s, body %s", field, key, body)
	}
	return v
}

// assertGolden records the response under RECORD=1, or asserts it stays
// compatible with the recorded golden, ignoring the volatile id and timestamp
// fields jsondiff masks by default.
func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	if os.Getenv("RECORD") == "1" {
		norm := strings.ReplaceAll(string(got), "git.test.internal", "HOST")
		if err := os.WriteFile(filepath.Join("testdata", name), append([]byte(norm), '\n'), 0o644); err != nil {
			t.Fatalf("record %s: %v", name, err)
		}
		return
	}
	jsondiff.AssertCompatible(t, golden(t, name), got, jsondiff.Default("git.test.internal"))
}

// TestIssueView confirms gh issue view resolves an issue with its author, labels,
// and comments against the recorded golden.
func TestIssueView(t *testing.T) {
	srv, token := issueServer(t)
	got := post(t, srv, token, issueViewQuery, map[string]any{"owner": "octocat", "name": "hello", "number": 1})
	assertGolden(t, "issue_view.golden.json", got)
}

// TestIssueList confirms gh issue list resolves the open-issue connection with
// edges, cursors, and page info against the recorded golden.
func TestIssueList(t *testing.T) {
	srv, token := issueServer(t)
	got := post(t, srv, token, issueListQuery, map[string]any{"owner": "octocat", "name": "hello"})
	assertGolden(t, "issue_list.golden.json", got)
}

// TestCreateIssue confirms the createIssue mutation opens an issue and returns it.
func TestCreateIssue(t *testing.T) {
	srv, token := issueServer(t)
	id := repoNodeID(t, srv, token)
	got := post(t, srv, token, createIssueMutation, map[string]any{"repo": id, "title": "a new issue", "body": "from graphql"})
	assertGolden(t, "issue_create.golden.json", got)
}

// TestCloseIssue confirms the closeIssue mutation closes an issue with a reason.
func TestCloseIssue(t *testing.T) {
	srv, token := issueServer(t)
	id := issueNodeID(t, srv, token, 1)
	got := post(t, srv, token, closeIssueMutation, map[string]any{"id": id})
	assertGolden(t, "issue_close.golden.json", got)
}

// TestReopenIssue confirms the reopenIssue mutation reopens a closed issue.
func TestReopenIssue(t *testing.T) {
	srv, token := issueServer(t)
	id := issueNodeID(t, srv, token, 1)
	if _, err := postErr(post(t, srv, token, closeIssueMutation, map[string]any{"id": id})); err != nil {
		t.Fatalf("pre-close: %v", err)
	}
	got := post(t, srv, token, reopenIssueMutation, map[string]any{"id": id})
	assertGolden(t, "issue_reopen.golden.json", got)
}

// TestAddComment confirms the addComment mutation comments on an issue and
// returns the new comment as a connection edge.
func TestAddComment(t *testing.T) {
	srv, token := issueServer(t)
	id := issueNodeID(t, srv, token, 1)
	got := post(t, srv, token, addCommentMutation, map[string]any{"id": id, "body": "a graphql comment"})
	assertGolden(t, "issue_comment.golden.json", got)
}

// postErr surfaces a top-level GraphQL errors array so a precondition mutation
// fails the test loudly instead of silently.
func postErr(body []byte) ([]byte, error) {
	var env struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return body, err
	}
	if len(env.Errors) > 0 {
		return body, &graphqlError{env.Errors[0].Message}
	}
	return body, nil
}

type graphqlError struct{ msg string }

func (e *graphqlError) Error() string { return e.msg }

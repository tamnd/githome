package graphql_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	graphqlapi "github.com/tamnd/githome/api/graphql"
	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// The pull request document gh pr view sends, reduced to the fields the command
// selects: the head and base, the merge view, and the commits and files
// connections each resolver pages on demand.
const pullViewQuery = `query PullView($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      id
      number
      title
      body
      state
      url
      isDraft
      merged
      mergeable
      mergeStateStatus
      baseRefName
      headRefName
      additions
      deletions
      changedFiles
      author { login }
      commits(first: 10) { totalCount nodes { commit { messageHeadline oid } } }
      files(first: 10) { totalCount nodes { path additions deletions changeType } }
    }
  }
}`

const pullListQuery = `query PullList($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    pullRequests(first: 10, states: [OPEN]) {
      totalCount
      nodes { number title state }
      edges { cursor node { number } }
      pageInfo { hasNextPage hasPreviousPage startCursor endCursor }
    }
  }
}`

const pullMergeableQuery = `query Mergeable($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) { mergeable }
  }
}`

// The close and reopen documents gh pr close and gh pr reopen send
// (cli/cli api/queries_pr.go PullRequestClose / PullRequestReopen).
const pullCloseMutation = `mutation PullRequestClose($id: ID!) {
  closePullRequest(input: {pullRequestId: $id}) {
    pullRequest { id state }
  }
}`

const pullReopenMutation = `mutation PullRequestReopen($id: ID!) {
  reopenPullRequest(input: {pullRequestId: $id}) {
    pullRequest { id state }
  }
}`

const pullIDQuery = `query ID($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) { id }
  }
}`

// The search shape gh pr status reads its created and review-requested buckets
// with (cli/cli api/queries_pr.go PullRequestStatus): an ISSUE-typed search
// whose is:pr results must come back as PullRequest nodes under edges { node }.
const prStatusSearchQuery = `query PullRequestStatus($q: String!) {
  search(query: $q, type: ISSUE, first: 10) {
    issueCount
    edges {
      node {
        __typename
        ... on PullRequest { number title state headRefName isDraft }
        ... on Issue { number title }
      }
    }
  }
}`

type pullFixture struct {
	srv     *httptest.Server
	token   string
	pulls   *domain.PRService
	st      *store.Store
	ownerPK int64
	ctx     context.Context
}

// pullServer seeds a store with octocat, a PAT, the hello repo, and a bare
// repository with a feature branch one commit ahead of main. It opens one pull
// request from feature into main and returns the running server plus the pull
// request service so a test can run the mergeability recompute the worker would.
func pullServer(t *testing.T) pullFixture {
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
	repo := &store.RepoRow{OwnerPK: u.PK, Name: "hello", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("insert repo: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	pullBareRepo(t, gitStore, repo.PK)

	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	pullSvc := domain.NewPRService(st, repoSvc, issueSvc, gitStore)
	searchSvc := domain.NewSearchService(st, repoSvc, issueSvc, gitStore)

	body := "It adds a feature."
	if _, err := pullSvc.CreatePR(ctx, u.PK, "octocat", "hello", domain.PRInput{
		Title: "Add a feature", Body: &body, Base: "main", Head: "feature",
	}); err != nil {
		t.Fatalf("seed pull: %v", err)
	}
	issueBody := "Something is off."
	if _, err := issueSvc.CreateIssue(ctx, u.PK, "octocat", "hello", domain.IssueInput{
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
		Search:     searchSvc,
		URLs:       presenter.NewURLBuilder(graphqlURLs(t)),
		NodeFormat: nodeid.FormatNew,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return pullFixture{srv: srv, token: g.Plaintext, pulls: pullSvc, st: st, ownerPK: u.PK, ctx: ctx}
}

// pullBareRepo builds a bare repository at gitStore.Dir(pk) with main and a
// feature branch one commit ahead, a clean merge. Commit times are pinned so the
// shas are stable and the recorded goldens stay valid.
func pullBareRepo(t *testing.T, gitStore *git.Store, pk int64) {
	t.Helper()
	src := t.TempDir()
	gitExec(t, src, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, src, "add", "README.md")
	gitExec(t, src, "commit", "-q", "-m", "initial commit")
	gitExec(t, src, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(src, "feature.txt"), []byte("a feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, src, "add", "feature.txt")
	gitExec(t, src, "commit", "-q", "-m", "add a feature")
	gitExec(t, src, "checkout", "-q", "main")

	bare := gitStore.Dir(pk)
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	gitExec(t, "", "clone", "-q", "--bare", src, bare)
}

func gitExec(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=Octo Cat", "GIT_AUTHOR_EMAIL=octo@example.com",
		"GIT_COMMITTER_NAME=Octo Cat", "GIT_COMMITTER_EMAIL=octo@example.com",
		"GIT_AUTHOR_DATE=2026-01-02T03:04:05Z", "GIT_COMMITTER_DATE=2026-01-02T03:04:05Z",
	)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

// recompute runs the mergeability the worker would run for pull request number.
func (fx pullFixture) recompute(t *testing.T, number int64) {
	t.Helper()
	iss, err := fx.st.GetIssueByNumber(fx.ctx, 1, number)
	if err != nil {
		t.Fatalf("GetIssueByNumber: %v", err)
	}
	if err := fx.pulls.RecomputeMergeability(fx.ctx, iss.PK); err != nil {
		t.Fatalf("RecomputeMergeability: %v", err)
	}
}

// TestPullView confirms gh pr view resolves a pull request with its merge view
// and its commits and files connections against the recorded golden.
func TestPullView(t *testing.T) {
	fx := pullServer(t)
	fx.recompute(t, 1)
	got := post(t, fx.srv, fx.token, pullViewQuery, map[string]any{"owner": "octocat", "name": "hello", "number": 1})
	assertGolden(t, "pull_view.golden.json", got)
}

// TestPullList confirms gh pr list resolves the open pull request connection with
// edges, cursors, and page info against the recorded golden.
func TestPullList(t *testing.T) {
	fx := pullServer(t)
	got := post(t, fx.srv, fx.token, pullListQuery, map[string]any{"owner": "octocat", "name": "hello"})
	assertGolden(t, "pull_list.golden.json", got)
}

// TestMergeabilityPolling confirms the null-then-value contract a client polls:
// a freshly opened pull request reports UNKNOWN until the worker resolves it,
// then MERGEABLE once the recompute lands.
func TestMergeabilityPolling(t *testing.T) {
	fx := pullServer(t)

	before := post(t, fx.srv, fx.token, pullMergeableQuery, map[string]any{"owner": "octocat", "name": "hello", "number": 1})
	if got := mergeableField(t, before); got != "UNKNOWN" {
		t.Fatalf("fresh mergeable = %q, want UNKNOWN", got)
	}

	fx.recompute(t, 1)

	after := post(t, fx.srv, fx.token, pullMergeableQuery, map[string]any{"owner": "octocat", "name": "hello", "number": 1})
	if got := mergeableField(t, after); got != "MERGEABLE" {
		t.Fatalf("resolved mergeable = %q, want MERGEABLE", got)
	}
}

// TestSearchIsPRReturnsPullRequests confirms an ISSUE search carrying is:pr
// resolves PullRequest nodes and is:issue keeps them out, the split gh pr
// status depends on.
func TestSearchIsPRReturnsPullRequests(t *testing.T) {
	fx := pullServer(t)

	got := post(t, fx.srv, fx.token, prStatusSearchQuery, map[string]any{"q": "is:pr is:open author:octocat"})
	count, typename, number := searchFirstEdge(t, got)
	if count != 1 || typename != "PullRequest" || number != 1 {
		t.Fatalf("is:pr search = count %d, %s #%d, want 1 PullRequest #1, body %s", count, typename, number, got)
	}

	got = post(t, fx.srv, fx.token, prStatusSearchQuery, map[string]any{"q": "is:issue is:open author:octocat"})
	count, typename, number = searchFirstEdge(t, got)
	if count != 1 || typename != "Issue" || number != 2 {
		t.Fatalf("is:issue search = count %d, %s #%d, want 1 Issue #2, body %s", count, typename, number, got)
	}
}

// searchFirstEdge pulls the issueCount and the first edge's typename and number
// out of a search response.
func searchFirstEdge(t *testing.T, body []byte) (int, string, int) {
	t.Helper()
	var env struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Data struct {
			Search struct {
				IssueCount int `json:"issueCount"`
				Edges      []struct {
					Node struct {
						Typename string `json:"__typename"`
						Number   int    `json:"number"`
					} `json:"node"`
				} `json:"edges"`
			} `json:"search"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal search: %v, body %s", err, body)
	}
	if len(env.Errors) > 0 {
		t.Fatalf("search errors: %v", env.Errors)
	}
	if len(env.Data.Search.Edges) == 0 {
		t.Fatalf("no search edges, body %s", body)
	}
	first := env.Data.Search.Edges[0].Node
	return env.Data.Search.IssueCount, first.Typename, first.Number
}

// TestClosePullRequestRoundTrip confirms gh pr close and gh pr reopen flip the
// pull request state through the close and reopen mutations.
func TestClosePullRequestRoundTrip(t *testing.T) {
	fx := pullServer(t)
	idBody := post(t, fx.srv, fx.token, pullIDQuery, map[string]any{"owner": "octocat", "name": "hello", "number": 1})
	var idEnv struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ID string `json:"id"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(idBody, &idEnv); err != nil {
		t.Fatalf("unmarshal id: %v, body %s", err, idBody)
	}
	prID := idEnv.Data.Repository.PullRequest.ID
	if prID == "" {
		t.Fatalf("empty pull request id, body %s", idBody)
	}

	got := post(t, fx.srv, fx.token, pullCloseMutation, map[string]any{"id": prID})
	if state := mutatedPullState(t, got, "closePullRequest"); state != "CLOSED" {
		t.Fatalf("close left state %q, body %s", state, got)
	}
	got = post(t, fx.srv, fx.token, pullReopenMutation, map[string]any{"id": prID})
	if state := mutatedPullState(t, got, "reopenPullRequest"); state != "OPEN" {
		t.Fatalf("reopen left state %q, body %s", state, got)
	}
}

// mutatedPullState pulls the pull request state out of a close or reopen payload.
func mutatedPullState(t *testing.T, body []byte, field string) string {
	t.Helper()
	var env struct {
		Data map[string]struct {
			PullRequest struct {
				State string `json:"state"`
			} `json:"pullRequest"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal %s: %v, body %s", field, err, body)
	}
	return env.Data[field].PullRequest.State
}

// mergeableField pulls repository.pullRequest.mergeable out of a GraphQL response.
func mergeableField(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					Mergeable string `json:"mergeable"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal mergeable: %v, body %s", err, body)
	}
	return env.Data.Repository.PullRequest.Mergeable
}

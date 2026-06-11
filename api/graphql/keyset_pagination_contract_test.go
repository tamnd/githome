package graphql_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
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

// The contract under test: a client that walks a connection with first plus
// the endCursor from each page sees every item exactly once, in order, with
// hasNextPage flipping off on the last page. The cursors now carry a seek key
// so the walk resumes with a keyset query, and the old offset-only cursors a
// deployed client may still hold keep working.

const issuesPageQuery = `query($owner: String!, $name: String!, $first: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    issues(first: $first, after: $after, states: [OPEN]) {
      totalCount
      nodes { number }
      pageInfo { hasNextPage endCursor }
    }
  }
}`

const pullsPageQuery = `query($owner: String!, $name: String!, $first: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequests(first: $first, after: $after, states: [OPEN]) {
      totalCount
      nodes { number }
      pageInfo { hasNextPage endCursor }
    }
  }
}`

const commentsPageQuery = `query($owner: String!, $name: String!, $number: Int!, $first: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      comments(first: $first, after: $after) {
        totalCount
        nodes { body }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}`

// connPage is the slice of a connection a single request returns, reduced to
// what the walk asserts on.
type connPage struct {
	total   int
	numbers []int
	bodies  []string
	hasNext bool
	end     *string
}

func decodeConnPage(t *testing.T, body []byte, conn string) connPage {
	t.Helper()
	var env struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v, body %s", err, body)
	}
	if len(env.Errors) > 0 {
		t.Fatalf("unexpected errors: %v, body %s", env.Errors, body)
	}
	type connJSON struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			Number int    `json:"number"`
			Body   string `json:"body"`
		} `json:"nodes"`
		PageInfo struct {
			HasNextPage bool    `json:"hasNextPage"`
			EndCursor   *string `json:"endCursor"`
		} `json:"pageInfo"`
	}
	// Dig repository{...conn} or repository{issue{...conn}} by unmarshaling
	// the repository object into a loose map of raw messages.
	var repo map[string]json.RawMessage
	if err := json.Unmarshal(env.Data["repository"], &repo); err != nil {
		t.Fatalf("unmarshal repository: %v, body %s", err, body)
	}
	raw, ok := repo[conn]
	if !ok {
		var issue map[string]json.RawMessage
		if err := json.Unmarshal(repo["issue"], &issue); err != nil {
			t.Fatalf("unmarshal issue: %v, body %s", err, body)
		}
		raw = issue[conn]
	}
	var c connJSON
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal connection: %v, body %s", err, body)
	}
	p := connPage{total: c.TotalCount, hasNext: c.PageInfo.HasNextPage, end: c.PageInfo.EndCursor}
	for _, n := range c.Nodes {
		p.numbers = append(p.numbers, n.Number)
		p.bodies = append(p.bodies, n.Body)
	}
	return p
}

// walkConn pages through a connection one item at a time until hasNextPage
// goes false, returning every page in order. The cap guards a cursor bug from
// looping forever.
func walkConn(t *testing.T, srv *httptest.Server, token, query string, vars map[string]any, conn string) []connPage {
	t.Helper()
	var pages []connPage
	var after *string
	for range 20 {
		v := map[string]any{"first": 1}
		for k, val := range vars {
			v[k] = val
		}
		if after != nil {
			v["after"] = *after
		}
		p := decodeConnPage(t, post(t, srv, token, query, v), conn)
		pages = append(pages, p)
		if !p.hasNext {
			return pages
		}
		if p.end == nil {
			t.Fatalf("hasNextPage true with nil endCursor on page %d", len(pages))
		}
		after = p.end
	}
	t.Fatalf("connection %s did not terminate in 20 pages", conn)
	return nil
}

// TestIssueKeysetWalk walks the issue connection item by item through the
// seek-carrying cursors: each issue once, newest first, totals stable.
func TestIssueKeysetWalk(t *testing.T) {
	srv, token := issueServer(t)
	id := repoNodeID(t, srv, token)
	if _, err := postErr(post(t, srv, token, createIssueMutation, map[string]any{
		"repo": id, "title": "third issue", "body": "one more",
	})); err != nil {
		t.Fatalf("seed issue 3: %v", err)
	}

	pages := walkConn(t, srv, token, issuesPageQuery,
		map[string]any{"owner": "octocat", "name": "hello"}, "issues")

	var got []int
	for i, p := range pages {
		if p.total != 3 {
			t.Errorf("page %d: totalCount = %d, want 3", i+1, p.total)
		}
		got = append(got, p.numbers...)
	}
	want := []int{3, 2, 1}
	if len(got) != len(want) {
		t.Fatalf("walked numbers %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("walked numbers %v, want %v", got, want)
		}
	}
}

// TestIssueLegacyOffsetCursor confirms an offset-only cursor recorded before
// the seek key existed still pages: base64("gho:1") consumed one row, so the
// next page starts at the second-newest issue.
func TestIssueLegacyOffsetCursor(t *testing.T) {
	srv, token := issueServer(t)
	legacy := base64.StdEncoding.EncodeToString([]byte("gho:1"))
	p := decodeConnPage(t, post(t, srv, token, issuesPageQuery, map[string]any{
		"owner": "octocat", "name": "hello", "first": 1, "after": legacy,
	}), "issues")
	if len(p.numbers) != 1 || p.numbers[0] != 1 {
		t.Fatalf("legacy cursor page numbers = %v, want [1]", p.numbers)
	}
	if p.hasNext {
		t.Fatalf("legacy cursor page hasNextPage = true, want false")
	}
}

// TestCommentKeysetWalk walks an issue's comments one at a time. The comment
// connection exposes cursors only through pageInfo, so endCursor is the whole
// contract.
func TestCommentKeysetWalk(t *testing.T) {
	srv, token := issueServer(t)
	id := issueNodeID(t, srv, token, 1)
	for _, body := range []string{"second comment", "third comment"} {
		if _, err := postErr(post(t, srv, token, addCommentMutation, map[string]any{
			"id": id, "body": body,
		})); err != nil {
			t.Fatalf("seed comment %q: %v", body, err)
		}
	}

	pages := walkConn(t, srv, token, commentsPageQuery,
		map[string]any{"owner": "octocat", "name": "hello", "number": 1}, "comments")

	var got []string
	for i, p := range pages {
		if p.total != 3 {
			t.Errorf("page %d: totalCount = %d, want 3", i+1, p.total)
		}
		got = append(got, p.bodies...)
	}
	want := []string{"thanks for the report", "second comment", "third comment"}
	if len(got) != len(want) {
		t.Fatalf("walked bodies %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("walked bodies %v, want %v", got, want)
		}
	}
}

// TestPullRequestKeysetWalk walks the pull request connection through three
// open pulls, newest first.
func TestPullRequestKeysetWalk(t *testing.T) {
	srv, token := keysetPullServer(t)

	pages := walkConn(t, srv, token, pullsPageQuery,
		map[string]any{"owner": "octocat", "name": "hello"}, "pullRequests")

	var got []int
	for i, p := range pages {
		if p.total != 3 {
			t.Errorf("page %d: totalCount = %d, want 3", i+1, p.total)
		}
		got = append(got, p.numbers...)
	}
	want := []int{3, 2, 1}
	if len(got) != len(want) {
		t.Fatalf("walked numbers %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("walked numbers %v, want %v", got, want)
		}
	}
}

// keysetPullServer is pullServer with three feature branches and three open
// pull requests, enough rows for a multi-page cursor walk.
func keysetPullServer(t *testing.T) (*httptest.Server, string) {
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
	keysetBareRepo(t, gitStore, repo.PK)

	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	pullSvc := domain.NewPRService(st, repoSvc, issueSvc, gitStore)

	for i, head := range []string{"feature1", "feature2", "feature3"} {
		body := "feature body"
		if _, err := pullSvc.CreatePR(ctx, u.PK, "octocat", "hello", domain.PRInput{
			Title: head, Body: &body, Base: "main", Head: head,
		}); err != nil {
			t.Fatalf("seed pull %d: %v", i+1, err)
		}
	}

	authSvc := auth.NewService(st, "https://git.test.internal")
	t.Cleanup(authSvc.Close)

	h := graphqlapi.NewHandler(graphqlapi.Deps{
		Auth:       authSvc,
		Repos:      repoSvc,
		Issues:     issueSvc,
		Pulls:      pullSvc,
		URLs:       presenter.NewURLBuilder(graphqlURLs(t)),
		NodeFormat: nodeid.FormatNew,
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, g.Plaintext
}

// keysetBareRepo builds a bare repository with main and three feature
// branches, each one commit ahead with its own file.
func keysetBareRepo(t *testing.T, gitStore *git.Store, pk int64) {
	t.Helper()
	src := t.TempDir()
	gitExec(t, src, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, src, "add", "README.md")
	gitExec(t, src, "commit", "-q", "-m", "initial commit")
	for _, branch := range []string{"feature1", "feature2", "feature3"} {
		gitExec(t, src, "checkout", "-q", "-b", branch, "main")
		if err := os.WriteFile(filepath.Join(src, branch+".txt"), []byte(branch+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitExec(t, src, "add", branch+".txt")
		gitExec(t, src, "commit", "-q", "-m", "add "+branch)
	}
	gitExec(t, src, "checkout", "-q", "main")

	bare := gitStore.Dir(pk)
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	gitExec(t, "", "clone", "-q", "--bare", src, bare)
}

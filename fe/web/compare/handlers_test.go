package compare

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/go-git/go-billy/v5/util"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/assets"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
)

// fixedWhen pins every commit time so object ids and rendered dates are stable.
var fixedWhen = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// newFixture seeds one owner with one public repository whose history forks:
// master gains a commit after the point feature branched off, and feature adds
// its own file. The two compare forms answer differently over that shape: the
// three-dot diff (against the merge base) shows only feature's change, while
// the two-dot direct diff also shows master's own change, reversed.
func newFixture(t *testing.T) *httptest.Server {
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
	hello := &store.RepoRow{OwnerPK: u.PK, Name: "hello", DefaultBranch: "master"}
	if err := st.InsertRepo(ctx, hello); err != nil {
		t.Fatalf("insert hello: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	buildForkedHistory(t, gitStore.Dir(hello.PK))

	renderSet, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}

	h := New(Deps{
		Repos:  domain.NewRepoService(st, gitStore),
		Render: renderSet,
		View:   view.NewBuilder("Githome"),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	root := mizu.NewRouter()
	page := root.With(webmw.ColorMode())
	cg := page.With(h.Resolve)
	cg.Get("/{owner}/{repo}/compare", h.Picker)
	cg.Get("/{owner}/{repo}/compare/{basehead...}", h.Range)

	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)
	return srv
}

// buildForkedHistory writes the diverged shape into a fresh repository at dir:
// commit one on master (README.md), then feature branches there and adds
// feature.txt, then master adds master-only.txt. HEAD ends back on master.
func buildForkedHistory(t *testing.T, dir string) {
	t.Helper()
	r, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	fs := wt.Filesystem
	sig := &object.Signature{Name: "Octo Cat", Email: "octo@example.com", When: fixedWhen}

	write := func(name, body string) {
		if err := util.WriteFile(fs, name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add(name); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string) plumbing.Hash {
		h, err := wt.Commit(msg, &gogit.CommitOptions{Author: sig, Committer: sig})
		if err != nil {
			t.Fatalf("commit %q: %v", msg, err)
		}
		return h
	}

	write("README.md", "# Hello\n")
	root := commit("initial commit")

	if err := wt.Checkout(&gogit.CheckoutOptions{
		Hash: root, Branch: plumbing.NewBranchReferenceName("feature"), Create: true,
	}); err != nil {
		t.Fatalf("checkout feature: %v", err)
	}
	write("feature.txt", "the feature\n")
	commit("add the feature")

	if err := wt.Checkout(&gogit.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("master")}); err != nil {
		t.Fatalf("checkout master: %v", err)
	}
	write("master-only.txt", "moved on\n")
	commit("master moves on")
}

// get issues a GET and returns the response and body, never following
// redirects so a test can assert them directly.
func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp, string(b)
}

// TestCompareThreeDotVsTwoDot pins the two range forms apart: the canonical
// three-dot diff is against the merge base, so master's own change after the
// fork point stays out; the two-dot direct diff includes it.
func TestCompareThreeDotVsTwoDot(t *testing.T) {
	srv := newFixture(t)

	resp, body := get(t, srv, "/octocat/hello/compare/master...feature")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("three-dot: status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "feature.txt") {
		t.Error("three-dot diff is missing feature.txt")
	}
	if strings.Contains(body, "master-only.txt") {
		t.Error("three-dot diff shows master's own change; it must diff against the merge base")
	}

	resp, body = get(t, srv, "/octocat/hello/compare/master..feature")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("two-dot: status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "feature.txt") {
		t.Error("two-dot diff is missing feature.txt")
	}
	if !strings.Contains(body, "master-only.txt") {
		t.Error("two-dot diff is missing master-only.txt; it must diff the trees directly")
	}
}

// TestCompareSingleSide covers the bare-head form: the base falls back to the
// repository's default branch.
func TestCompareSingleSide(t *testing.T) {
	srv := newFixture(t)
	resp, body := get(t, srv, "/octocat/hello/compare/feature")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "feature.txt") {
		t.Error("single-side compare is missing feature.txt")
	}
}

// TestCompareQualifiedSides covers the owner:ref and owner:repo:ref grammar.
// With no cross-owner forks yet, a qualified side resolves only when it names
// this same repository; anything else is a clear 404.
func TestCompareQualifiedSides(t *testing.T) {
	srv := newFixture(t)

	for _, path := range []string{
		"/octocat/hello/compare/master...octocat:feature",
		"/octocat/hello/compare/master...octocat:hello:feature",
		"/octocat/hello/compare/Octocat:master...feature", // qualifier is case-insensitive like the URL
		"/octocat/hello/compare/octocat:hello:master..octocat:hello:feature",
	} {
		resp, body := get(t, srv, path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200", path, resp.StatusCode)
			continue
		}
		if !strings.Contains(body, "feature.txt") {
			t.Errorf("GET %s: body is missing feature.txt", path)
		}
	}

	for _, path := range []string{
		"/octocat/hello/compare/master...ghost:feature",         // unknown owner
		"/octocat/hello/compare/master...octocat:other:feature", // another repo
		"/octocat/hello/compare/ghost:master...feature",         // qualified base misses too
	} {
		resp, _ := get(t, srv, path)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, resp.StatusCode)
		}
	}
}

// TestCompareBadGrammar covers the strings that do not parse, and the ranges
// that parse but name no branch: all of them are the soft 404.
// TestCompareExpandAliases covers the PR-creation form gate: the bare range
// shows the "Create pull request" button, ?expand=1 swaps it for the creation
// form, and github.com's older ?quick_pull=1 spelling does the same, since the
// "Create pull request" buttons on github.com still emit quick_pull.
func TestCompareExpandAliases(t *testing.T) {
	srv := newFixture(t)

	_, collapsed := get(t, srv, "/octocat/hello/compare/master...feature")
	if strings.Contains(collapsed, "compare-create-pr") {
		t.Error("bare range showed the creation form before it was expanded")
	}
	if !strings.Contains(collapsed, "Create pull request") {
		t.Error("bare range is missing the Create pull request button")
	}

	for _, q := range []string{"?expand=1", "?quick_pull=1"} {
		resp, body := get(t, srv, "/octocat/hello/compare/master...feature"+q)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200", q, resp.StatusCode)
		}
		if !strings.Contains(body, "compare-create-pr") {
			t.Errorf("GET %s did not expand the creation form:\n%s", q, body)
		}
	}
}

func TestCompareBadGrammar(t *testing.T) {
	srv := newFixture(t)
	for _, path := range []string{
		"/octocat/hello/compare/...feature",
		"/octocat/hello/compare/master...",
		"/octocat/hello/compare/a:b:c:d...master",
		"/octocat/hello/compare/master...:feature",
		"/octocat/hello/compare/master...nosuchbranch",
	} {
		resp, _ := get(t, srv, path)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, resp.StatusCode)
		}
	}
}

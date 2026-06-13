package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/go-git/go-billy/v5/util"

	"github.com/tamnd/githome/store"
)

// seedUserToken inserts a fresh user with the given login and a repo-scoped PAT,
// returning the plaintext token. It is used to exercise access checks from a
// principal that is not the repository owner.
func seedUserToken(t *testing.T, st *store.Store, login string) string {
	t.Helper()
	u := &store.UserRow{Login: login, Type: "User"}
	if err := st.InsertUser(context.Background(), u); err != nil {
		t.Fatalf("insert user %s: %v", login, err)
	}
	return seedToken(t, st, u.PK)
}

// seedBranch creates branchName off parent in the fixture's git repo, writes
// files, and commits them, returning the new commit sha. It leaves the worktree
// checked back out on master so later reads in the same repo are unaffected.
func (fx repoFixture) seedBranch(t *testing.T, branchName string, parent string, files map[string]string) string {
	t.Helper()
	r, err := gogit.PlainOpen(fx.gitStore.Dir(fx.repoPK))
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	ref := plumbing.NewBranchReferenceName(branchName)
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Hash:   plumbing.NewHash(parent),
		Branch: ref,
		Create: true,
	}); err != nil {
		t.Fatalf("checkout %s: %v", branchName, err)
	}
	for path, content := range files {
		if err := util.WriteFile(wt.Filesystem, path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		if _, err := wt.Add(path); err != nil {
			t.Fatalf("add %s: %v", path, err)
		}
	}
	sig := &object.Signature{Name: "Octo Cat", Email: "octo@example.com", When: fixedWhen}
	h, err := wt.Commit("commit on "+branchName, &gogit.CommitOptions{Author: sig, Committer: sig})
	if err != nil {
		t.Fatalf("commit on %s: %v", branchName, err)
	}
	if err := wt.Checkout(&gogit.CheckoutOptions{Branch: plumbing.NewBranchReferenceName(fx.branch)}); err != nil {
		t.Fatalf("checkout back to %s: %v", fx.branch, err)
	}
	return h.String()
}

func TestRepoMergeContract(t *testing.T) {
	fx := repoServer(t)
	// A feature branch off master HEAD that only adds a new file merges cleanly.
	fx.seedBranch(t, "feature", fx.headSHA, map[string]string{"feature.txt": "new feature\n"})

	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/merges", fx.token,
		`{"base":"master","head":"feature","commit_message":"Merge feature"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	var out struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
		} `json:"commit"`
		Parents []struct {
			SHA string `json:"sha"`
		} `json:"parents"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v, body %s", err, body)
	}
	if out.SHA == "" {
		t.Fatalf("merge commit has no sha: %s", body)
	}
	if out.Commit.Message != "Merge feature" {
		t.Fatalf("commit message = %q, want %q", out.Commit.Message, "Merge feature")
	}
	if len(out.Parents) != 2 {
		t.Fatalf("merge commit has %d parents, want 2: %s", len(out.Parents), body)
	}
}

func TestRepoMergeNothingToMerge(t *testing.T) {
	fx := repoServer(t)
	// master already contains its own first commit, so there is nothing to merge.
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/merges", fx.token,
		`{"base":"master","head":"`+fx.firstSHA+`"}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d, want 204, body %s", resp.StatusCode, body)
	}
	if len(body) != 0 {
		t.Fatalf("204 carried a body: %s", body)
	}
}

func TestRepoMergeConflict(t *testing.T) {
	fx := repoServer(t)
	// Two branches off the first commit edit README.md differently; merging one
	// into the other cannot apply cleanly.
	fx.seedBranch(t, "conflict-base", fx.firstSHA, map[string]string{"README.md": "# Base side\n"})
	fx.seedBranch(t, "conflict-head", fx.firstSHA, map[string]string{"README.md": "# Head side\n"})

	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/merges", fx.token,
		`{"base":"conflict-base","head":"conflict-head"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status %d, want 409, body %s", resp.StatusCode, body)
	}
}

func TestRepoMergeMissing(t *testing.T) {
	fx := repoServer(t)
	for _, tc := range []struct {
		name string
		body string
	}{
		{"unknown base", `{"base":"nope","head":"master"}`},
		{"unknown head", `{"base":"master","head":"nope"}`},
	} {
		resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/merges", fx.token, tc.body)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404, body %s", tc.name, resp.StatusCode, body)
		}
	}
}

func TestRepoMergeValidation(t *testing.T) {
	fx := repoServer(t)
	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing base", `{"head":"feature"}`},
		{"missing head", `{"base":"master"}`},
		{"missing both", `{}`},
	} {
		resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/merges", fx.token, tc.body)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status %d, want 422, body %s", tc.name, resp.StatusCode, body)
		}
	}
}

func TestRepoDispatchContract(t *testing.T) {
	fx := repoServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/dispatches", fx.token,
		`{"event_type":"deploy","client_payload":{"env":"prod"}}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d, want 204, body %s", resp.StatusCode, body)
	}
	if len(body) != 0 {
		t.Fatalf("204 carried a body: %s", body)
	}
}

func TestRepoDispatchValidation(t *testing.T) {
	fx := repoServer(t)
	long := make([]byte, 101)
	for i := range long {
		long[i] = 'x'
	}
	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing event_type", `{"client_payload":{}}`},
		{"oversized event_type", `{"event_type":"` + string(long) + `"}`},
	} {
		resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/dispatches", fx.token, tc.body)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status %d, want 422, body %s", tc.name, resp.StatusCode, body)
		}
	}
}

func TestRepoDispatchForbidden(t *testing.T) {
	fx := repoServer(t)
	other := seedUserToken(t, fx.st, "mallory")
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/dispatches", other,
		`{"event_type":"deploy"}`)
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 403 or 404, body %s", resp.StatusCode, body)
	}
}

func TestRepoByIDContract(t *testing.T) {
	fx := repoServer(t)
	// The owner/name view carries the canonical id; the by-id lookup must return
	// the same full object.
	resp, byName := authedSend(t, fx.srv, http.MethodGet, "/repos/octocat/hello", fx.token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("by name: status %d, body %s", resp.StatusCode, byName)
	}
	var named struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(byName, &named); err != nil {
		t.Fatalf("unmarshal by name: %v", err)
	}
	if named.ID == 0 {
		t.Fatalf("repo id is zero: %s", byName)
	}

	resp, byID := authedSend(t, fx.srv, http.MethodGet, "/repositories/"+itoa(named.ID), fx.token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("by id: status %d, body %s", resp.StatusCode, byID)
	}
	if string(byID) != string(byName) {
		t.Fatalf("by-id object differs from by-name:\n by-id: %s\n by-name: %s", byID, byName)
	}
}

func TestRepoByIDNotFound(t *testing.T) {
	fx := repoServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodGet, "/repositories/999999", fx.token, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404, body %s", resp.StatusCode, body)
	}
}

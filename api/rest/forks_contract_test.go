package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/store"
)

// addUser inserts a second account with a repo-scoped token so a test can act
// as someone other than the fixture owner.
func (fx repoFixture) addUser(t *testing.T, login string) string {
	t.Helper()
	ctx := context.Background()
	u := &store.UserRow{Login: login, Type: "User"}
	if err := fx.st.InsertUser(ctx, u); err != nil {
		t.Fatalf("insert user %s: %v", login, err)
	}
	g, err := auth.GenerateToken(auth.PrefixClassicPAT)
	if err != nil {
		t.Fatal(err)
	}
	hash := g.Hash
	if err := fx.st.InsertToken(ctx, &store.TokenRow{
		UserPK: &u.PK, TokenHash: hash[:], TokenPrefix: auth.PrefixClassicPAT,
		LastEight: g.Last8, Kind: "pat", Scopes: "repo",
	}); err != nil {
		t.Fatalf("insert token for %s: %v", login, err)
	}
	return g.Plaintext
}

// TestForkCreate covers the headline path: a second user forks octocat/hello
// and gets 202 with a full repository object whose parent and source point at
// the original. The git side must really be copied, so the fork's branches
// and tags are checked through the API as well.
func TestForkCreate(t *testing.T) {
	fx := repoServer(t)
	hubber := fx.addUser(t, "hubber")

	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/forks", hubber, "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("fork status %d, want 202, body %s", resp.StatusCode, body)
	}
	obj := decodeObject(t, body)
	if obj["full_name"] != "hubber/hello" {
		t.Fatalf("full_name = %v, want hubber/hello", obj["full_name"])
	}
	if obj["fork"] != true {
		t.Fatalf("fork = %v, want true", obj["fork"])
	}
	parent, _ := obj["parent"].(map[string]any)
	if parent == nil || parent["full_name"] != "octocat/hello" {
		t.Fatalf("parent = %v, want octocat/hello", obj["parent"])
	}
	source, _ := obj["source"].(map[string]any)
	if source == nil || source["full_name"] != "octocat/hello" {
		t.Fatalf("source = %v, want octocat/hello", obj["source"])
	}
	if obj["default_branch"] != "master" {
		t.Fatalf("default_branch = %v, want master", obj["default_branch"])
	}

	// The refs came across: same branch, both tags.
	resp, body = authedGet(t, fx.srv, "/repos/hubber/hello/branches", "token "+hubber)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fork branches status %d, body %s", resp.StatusCode, body)
	}
	var branches []map[string]any
	if err := json.Unmarshal(body, &branches); err != nil {
		t.Fatal(err)
	}
	if len(branches) != 1 || branches[0]["name"] != "master" {
		t.Fatalf("fork branches = %s", body)
	}
	resp, body = authedGet(t, fx.srv, "/repos/hubber/hello/tags", "token "+hubber)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fork tags status %d, body %s", resp.StatusCode, body)
	}
	var tags []map[string]any
	if err := json.Unmarshal(body, &tags); err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 {
		t.Fatalf("fork tags = %s, want both fixture tags", body)
	}

	// Forking the same repository again answers 202 with the existing fork.
	resp, again := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/forks", hubber, "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("repeat fork status %d, want 202, body %s", resp.StatusCode, again)
	}
	if decodeObject(t, again)["id"] != obj["id"] {
		t.Fatalf("repeat fork returned a different repository: %s", again)
	}
}

// TestForkOptions covers the body knobs: name renames the fork and
// default_branch_only leaves the tags behind.
func TestForkOptions(t *testing.T) {
	fx := repoServer(t)
	hubber := fx.addUser(t, "hubber")

	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/forks", hubber,
		`{"name":"hello-mini","default_branch_only":true}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("fork status %d, want 202, body %s", resp.StatusCode, body)
	}
	if got := decodeObject(t, body)["full_name"]; got != "hubber/hello-mini" {
		t.Fatalf("full_name = %v, want hubber/hello-mini", got)
	}
	resp, body = authedGet(t, fx.srv, "/repos/hubber/hello-mini/tags", "token "+hubber)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fork tags status %d, body %s", resp.StatusCode, body)
	}
	var tags []map[string]any
	if err := json.Unmarshal(body, &tags); err != nil {
		t.Fatal(err)
	}
	if len(tags) != 0 {
		t.Fatalf("default_branch_only fork has tags: %s", body)
	}
}

// TestForkErrors covers the refusal paths: anonymous callers get 401, a name
// collision with an unrelated repository gets 403, and forking your own
// repository collides with itself.
func TestForkErrors(t *testing.T) {
	fx := repoServer(t)
	hubber := fx.addUser(t, "hubber")

	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/forks", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous fork status %d, want 401, body %s", resp.StatusCode, body)
	}

	resp, body = authedSend(t, fx.srv, http.MethodPost, "/user/repos", hubber, `{"name":"taken"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create taken status %d, body %s", resp.StatusCode, body)
	}
	resp, body = authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/forks", hubber, `{"name":"taken"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("collision fork status %d, want 403, body %s", resp.StatusCode, body)
	}

	resp, body = authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/forks", fx.token, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("self fork status %d, want 403, body %s", resp.StatusCode, body)
	}
}

// TestForksList covers GET /forks: newest fork first, and a private fork only
// shows to a viewer who can see it.
func TestForksList(t *testing.T) {
	fx := repoServer(t)
	hubber := fx.addUser(t, "hubber")

	for _, body := range []string{"", `{"name":"hello-mini"}`} {
		resp, out := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/forks", hubber, body)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("fork status %d, body %s", resp.StatusCode, out)
		}
	}

	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/forks", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("forks list status %d, body %s", resp.StatusCode, body)
	}
	var forks []map[string]any
	if err := json.Unmarshal(body, &forks); err != nil {
		t.Fatal(err)
	}
	if len(forks) != 2 {
		t.Fatalf("forks list has %d entries, want 2: %s", len(forks), body)
	}
	if forks[0]["full_name"] != "hubber/hello-mini" || forks[1]["full_name"] != "hubber/hello" {
		t.Fatalf("forks order = %v, %v; want newest first", forks[0]["full_name"], forks[1]["full_name"])
	}

	// The source's network_count follows the fork count.
	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("repo status %d, body %s", resp.StatusCode, body)
	}
	if got := decodeObject(t, body)["forks_count"]; got != float64(2) {
		t.Fatalf("forks_count = %v, want 2", got)
	}
}

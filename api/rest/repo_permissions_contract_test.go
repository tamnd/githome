package rest

import (
	"context"
	"net/http"
	"testing"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/store"
)

// addFixtureUser seeds another user with a classic PAT on the repo fixture's
// store and returns the user row and plaintext token.
func addFixtureUser(t *testing.T, fx repoFixture, login string) (*store.UserRow, string) {
	t.Helper()
	ctx := context.Background()
	u := &store.UserRow{Login: login, Type: "User"}
	if err := fx.st.InsertUser(ctx, u); err != nil {
		t.Fatalf("insert %s: %v", login, err)
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
		t.Fatalf("insert token: %v", err)
	}
	return u, g.Plaintext
}

// permsOf pulls the permissions block out of a repository response.
func permsOf(t *testing.T, body []byte) map[string]any {
	t.Helper()
	m := decodeObject(t, body)
	p, ok := m["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions block missing: %s", body)
	}
	return p
}

// TestRepoPermissionsCollaborator walks the collaborator grant ladder on a
// private repository: invisible before the grant, then the permission block
// mirrors the granted role rather than collapsing to read-only.
func TestRepoPermissionsCollaborator(t *testing.T) {
	fx := repoServer(t)
	ctx := context.Background()
	hubber, hubToken := addFixtureUser(t, fx, "hubber")

	// A random authed user on a public repo gets pull only.
	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello", "token "+hubToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("public get: status %d", resp.StatusCode)
	}
	p := permsOf(t, body)
	if p["pull"] != true || p["push"] != false || p["admin"] != false {
		t.Errorf("public perms = %v, want pull only", p)
	}

	// Make the repo private: hubber loses sight of it entirely.
	resp, body = authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello", fx.token, `{"private":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("make private: status %d, body %s", resp.StatusCode, body)
	}
	resp, _ = authedGet(t, fx.srv, "/repos/octocat/hello", "token "+hubToken)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("private pre-grant: status %d, want 404", resp.StatusCode)
	}

	// A push grant makes it visible and the block reflects the role.
	if err := fx.st.UpsertCollaborator(ctx, fx.repoPK, hubber.PK, "push"); err != nil {
		t.Fatalf("grant push: %v", err)
	}
	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello", "token "+hubToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("private post-grant: status %d", resp.StatusCode)
	}
	p = permsOf(t, body)
	if p["push"] != true || p["triage"] != true || p["pull"] != true {
		t.Errorf("push perms = %v, want push+triage+pull", p)
	}
	if p["admin"] != false || p["maintain"] != false {
		t.Errorf("push perms = %v, want admin/maintain false", p)
	}
	// Push access is not admin access: the merge settings stay hidden.
	m := decodeObject(t, body)
	if _, ok := m["allow_squash_merge"]; ok {
		t.Errorf("push collaborator must not see allow_squash_merge")
	}

	// Triage narrows the block; maintain widens it short of admin.
	for role, want := range map[string]map[string]bool{
		"triage":   {"admin": false, "maintain": false, "push": false, "triage": true, "pull": true},
		"maintain": {"admin": false, "maintain": true, "push": true, "triage": true, "pull": true},
		"admin":    {"admin": true, "maintain": true, "push": true, "triage": true, "pull": true},
	} {
		if err := fx.st.UpsertCollaborator(ctx, fx.repoPK, hubber.PK, role); err != nil {
			t.Fatalf("grant %s: %v", role, err)
		}
		_, body = authedGet(t, fx.srv, "/repos/octocat/hello", "token "+hubToken)
		p = permsOf(t, body)
		for k, v := range want {
			if p[k] != v {
				t.Errorf("%s perms[%s] = %v, want %v", role, k, p[k], v)
			}
		}
	}

	// The repo list renders the same resolved block per item.
	resp, body = authedGet(t, fx.srv, "/users/octocat/repos", "token "+hubToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status %d", resp.StatusCode)
	}
}

// TestRepoPermissionsLegacyNames checks the legacy read/write grant names
// normalize to pull/push in the rendered block.
func TestRepoPermissionsLegacyNames(t *testing.T) {
	fx := repoServer(t)
	ctx := context.Background()
	hubber, hubToken := addFixtureUser(t, fx, "hubber")

	if err := fx.st.UpsertCollaborator(ctx, fx.repoPK, hubber.PK, "write"); err != nil {
		t.Fatalf("grant write: %v", err)
	}
	_, body := authedGet(t, fx.srv, "/repos/octocat/hello", "token "+hubToken)
	p := permsOf(t, body)
	if p["push"] != true {
		t.Errorf("write grant: perms = %v, want push true", p)
	}

	if err := fx.st.UpsertCollaborator(ctx, fx.repoPK, hubber.PK, "read"); err != nil {
		t.Fatalf("grant read: %v", err)
	}
	_, body = authedGet(t, fx.srv, "/repos/octocat/hello", "token "+hubToken)
	p = permsOf(t, body)
	if p["push"] != false || p["pull"] != true {
		t.Errorf("read grant: perms = %v, want pull only", p)
	}
}

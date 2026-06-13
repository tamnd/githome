package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tamnd/githome/store"
)

// seedSecondUser inserts another user the org membership tests can grant and
// revoke against.
func seedSecondUser(t *testing.T, fx repoFixture, login string) {
	t.Helper()
	u := &store.UserRow{Login: login, Type: "User"}
	if err := fx.st.InsertUser(context.Background(), u); err != nil {
		t.Fatalf("insert %s: %v", login, err)
	}
}

// TestOrgGetOrganizationShape checks GET /orgs/{org} serves the org presenter
// shape rather than the user one: type Organization, an O-kind node id, and
// the org-flavored URL family including members_url.
func TestOrgGetOrganizationShape(t *testing.T) {
	fx := repoServer(t)

	resp, body := authedGet(t, fx.srv, "/orgs/octocat", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	var org struct {
		Login      string          `json:"login"`
		NodeID     string          `json:"node_id"`
		Type       string          `json:"type"`
		URL        string          `json:"url"`
		MembersURL string          `json:"members_url"`
		HooksURL   string          `json:"hooks_url"`
		ReposURL   string          `json:"repos_url"`
		HTMLURL    string          `json:"html_url"`
		Desc       json.RawMessage `json:"description"`
	}
	if err := json.Unmarshal(body, &org); err != nil {
		t.Fatalf("decode: %v from %s", err, body)
	}
	if org.Type != "Organization" {
		t.Errorf("type = %q, want Organization", org.Type)
	}
	if !strings.HasPrefix(org.NodeID, "O_") {
		t.Errorf("node_id = %q, want O_ prefix", org.NodeID)
	}
	if !strings.HasSuffix(org.URL, "/orgs/octocat") {
		t.Errorf("url = %q, want /orgs/octocat suffix", org.URL)
	}
	if !strings.HasSuffix(org.MembersURL, "/orgs/octocat/members{/member}") {
		t.Errorf("members_url = %q", org.MembersURL)
	}
	if !strings.HasSuffix(org.HooksURL, "/orgs/octocat/hooks") {
		t.Errorf("hooks_url = %q", org.HooksURL)
	}
	if !strings.HasSuffix(org.ReposURL, "/orgs/octocat/repos") {
		t.Errorf("repos_url = %q", org.ReposURL)
	}
	if len(org.Desc) == 0 {
		t.Errorf("description key missing from %s", body)
	}
}

// TestOrgMembershipLifecycle drives the persisted org membership the review
// asked for: the member check 404s for a non-member, a membership PUT makes
// it a 204 and the member appears in the listing, and removal reverts it.
func TestOrgMembershipLifecycle(t *testing.T) {
	fx := repoServer(t)
	seedSecondUser(t, fx, "hubot")

	// The backing account is the org's built-in member.
	if resp, body := authedGet(t, fx.srv, "/orgs/octocat/members/octocat", "token "+fx.token); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("self member check: status %d, body %s", resp.StatusCode, body)
	}
	// A user with no membership is not.
	if resp, _ := authedGet(t, fx.srv, "/orgs/octocat/members/hubot", "token "+fx.token); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("non-member check: status %d, want 404", resp.StatusCode)
	}
	if resp, _ := authedGet(t, fx.srv, "/orgs/octocat/memberships/hubot", "token "+fx.token); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("non-member membership get: status %d, want 404", resp.StatusCode)
	}

	resp, body := authedSend(t, fx.srv, http.MethodPut, "/orgs/octocat/memberships/hubot", fx.token, `{"role":"member"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("membership put: status %d, body %s", resp.StatusCode, body)
	}
	var membership struct {
		Role  string `json:"role"`
		State string `json:"state"`
		User  struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &membership); err != nil {
		t.Fatalf("decode membership: %v from %s", err, body)
	}
	if membership.Role != "member" || membership.State != "active" || membership.User.Login != "hubot" {
		t.Errorf("membership = %+v, want member/active/hubot", membership)
	}

	if resp, _ := authedGet(t, fx.srv, "/orgs/octocat/members/hubot", "token "+fx.token); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("member check after put: status %d, want 204", resp.StatusCode)
	}
	resp, body = authedGet(t, fx.srv, "/orgs/octocat/members", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("members list: status %d, body %s", resp.StatusCode, body)
	}
	var members []struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	}
	if err := json.Unmarshal(body, &members); err != nil {
		t.Fatalf("decode members: %v from %s", err, body)
	}
	logins := make([]string, 0, len(members))
	for _, m := range members {
		logins = append(logins, m.Login)
	}
	if len(members) != 2 || logins[0] != "octocat" || logins[1] != "hubot" {
		t.Errorf("members = %v, want [octocat hubot]", logins)
	}

	if resp, _ := authedSend(t, fx.srv, http.MethodDelete, "/orgs/octocat/members/hubot", fx.token, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("member delete: status %d", resp.StatusCode)
	}
	if resp, _ := authedGet(t, fx.srv, "/orgs/octocat/members/hubot", "token "+fx.token); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("member check after delete: status %d, want 404", resp.StatusCode)
	}
}

// TestOrgTeamsListAndShape checks a created team is visible in the org teams
// listing and that the team object carries the fields hub4j and Terraform
// read: a T-kind node id, members_url, repositories_url, parent, and counts.
func TestOrgTeamsListAndShape(t *testing.T) {
	fx := repoServer(t)

	resp, body := authedGet(t, fx.srv, "/orgs/octocat/teams", "token "+fx.token)
	if resp.StatusCode != http.StatusOK || string(body) != "[]" {
		t.Fatalf("empty teams list: status %d, body %s", resp.StatusCode, body)
	}

	resp, body = authedSend(t, fx.srv, http.MethodPost, "/orgs/octocat/teams", fx.token,
		`{"name":"Core Team","description":"the core"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create team: status %d, body %s", resp.StatusCode, body)
	}

	if resp, _ := authedSend(t, fx.srv, http.MethodPut,
		"/orgs/octocat/teams/core-team/memberships/octocat", fx.token, `{"role":"maintainer"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("team member add: status %d", resp.StatusCode)
	}
	if resp, _ := authedSend(t, fx.srv, http.MethodPut,
		"/orgs/octocat/teams/core-team/repos/octocat/hello", fx.token, `{"permission":"push"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("team repo add: status %d", resp.StatusCode)
	}

	resp, body = authedGet(t, fx.srv, "/orgs/octocat/teams", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("teams list: status %d, body %s", resp.StatusCode, body)
	}
	var teams []struct {
		Slug         string          `json:"slug"`
		NodeID       string          `json:"node_id"`
		MembersURL   string          `json:"members_url"`
		ReposURL     string          `json:"repositories_url"`
		Parent       json.RawMessage `json:"parent"`
		MembersCount *int            `json:"members_count"`
		ReposCount   *int            `json:"repos_count"`
		HTMLURL      string          `json:"html_url"`
	}
	if err := json.Unmarshal(body, &teams); err != nil {
		t.Fatalf("decode teams: %v from %s", err, body)
	}
	if len(teams) != 1 {
		t.Fatalf("teams = %d entries, want 1: %s", len(teams), body)
	}
	team := teams[0]
	if team.Slug != "core-team" {
		t.Errorf("slug = %q", team.Slug)
	}
	if !strings.HasPrefix(team.NodeID, "T_") {
		t.Errorf("node_id = %q, want T_ prefix", team.NodeID)
	}
	if !strings.HasSuffix(team.MembersURL, "/orgs/octocat/teams/core-team/members{/member}") {
		t.Errorf("members_url = %q", team.MembersURL)
	}
	if !strings.HasSuffix(team.ReposURL, "/orgs/octocat/teams/core-team/repos") {
		t.Errorf("repositories_url = %q", team.ReposURL)
	}
	if string(team.Parent) != "null" {
		t.Errorf("parent = %s, want null", team.Parent)
	}
	if team.MembersCount == nil || *team.MembersCount != 1 {
		t.Errorf("members_count = %v, want 1", team.MembersCount)
	}
	if team.ReposCount == nil || *team.ReposCount != 1 {
		t.Errorf("repos_count = %v, want 1", team.ReposCount)
	}

	// The single-team GET carries the same counts.
	resp, body = authedGet(t, fx.srv, "/orgs/octocat/teams/core-team", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("team get: status %d, body %s", resp.StatusCode, body)
	}
	var single struct {
		MembersCount *int `json:"members_count"`
	}
	if err := json.Unmarshal(body, &single); err != nil {
		t.Fatalf("decode team: %v from %s", err, body)
	}
	if single.MembersCount == nil || *single.MembersCount != 1 {
		t.Errorf("single team members_count = %v, want 1", single.MembersCount)
	}
}

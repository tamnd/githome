package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tamnd/githome/store"
)

// TestUserOrgsListsMemberships covers GET /user/orgs and the
// GET /user/memberships/orgs family the compat review flagged as stubbed:
// the authenticated user's org memberships are listed with the org-simple
// shape, and a single membership is fetchable by org login.
func TestUserOrgsListsMemberships(t *testing.T) {
	fx := repoServer(t)
	ctx := context.Background()

	// Seed an org and make the authenticated user (octocat) an admin member.
	org := &store.UserRow{Login: "acme", Type: "Organization"}
	if err := fx.st.InsertUser(ctx, org); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if err := fx.st.UpsertOrgMember(ctx, org.PK, fx.ownerPK, "admin"); err != nil {
		t.Fatalf("upsert member: %v", err)
	}

	// GET /user/orgs returns the org-simple shape for each membership.
	resp, body := authedGet(t, fx.srv, "/user/orgs", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("user orgs: status %d, body %s", resp.StatusCode, body)
	}
	var orgs []struct {
		Login      string          `json:"login"`
		NodeID     string          `json:"node_id"`
		ReposURL   string          `json:"repos_url"`
		MembersURL string          `json:"members_url"`
		Desc       json.RawMessage `json:"description"`
	}
	if err := json.Unmarshal(body, &orgs); err != nil {
		t.Fatalf("decode orgs: %v from %s", err, body)
	}
	if len(orgs) != 1 || orgs[0].Login != "acme" {
		t.Fatalf("orgs = %+v, want [acme]", orgs)
	}
	if !strings.HasPrefix(orgs[0].NodeID, "O_") {
		t.Errorf("node_id = %q, want O_ prefix", orgs[0].NodeID)
	}
	if !strings.HasSuffix(orgs[0].ReposURL, "/orgs/acme/repos") {
		t.Errorf("repos_url = %q", orgs[0].ReposURL)
	}
	if !strings.HasSuffix(orgs[0].MembersURL, "/orgs/acme/members{/member}") {
		t.Errorf("members_url = %q", orgs[0].MembersURL)
	}
	if len(orgs[0].Desc) == 0 {
		t.Errorf("description key missing from %s", body)
	}

	// GET /user/memberships/orgs carries state, role, organization, and user.
	resp, body = authedGet(t, fx.srv, "/user/memberships/orgs", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("memberships list: status %d, body %s", resp.StatusCode, body)
	}
	var memberships []struct {
		State        string `json:"state"`
		Role         string `json:"role"`
		Organization struct {
			Login string `json:"login"`
		} `json:"organization"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &memberships); err != nil {
		t.Fatalf("decode memberships: %v from %s", err, body)
	}
	if len(memberships) != 1 {
		t.Fatalf("memberships = %d, want 1: %s", len(memberships), body)
	}
	m := memberships[0]
	if m.State != "active" || m.Role != "admin" || m.Organization.Login != "acme" || m.User.Login != "octocat" {
		t.Errorf("membership = %+v, want active/admin/acme/octocat", m)
	}

	// GET /user/memberships/orgs/{org} returns the single membership.
	resp, body = authedGet(t, fx.srv, "/user/memberships/orgs/acme", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("single membership: status %d, body %s", resp.StatusCode, body)
	}
	if jsonString(t, body, "role") != "admin" || jsonString(t, body, "state") != "active" {
		t.Errorf("single membership shape wrong: %s", body)
	}

	// A non-member org 404s.
	resp, _ = authedGet(t, fx.srv, "/user/memberships/orgs/octocat", "token "+fx.token)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("non-member membership: status %d, want 404", resp.StatusCode)
	}
}

// TestUsersListSinceCursor covers GET /users id-cursor pagination: per_page
// caps the page, a rel="next" Link carries the last id as since, and the
// follow-up page resumes after it.
func TestUsersListSinceCursor(t *testing.T) {
	fx := repoServer(t)
	ctx := context.Background()
	for _, login := range []string{"alpha", "bravo", "charlie"} {
		if err := fx.st.InsertUser(ctx, &store.UserRow{Login: login, Type: "User"}); err != nil {
			t.Fatalf("insert %s: %v", login, err)
		}
	}

	resp, body := authedGet(t, fx.srv, "/users?per_page=2", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("users list: status %d, body %s", resp.StatusCode, body)
	}
	var page1 []struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	}
	if err := json.Unmarshal(body, &page1); err != nil {
		t.Fatalf("decode page1: %v from %s", err, body)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 = %d users, want 2: %s", len(page1), body)
	}
	link := resp.Header.Get("Link")
	if !strings.Contains(link, `rel="next"`) || !strings.Contains(link, "since=") {
		t.Fatalf("page1 Link = %q, want a since next link", link)
	}

	// Resume after the last id; the next user must not repeat page 1.
	last := page1[len(page1)-1].ID
	resp, body = authedGet(t, fx.srv, "/users?per_page=2&since="+itoa(last), "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("users page2: status %d, body %s", resp.StatusCode, body)
	}
	var page2 []struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &page2); err != nil {
		t.Fatalf("decode page2: %v from %s", err, body)
	}
	for _, u := range page2 {
		if u.ID <= last {
			t.Errorf("page2 id %d <= since %d", u.ID, last)
		}
	}
}

package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/tamnd/githome/store"
)

// addRepoRow seeds an extra repository row directly in the store, no git
// directory behind it; the list endpoints never open git.
func (fx repoFixture) addRepoRow(t *testing.T, ownerPK int64, name string, private bool, pushed time.Time) *store.RepoRow {
	t.Helper()
	r := &store.RepoRow{OwnerPK: ownerPK, Name: name, Private: private, DefaultBranch: "master", PushedAt: &pushed}
	if err := fx.st.InsertRepo(context.Background(), r); err != nil {
		t.Fatalf("insert repo %s: %v", name, err)
	}
	return r
}

func repoNames(t *testing.T, body []byte) []string {
	t.Helper()
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v: %s", err, body)
	}
	names := make([]string, 0, len(list))
	for _, r := range list {
		names = append(names, r["name"].(string))
	}
	return names
}

// TestUserReposSortAndDirection covers the sort and direction knobs on
// GET /users/{username}/repos: the default full_name ascending order, the
// explicit descending flip, and the pushed sort's default descending order.
func TestUserReposSortAndDirection(t *testing.T) {
	fx := repoServer(t)
	fx.addRepoRow(t, fx.ownerPK, "alpha", false, fixedWhen.Add(time.Hour))

	resp, body := get(t, fx.srv, "/users/octocat/repos")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if names := repoNames(t, body); len(names) != 2 || names[0] != "alpha" || names[1] != "hello" {
		t.Fatalf("default order = %v, want [alpha hello]", names)
	}

	_, body = get(t, fx.srv, "/users/octocat/repos?direction=desc")
	if names := repoNames(t, body); names[0] != "hello" {
		t.Fatalf("desc order = %v, want hello first", names)
	}

	// alpha was pushed an hour after hello, so the pushed sort (descending by
	// default) leads with it.
	_, body = get(t, fx.srv, "/users/octocat/repos?sort=pushed")
	if names := repoNames(t, body); names[0] != "alpha" {
		t.Fatalf("pushed order = %v, want alpha first", names)
	}

	resp, _ = get(t, fx.srv, "/users/octocat/repos?sort=stars")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad sort status = %d, want 422", resp.StatusCode)
	}
}

// TestUserReposTypeMember covers the type selector on the public list: the
// default owner view of a collaborator's profile is empty, type=member shows
// the granted repo, and type=all merges both sides.
func TestUserReposTypeMember(t *testing.T) {
	fx := repoServer(t)
	hubber := fx.addUser(t, "hubber")
	ctx := context.Background()
	row, err := fx.st.UserByLogin(ctx, "hubber")
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.st.UpsertCollaborator(ctx, fx.repoPK, row.PK, "push"); err != nil {
		t.Fatal(err)
	}
	fx.addRepoRow(t, row.PK, "own-thing", false, fixedWhen)

	_, body := get(t, fx.srv, "/users/hubber/repos")
	if names := repoNames(t, body); len(names) != 1 || names[0] != "own-thing" {
		t.Fatalf("owner view = %v, want [own-thing]", names)
	}

	_, body = get(t, fx.srv, "/users/hubber/repos?type=member")
	if names := repoNames(t, body); len(names) != 1 || names[0] != "hello" {
		t.Fatalf("member view = %v, want [hello]", names)
	}

	_, body = get(t, fx.srv, "/users/hubber/repos?type=all")
	if names := repoNames(t, body); len(names) != 2 {
		t.Fatalf("all view = %v, want both repos", names)
	}
	_ = hubber
}

// TestViewerReposAffiliationAndVisibility covers GET /user/repos: the default
// affiliation set includes collaborator grants (even on a private repo), the
// affiliation and visibility selectors narrow it, and combining type with
// either is GitHub's 422.
func TestViewerReposAffiliationAndVisibility(t *testing.T) {
	fx := repoServer(t)
	hubber := fx.addUser(t, "hubber")
	ctx := context.Background()
	row, err := fx.st.UserByLogin(ctx, "hubber")
	if err != nil {
		t.Fatal(err)
	}
	secret := fx.addRepoRow(t, fx.ownerPK, "secret", true, fixedWhen)
	if err := fx.st.UpsertCollaborator(ctx, secret.PK, row.PK, "push"); err != nil {
		t.Fatal(err)
	}
	fx.addRepoRow(t, row.PK, "own-thing", false, fixedWhen)

	auth := "token " + hubber
	resp, body := authedGet(t, fx.srv, "/user/repos", auth)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	// full_name ascending: hubber/own-thing sorts before octocat/secret.
	if names := repoNames(t, body); len(names) != 2 || names[0] != "own-thing" || names[1] != "secret" {
		t.Fatalf("default = %v, want [own-thing secret] (full_name asc)", names)
	}

	_, body = authedGet(t, fx.srv, "/user/repos?affiliation=owner", auth)
	if names := repoNames(t, body); len(names) != 1 || names[0] != "own-thing" {
		t.Fatalf("affiliation=owner = %v, want [own-thing]", names)
	}

	_, body = authedGet(t, fx.srv, "/user/repos?visibility=private", auth)
	if names := repoNames(t, body); len(names) != 1 || names[0] != "secret" {
		t.Fatalf("visibility=private = %v, want [secret]", names)
	}

	_, body = authedGet(t, fx.srv, "/user/repos?type=owner", auth)
	if names := repoNames(t, body); len(names) != 1 || names[0] != "own-thing" {
		t.Fatalf("type=owner = %v, want [own-thing]", names)
	}

	resp, body = authedGet(t, fx.srv, "/user/repos?type=owner&visibility=private", auth)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("type+visibility status = %d, want 422: %s", resp.StatusCode, body)
	}
	if !contains(body, "If you specify visibility or affiliation, you cannot specify type.") {
		t.Errorf("type+visibility body = %s", body)
	}
}

// TestViewerReposSinceBefore covers the updated-at window on GET /user/repos.
func TestViewerReposSinceBefore(t *testing.T) {
	fx := repoServer(t)

	// The fixture repo's updated_at is "now" (set by the insert), so a since
	// far in the future excludes it and one far in the past keeps it.
	resp, body := authedGet(t, fx.srv, "/user/repos?since=2090-01-01T00:00:00Z", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if names := repoNames(t, body); len(names) != 0 {
		t.Fatalf("future since = %v, want empty", names)
	}

	_, body = authedGet(t, fx.srv, "/user/repos?since=2000-01-01T00:00:00Z", "token "+fx.token)
	if names := repoNames(t, body); len(names) != 1 {
		t.Fatalf("past since = %v, want [hello]", names)
	}

	_, body = authedGet(t, fx.srv, "/user/repos?before=2000-01-01T00:00:00Z", "token "+fx.token)
	if names := repoNames(t, body); len(names) != 0 {
		t.Fatalf("past before = %v, want empty", names)
	}

	resp, _ = authedGet(t, fx.srv, "/user/repos?since=yesterday", "token "+fx.token)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad since status = %d, want 422", resp.StatusCode)
	}
}

// TestOrgReposTypeSelectors covers the org list's type values over fork
// status and visibility.
func TestOrgReposTypeSelectors(t *testing.T) {
	fx := repoServer(t)
	ctx := context.Background()
	org := &store.UserRow{Login: "hooli", Type: "Organization"}
	if err := fx.st.InsertUser(ctx, org); err != nil {
		t.Fatal(err)
	}
	plain := fx.addRepoRow(t, org.PK, "plain", false, fixedWhen)
	forkRow := &store.RepoRow{OwnerPK: org.PK, Name: "forky", Fork: true, DefaultBranch: "master", ForkOfPK: &plain.PK}
	if err := fx.st.InsertRepo(ctx, forkRow); err != nil {
		t.Fatal(err)
	}

	_, body := get(t, fx.srv, "/orgs/hooli/repos?type=forks")
	if names := repoNames(t, body); len(names) != 1 || names[0] != "forky" {
		t.Fatalf("forks = %v, want [forky]", names)
	}
	_, body = get(t, fx.srv, "/orgs/hooli/repos?type=sources")
	if names := repoNames(t, body); len(names) != 1 || names[0] != "plain" {
		t.Fatalf("sources = %v, want [plain]", names)
	}
	_, body = get(t, fx.srv, "/orgs/hooli/repos?type=public")
	if names := repoNames(t, body); len(names) != 2 {
		t.Fatalf("public = %v, want both", names)
	}
}

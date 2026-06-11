package rest

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tamnd/githome/store"
)

// seedReviewer inserts a plain user the tests can request reviews from; the
// pull author cannot review their own pull request.
func (fx pullFixture) seedReviewer(t *testing.T, login string) {
	t.Helper()
	u := &store.UserRow{Login: login, Type: "User"}
	if err := fx.st.InsertUser(fx.ctx, u); err != nil {
		t.Fatalf("insert reviewer: %v", err)
	}
}

// reviewerLogins projects the requested_reviewers logins out of a pull
// request body.
func reviewerLogins(t *testing.T, body []byte) []string {
	t.Helper()
	var pr struct {
		RequestedReviewers []struct {
			Login string `json:"login"`
		} `json:"requested_reviewers"`
	}
	if err := json.Unmarshal(body, &pr); err != nil {
		t.Fatalf("decode pull: %v, body %s", err, body)
	}
	out := make([]string, 0, len(pr.RequestedReviewers))
	for _, u := range pr.RequestedReviewers {
		out = append(out, u.Login)
	}
	return out
}

// TestRequestedReviewersContract walks the request lifecycle: requesting a
// reviewer returns 201 with the reviewer on the pull request, the list and
// the full pull view both carry it, and removing the request empties both.
func TestRequestedReviewersContract(t *testing.T) {
	fx := pullServer(t)
	fx.openPull(t)
	fx.seedReviewer(t, "hubber")

	resp, body := authedSend(t, fx.srv, http.MethodPost,
		"/repos/octocat/hello/pulls/1/requested_reviewers", fx.token,
		`{"reviewers":["hubber"]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("request status %d, body %s", resp.StatusCode, body)
	}
	if logins := reviewerLogins(t, body); len(logins) != 1 || logins[0] != "hubber" {
		t.Errorf("requested_reviewers after add %v, want [hubber]", logins)
	}

	resp, body = get(t, fx.srv, "/repos/octocat/hello/pulls/1/requested_reviewers")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status %d, body %s", resp.StatusCode, body)
	}
	var list struct {
		Users []struct {
			Login string `json:"login"`
		} `json:"users"`
		Teams []any `json:"teams"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v, body %s", err, body)
	}
	if len(list.Users) != 1 || list.Users[0].Login != "hubber" {
		t.Errorf("listed users %v, want hubber", list.Users)
	}
	if list.Teams == nil || len(list.Teams) != 0 {
		t.Errorf("listed teams %v, want empty array", list.Teams)
	}

	_, body = get(t, fx.srv, "/repos/octocat/hello/pulls/1")
	if logins := reviewerLogins(t, body); len(logins) != 1 || logins[0] != "hubber" {
		t.Errorf("full view requested_reviewers %v, want [hubber]", logins)
	}

	// Re-requesting the same reviewer is a no-op, not a duplicate.
	resp, body = authedSend(t, fx.srv, http.MethodPost,
		"/repos/octocat/hello/pulls/1/requested_reviewers", fx.token,
		`{"reviewers":["hubber"]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("re-request status %d, body %s", resp.StatusCode, body)
	}
	if logins := reviewerLogins(t, body); len(logins) != 1 {
		t.Errorf("requested_reviewers after re-add %v, want one entry", logins)
	}

	resp, body = authedSend(t, fx.srv, http.MethodDelete,
		"/repos/octocat/hello/pulls/1/requested_reviewers", fx.token,
		`{"reviewers":["hubber"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove status %d, body %s", resp.StatusCode, body)
	}
	if logins := reviewerLogins(t, body); len(logins) != 0 {
		t.Errorf("requested_reviewers after remove %v, want none", logins)
	}

	_, body = get(t, fx.srv, "/repos/octocat/hello/pulls/1/requested_reviewers")
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list after remove: %v, body %s", err, body)
	}
	if len(list.Users) != 0 {
		t.Errorf("listed users after remove %v, want none", list.Users)
	}
}

// TestRequestedReviewersErrors covers the refusals: the pull author, an
// unknown login, and an anonymous caller.
func TestRequestedReviewersErrors(t *testing.T) {
	fx := pullServer(t)
	fx.openPull(t)

	resp, body := authedSend(t, fx.srv, http.MethodPost,
		"/repos/octocat/hello/pulls/1/requested_reviewers", fx.token,
		`{"reviewers":["octocat"]}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("author request status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "pull request author") {
		t.Errorf("author request body %s, want the author refusal", body)
	}

	resp, body = authedSend(t, fx.srv, http.MethodPost,
		"/repos/octocat/hello/pulls/1/requested_reviewers", fx.token,
		`{"reviewers":["nobody-here"]}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unknown request status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "collaborators") {
		t.Errorf("unknown request body %s, want the collaborator refusal", body)
	}

	req, err := http.NewRequest(http.MethodPost,
		fx.srv.URL+"/repos/octocat/hello/pulls/1/requested_reviewers",
		strings.NewReader(`{"reviewers":["hubber"]}`))
	if err != nil {
		t.Fatal(err)
	}
	anon, err := fx.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = anon.Body.Close() }()
	if anon.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous request status %d, want 401", anon.StatusCode)
	}
}

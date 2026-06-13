package rest

import (
	"net/http"
	"strings"
	"testing"
)

// TestForbiddenCanonicalMessage checks the admin-gated repository operations
// answer an unauthorized caller with GitHub's canonical 403 string, the one
// clients and SDK error matchers key on.
func TestForbiddenCanonicalMessage(t *testing.T) {
	fx := repoServer(t)
	hubber := fx.addUser(t, "hubber")

	cases := []struct {
		method, path, body string
	}{
		// DELETE /repos is gated by the delete_repo scope before the
		// permission check, so it cannot reach the 403 with a repo token.
		{http.MethodPatch, "/repos/octocat/hello", `{"description":"nope"}`},
		{http.MethodPost, "/repos/octocat/hello/keys", `{"key":"ssh-ed25519 AAAA test"}`},
		{http.MethodPut, "/repos/octocat/hello/topics", `{"names":["x"]}`},
		{http.MethodPut, "/repos/octocat/hello/collaborators/hubber", ""},
	}
	for _, tc := range cases {
		resp, body := authedSend(t, fx.srv, tc.method, tc.path, hubber, tc.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s: status %d, want 403, body %s", tc.method, tc.path, resp.StatusCode, body)
			continue
		}
		if !contains(body, "Must have admin rights to Repository.") {
			t.Errorf("%s %s: body %s, want canonical admin-rights message", tc.method, tc.path, body)
		}
	}
}

// TestAcceptVariantsIgnored checks the issue body media types Githome does not
// render variants for (html+json, full+json, text+json) are accepted and
// answered with the normal JSON representation, never rejected. GitHub clients
// send these routinely; the body simply lacks body_html until that render
// exists. The text-match variant on search IS honored now (see
// TestSearchTextMatch); here we only confirm the envelope still carries
// total_count when it is requested.
func TestAcceptVariantsIgnored(t *testing.T) {
	fx := issueServer(t)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"media issue","body":"hello"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issue: status %d, body %s", resp.StatusCode, body)
	}

	for _, accept := range []string{
		"application/vnd.github.html+json",
		"application/vnd.github.full+json",
		"application/vnd.github.text+json",
	} {
		resp, body := getWith(t, fx.srv, "/repos/octocat/hello/issues/1", map[string]string{"Accept": accept})
		if resp.StatusCode != http.StatusOK {
			t.Errorf("issue GET with Accept %s: status %d, body %s", accept, resp.StatusCode, body)
			continue
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("issue GET with Accept %s: content type %q, want JSON", accept, ct)
		}
		if !contains(body, `"media issue"`) {
			t.Errorf("issue GET with Accept %s: body %s, want the normal representation", accept, body)
		}
	}

	sfx := searchServer(t)
	resp, body := getWith(t, sfx.srv, "/search/repositories?q=hello",
		map[string]string{"Accept": "application/vnd.github.text-match+json"})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("search with text-match accept: status %d, body %s", resp.StatusCode, body)
	}
	if !contains(body, `"total_count"`) {
		t.Errorf("search with text-match accept: body %s, want the normal result envelope", body)
	}
}

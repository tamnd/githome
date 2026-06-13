package rest

import (
	"net/http"
	"strings"
	"testing"
)

// searchUsersEnv is the GET /search/users envelope reduced to the fields the
// user search test asserts.
type searchUsersEnv struct {
	TotalCount        int  `json:"total_count"`
	IncompleteResults bool `json:"incomplete_results"`
	Items             []struct {
		Login string  `json:"login"`
		Type  string  `json:"type"`
		ID    int64   `json:"id"`
		Score float64 `json:"score"`
	} `json:"items"`
}

// TestSearchUsersContract drives the real account search the compat review
// asked for. The search fixture holds octocat and hubot; a term that matches
// one returns just that account, and an unscoped term returns both.
func TestSearchUsersContract(t *testing.T) {
	fx := searchServer(t)

	resp, body := get(t, fx.srv, "/search/users?q=hubot")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	var env searchUsersEnv
	decodeBody(t, body, &env)
	if env.IncompleteResults {
		t.Errorf("incomplete_results = true, want false")
	}
	if env.TotalCount != 1 || len(env.Items) != 1 {
		t.Fatalf("hubot search returned %d items (total %d), want 1: %s", len(env.Items), env.TotalCount, body)
	}
	if env.Items[0].Login != "hubot" {
		t.Errorf("matched %q, want hubot", env.Items[0].Login)
	}
	if env.Items[0].ID == 0 {
		t.Errorf("user hit missing id: %s", body)
	}

	// A term shared by both accounts (the common substring "o") returns both.
	resp, body = get(t, fx.srv, "/search/users?q=o")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("broad status %d, body %s", resp.StatusCode, body)
	}
	decodeBody(t, body, &env)
	if env.TotalCount != 2 || len(env.Items) != 2 {
		t.Fatalf("broad search returned %d items (total %d), want 2: %s", len(env.Items), env.TotalCount, body)
	}
}

// TestSearchUsersTypeQualifier checks the type: qualifier narrows the search to
// the account kind, so a user-typed query never returns the org account.
func TestSearchUsersTypeQualifier(t *testing.T) {
	fx := searchServer(t)

	// Seed an org sharing the "o" substring with the two seeded users.
	if resp, body := authedSend(t, fx.srv, http.MethodGet, "/orgs/octocat", fx.token, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("org get: status %d, body %s", resp.StatusCode, body)
	}

	resp, body := get(t, fx.srv, "/search/users?q=o+type:user")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	var env searchUsersEnv
	decodeBody(t, body, &env)
	for _, it := range env.Items {
		if it.Type != "User" {
			t.Errorf("type:user search returned a %s account (%s)", it.Type, it.Login)
		}
	}

	// A missing q is the required-field 422, like every other search.
	resp, body = get(t, fx.srv, "/search/users")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("missing q: status %d, want 422, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"field":"q"`) {
		t.Errorf("422 body missing q field error: %s", body)
	}
}

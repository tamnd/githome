package rest

import (
	"net/http"
	"strings"
	"testing"
)

// TestIssueLockContract covers PUT and DELETE /issues/{number}/lock: locking
// with a reason renders on the issue, unlocking clears both fields, and both
// writes answer with a bare 204.
func TestIssueLockContract(t *testing.T) {
	fx := issueServer(t)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"Heated"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issue status %d, body %s", resp.StatusCode, body)
	}

	resp, body := authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/issues/1/lock", fx.token,
		`{"lock_reason":"too heated"}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("lock status %d, want 204, body %s", resp.StatusCode, body)
	}
	if len(body) != 0 {
		t.Errorf("lock body not empty: %s", body)
	}

	resp, body = get(t, fx.srv, "/repos/octocat/hello/issues/1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"locked":true`) {
		t.Errorf("issue not locked: %s", body)
	}
	if !strings.Contains(string(body), `"active_lock_reason":"too heated"`) {
		t.Errorf("lock reason missing: %s", body)
	}

	resp, body = authedSend(t, fx.srv, http.MethodDelete, "/repos/octocat/hello/issues/1/lock", fx.token, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unlock status %d, want 204, body %s", resp.StatusCode, body)
	}

	resp, body = get(t, fx.srv, "/repos/octocat/hello/issues/1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"locked":false`) {
		t.Errorf("issue still locked: %s", body)
	}
	if !strings.Contains(string(body), `"active_lock_reason":null`) {
		t.Errorf("lock reason not cleared: %s", body)
	}
}

// TestIssueLockNoBody covers locking without a request body, which GitHub
// allows; the lock takes with no reason recorded.
func TestIssueLockNoBody(t *testing.T) {
	fx := issueServer(t)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"Quiet"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issue status %d, body %s", resp.StatusCode, body)
	}
	resp, body := authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/issues/1/lock", fx.token, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("lock status %d, want 204, body %s", resp.StatusCode, body)
	}
	_, body = get(t, fx.srv, "/repos/octocat/hello/issues/1")
	if !strings.Contains(string(body), `"locked":true`) {
		t.Errorf("issue not locked: %s", body)
	}
	if !strings.Contains(string(body), `"active_lock_reason":null`) {
		t.Errorf("reasonless lock should keep null reason: %s", body)
	}
}

// TestIssueLockErrors covers the invalid reason (422), the missing issue
// (404), and the unauthenticated write (401).
func TestIssueLockErrors(t *testing.T) {
	fx := issueServer(t)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"Edge"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issue status %d, body %s", resp.StatusCode, body)
	}

	resp, body := authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/issues/1/lock", fx.token,
		`{"lock_reason":"because"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad reason status %d, want 422, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"field":"lock_reason"`) {
		t.Errorf("422 missing lock_reason field error: %s", body)
	}

	resp, body = authedSend(t, fx.srv, http.MethodPut, "/repos/octocat/hello/issues/99/lock", fx.token, `{}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing issue status %d, want 404, body %s", resp.StatusCode, body)
	}

	req, err := http.NewRequest(http.MethodPut, fx.srv.URL+"/repos/octocat/hello/issues/1/lock", nil)
	if err != nil {
		t.Fatal(err)
	}
	anon, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = anon.Body.Close() }()
	if anon.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous lock status %d, want 401", anon.StatusCode)
	}
}

package rest

import (
	"fmt"
	"net/http"
	"testing"
)

// TestWebhookTestEndpoint covers the tests endpoint the compat review flagged as
// missing (R01-55): POST /repos/{owner}/{repo}/hooks/{hook_id}/tests fires the
// hook against the repository's latest push and answers 204. A hook subscribed
// to push, a hook not subscribed, and an empty repository all return 204; an
// unknown hook is a 404.
func TestWebhookTestEndpoint(t *testing.T) {
	fx := hookServer(t)
	id := fx.seedHook(t)

	// The push-subscribed hook accepts the test against an empty repository:
	// there is no head to push, so no delivery is fired, but the call still
	// answers 204 the way GitHub does.
	path := fmt.Sprintf("/repos/octocat/hello/hooks/%d/tests", id)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, path, fx.token, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("test push hook: status %d, body %s", resp.StatusCode, body)
	}

	// A hook subscribed only to issues is not a push subscriber; the endpoint
	// authorizes and returns 204 without a delivery, matching GitHub.
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/hooks", fx.token,
		`{"events":["issues"],"config":{"url":"https://example.test/issues-hook"}}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issues hook: status %d, body %s", resp.StatusCode, body)
	}
	issuesID := jsonInt(t, body, "id")
	issuesPath := fmt.Sprintf("/repos/octocat/hello/hooks/%d/tests", issuesID)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, issuesPath, fx.token, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("test non-push hook: status %d, body %s", resp.StatusCode, body)
	}

	// An unknown hook id is a 404, like every other hook subresource.
	missing := "/repos/octocat/hello/hooks/999999/tests"
	if resp, _ := authedSend(t, fx.srv, http.MethodPost, missing, fx.token, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("test unknown hook: status %d, want 404", resp.StatusCode)
	}

	// An unrelated user cannot test a hook they do not administer.
	if resp, _ := authedSend(t, fx.srv, http.MethodPost, path, fx.intrud, ""); resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusForbidden {
		t.Errorf("test as intruder: status %d, want 404 or 403", resp.StatusCode)
	}
}

// TestHookLastResponsePresent confirms the hook representation carries the
// last_response object the review flagged ({code, status, message}): before any
// delivery the status is "unused" with a null code and message.
func TestHookLastResponsePresent(t *testing.T) {
	fx := hookServer(t)
	id := fx.seedHook(t)

	resp, body := authedSend(t, fx.srv, http.MethodGet, fmt.Sprintf("/repos/octocat/hello/hooks/%d", id), fx.token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get hook: status %d, body %s", resp.StatusCode, body)
	}
	var got struct {
		LastResponse *struct {
			Code    *int    `json:"code"`
			Status  string  `json:"status"`
			Message *string `json:"message"`
		} `json:"last_response"`
	}
	decodeBody(t, body, &got)
	if got.LastResponse == nil {
		t.Fatalf("hook missing last_response object: %s", body)
	}
	if got.LastResponse.Status != "unused" {
		t.Errorf("last_response.status = %q, want unused", got.LastResponse.Status)
	}
	if got.LastResponse.Code != nil {
		t.Errorf("last_response.code = %v, want null before any delivery", *got.LastResponse.Code)
	}
	if got.LastResponse.Message != nil {
		t.Errorf("last_response.message = %v, want null before any delivery", *got.LastResponse.Message)
	}
}

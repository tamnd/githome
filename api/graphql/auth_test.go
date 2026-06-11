package graphql_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestAnonymousRequestIs401 confirms an unauthenticated GraphQL request is
// rejected at the transport with GitHub's 401 body, before any execution.
func TestAnonymousRequestIs401(t *testing.T) {
	srv, _ := graphqlServer(t)
	body := strings.NewReader(`{"query":"{ viewer { login } }"}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/graphql", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	got := string(buf[:n])
	if !strings.Contains(got, "This endpoint requires you to be authenticated.") {
		t.Fatalf("body = %s, want the authentication-required message", got)
	}
}

// TestBadCredentialIs401 confirms a present but invalid credential gets the
// bad-credentials 401, distinct from the missing-credential message.
func TestBadCredentialIs401(t *testing.T) {
	srv, _ := graphqlServer(t)
	body := strings.NewReader(`{"query":"{ viewer { login } }"}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/graphql", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token ghp_definitelynotvalid")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	got := string(buf[:n])
	if !strings.Contains(got, "Bad credentials") {
		t.Fatalf("body = %s, want Bad credentials", got)
	}
}

// TestNodeUnresolvableIsNotFound confirms node() answers a malformed global id
// with null plus GitHub's NOT_FOUND error, not a validation error.
func TestNodeUnresolvableIsNotFound(t *testing.T) {
	srv, token := graphqlServer(t)
	got := post(t, srv, token, `query($id: ID!) { node(id: $id) { id } }`, map[string]any{"id": "garbage"})
	var env struct {
		Data struct {
			Node *json.RawMessage `json:"node"`
		} `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal: %v, body %s", err, got)
	}
	if env.Data.Node != nil {
		t.Fatalf("node = %s, want null", *env.Data.Node)
	}
	if len(env.Errors) != 1 {
		t.Fatalf("errors = %v, want one", env.Errors)
	}
	if env.Errors[0].Type != "NOT_FOUND" {
		t.Errorf("type = %q, want NOT_FOUND", env.Errors[0].Type)
	}
	if want := "Could not resolve to a node with the global id of 'garbage'."; env.Errors[0].Message != want {
		t.Errorf("message = %q, want %q", env.Errors[0].Message, want)
	}
}

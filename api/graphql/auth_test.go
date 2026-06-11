package graphql_test

import (
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

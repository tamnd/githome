package graphql_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// TestRateLimitHeaders covers R02-23: a GraphQL POST carries GitHub's
// X-RateLimit-* family, fed from the same cost walk the rateLimit query reports.
func TestRateLimitHeaders(t *testing.T) {
	srv, token := graphqlServer(t)

	query := `query($owner:String!,$name:String!){ repository(owner:$owner,name:$name){ name } }`
	body, _ := json.Marshal(map[string]any{
		"query":     query,
		"variables": map[string]any{"owner": "octocat", "name": "hello"},
	})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}

	if got := resp.Header.Get("X-RateLimit-Limit"); got != "5000" {
		t.Errorf("X-RateLimit-Limit = %q, want 5000", got)
	}
	if got := resp.Header.Get("X-RateLimit-Resource"); got != "graphql" {
		t.Errorf("X-RateLimit-Resource = %q, want graphql", got)
	}
	used, err := strconv.Atoi(resp.Header.Get("X-RateLimit-Used"))
	if err != nil || used < 1 {
		t.Fatalf("X-RateLimit-Used = %q, want >= 1", resp.Header.Get("X-RateLimit-Used"))
	}
	remaining, err := strconv.Atoi(resp.Header.Get("X-RateLimit-Remaining"))
	if err != nil {
		t.Fatalf("X-RateLimit-Remaining = %q, not an int", resp.Header.Get("X-RateLimit-Remaining"))
	}
	if remaining != 5000-used {
		t.Errorf("X-RateLimit-Remaining = %d, want %d (limit - used)", remaining, 5000-used)
	}
	reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64)
	if err != nil {
		t.Fatalf("X-RateLimit-Reset = %q, not an int", resp.Header.Get("X-RateLimit-Reset"))
	}
	if reset <= time.Now().Unix() {
		t.Errorf("X-RateLimit-Reset = %d, want a future epoch second", reset)
	}
}

// TestGraphQLPostOnly covers R02-23: the GraphQL endpoint refuses GET with 405
// and an Allow: POST header, the way GitHub's endpoint does. The GET route stays
// registered so the refusal is the handler's, not a routing miss.
func TestGraphQLPostOnly(t *testing.T) {
	srv, token := graphqlServer(t)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/graphql?query=%7Bviewer%7Blogin%7D%7D", nil)
	req.Header.Set("Authorization", "token "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET status %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); allow != http.MethodPost {
		t.Errorf("Allow = %q, want POST", allow)
	}
}

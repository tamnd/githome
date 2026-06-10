package rest

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/config"
	"github.com/tamnd/githome/jsondiff"
)

func contains(b []byte, sub string) bool { return bytes.Contains(b, []byte(sub)) }

func testConfig() config.Config {
	return config.Config{
		RateLimit: config.RateLimit{
			AuthedPerHour: 5000,
			AnonPerHour:   60,
			GraphQLPoints: 5000,
			SearchPerMin:  30,
			Window:        time.Hour,
		},
	}
}

func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	root := mizu.NewRouter()
	Mount(root, Deps{Config: testConfig()})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)
	return srv
}

func golden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return b
}

func get(t testing.TB, srv *httptest.Server, path string) (*http.Response, []byte) {
	t.Helper()
	return getWith(t, srv, path, nil)
}

// getWith is get with caller-supplied request headers, for conditional-request
// tests that send If-None-Match.
func getWith(t testing.TB, srv *httptest.Server, path string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("new request %s: %v", path, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		body = append(body, buf[:n]...)
		if err != nil {
			break
		}
	}
	return resp, body
}

func TestMetaContract(t *testing.T) {
	srv := testServer(t)
	// Both the GHES-style prefix and the bare root must serve the same body.
	for _, path := range []string{"/api/v3/meta", "/meta"} {
		resp, body := get(t, srv, path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("%s: content-type %q", path, ct)
		}
		jsondiff.AssertCompatible(t, golden(t, "meta.golden.json"), body, jsondiff.Default("git.test.internal"))
	}
}

func TestRateLimitContract(t *testing.T) {
	srv := testServer(t)
	resp, body := get(t, srv, "/api/v3/rate_limit")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	jsondiff.AssertCompatible(t, golden(t, "rate_limit.golden.json"), body, jsondiff.Default("git.test.internal"))
}

func TestNotFoundContract(t *testing.T) {
	srv := testServer(t)
	resp, body := get(t, srv, "/api/v3/does-not-exist")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", resp.StatusCode)
	}
	jsondiff.AssertCompatible(t, golden(t, "not_found.golden.json"), body, jsondiff.Default("git.test.internal"))
}

func TestValidationFailedContract(t *testing.T) {
	// No M0 endpoint emits 422 yet, so exercise the envelope directly: it is the
	// shape every later mutating endpoint will reuse.
	rec := httptest.NewRecorder()
	writeError(rec, errValidation(FieldError{Resource: "Repository", Field: "name", Code: "missing_field"}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", rec.Code)
	}
	jsondiff.AssertCompatible(t, golden(t, "validation_failed.golden.json"), rec.Body.Bytes(), jsondiff.Default("git.test.internal"))
}

func TestVersionsEndpoint(t *testing.T) {
	srv := testServer(t)
	for _, path := range []string{"/api/v3/versions", "/versions"} {
		resp, body := get(t, srv, path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
		// Must return an empty JSON array so gh detects a GHES-compatible host.
		if s := string(body); s != "[]" && s != "[]\n" {
			t.Errorf("%s: body = %q, want []", path, body)
		}
	}
}

func TestOAuthDiscovery(t *testing.T) {
	srv := testServer(t)
	resp, body := get(t, srv, "/.well-known/oauth-authorization-server")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	// Must contain the device authorization endpoint so git-credential-oauth
	// and GCM can discover the device flow without hardcoded paths.
	if !contains(body, "device_authorization_endpoint") {
		t.Errorf("missing device_authorization_endpoint in: %s", body)
	}
	if !contains(body, "token_endpoint") {
		t.Errorf("missing token_endpoint in: %s", body)
	}
}

func TestOAuthAuthCodeGrantAccepted(t *testing.T) {
	// Verify that POST /login/oauth/access_token with grant_type=authorization_code
	// returns a well-formed OAuth JSON response, not "unsupported_grant_type".
	// The code is bogus so we get "bad_verification_code" — but that proves the
	// endpoint understood the authorization_code grant type.
	srv, _ := authServer(t)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/login/oauth/access_token", bytes.NewBufferString(
		"grant_type=authorization_code&client_id=testclient&code=bogus&redirect_uri=http%3A%2F%2Flocalhost%2Fcb",
	))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST access_token: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	body := buf[:n]
	// Must not be "unsupported_grant_type" — the route understood authorization_code.
	if contains(body, "unsupported_grant_type") {
		t.Errorf("unexpected unsupported_grant_type; authorization_code grant must be accepted: %s", body)
	}
}

func TestEnterpriseVersionHeader(t *testing.T) {
	srv := testServer(t)
	resp, _ := get(t, srv, "/api/v3/meta")
	if got := resp.Header.Get("x-github-enterprise-version"); got == "" {
		t.Error("missing x-github-enterprise-version header on /api/v3/ response")
	}
}

func TestStandardHeaders(t *testing.T) {
	srv := testServer(t)
	resp, _ := get(t, srv, "/api/v3/meta")
	if resp.Header.Get("X-GitHub-Request-Id") == "" {
		t.Error("missing X-GitHub-Request-Id")
	}
	if got := resp.Header.Get("X-GitHub-Api-Version"); got != "2022-11-28" {
		t.Errorf("X-GitHub-Api-Version = %q, want default", got)
	}
	if got := resp.Header.Get("X-GitHub-Media-Type"); got != "github.v3; format=json" {
		t.Errorf("X-GitHub-Media-Type = %q", got)
	}
}

func TestUnknownAPIVersionRejected(t *testing.T) {
	srv := testServer(t)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v3/meta", nil)
	req.Header.Set("X-GitHub-Api-Version", "1999-01-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
}

func TestSupportedAPIVersionEchoed(t *testing.T) {
	srv := testServer(t)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v3/meta", nil)
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-GitHub-Api-Version"); got != "2026-03-10" {
		t.Errorf("echoed version = %q", got)
	}
}

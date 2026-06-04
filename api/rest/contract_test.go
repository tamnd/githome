package rest

import (
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

func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
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

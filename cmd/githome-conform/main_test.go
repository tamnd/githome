package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSplitNWO(t *testing.T) {
	cases := []struct {
		in          string
		owner, repo string
		ok          bool
	}{
		{"octocat/hello", "octocat", "hello", true},
		{"octocat", "", "", false},
		{"/hello", "", "", false},
		{"octocat/", "", "", false},
		{"a/b/c", "", "", false},
	}
	for _, tc := range cases {
		owner, repo, ok := splitNWO(tc.in)
		if owner != tc.owner || repo != tc.repo || ok != tc.ok {
			t.Errorf("splitNWO(%q) = (%q,%q,%v), want (%q,%q,%v)",
				tc.in, owner, repo, ok, tc.owner, tc.repo, tc.ok)
		}
	}
}

// stubInstance is an httptest server that answers the conformance matrix with
// representative GitHub-compatible payloads, including ETag/304 handling, a Link
// header on a paginated issue list, the search envelope, and the two GraphQL
// documents the binary sends. It lets the matrix runner be tested without
// booting the whole server.
func stubInstance(t *testing.T) *httptest.Server {
	t.Helper()
	const etag = `W/"abc123"`
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v3/repos/octocat/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", etag)
		w.Header().Set("X-RateLimit-Remaining", "60")
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		writeJSON(w, map[string]any{
			"full_name": "octocat/hello",
			"url":       "https://git.example.com/api/v3/repos/octocat/hello",
		})
	})

	mux.HandleFunc("/api/v3/repos/octocat/hello/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<`+r.Host+`/issues?page=2>; rel="next", <`+r.Host+`/issues?page=3>; rel="last"`)
		writeJSON(w, []map[string]any{{"number": 1, "title": "first"}})
	})

	mux.HandleFunc("/api/v3/search/repositories", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"total_count": 1, "incomplete_results": false,
			"items": []map[string]any{{"full_name": "octocat/hello", "score": 1.0}},
		})
	})
	mux.HandleFunc("/api/v3/search/issues", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"total_count": 1, "incomplete_results": false,
			"items": []map[string]any{{"number": 1, "score": 1.0}},
		})
	})

	mux.HandleFunc("/api/graphql", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		if strings.Contains(req.Query, "__schema") {
			writeJSON(w, map[string]any{"data": map[string]any{
				"__schema": map[string]any{"queryType": map[string]any{"name": "Query"}},
			}})
			return
		}
		writeJSON(w, map[string]any{"data": map[string]any{
			"repository": map[string]any{"issues": map[string]any{
				"pageInfo": map[string]any{"hasNextPage": false}, "totalCount": 1,
			}},
		}})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestConformAllPass(t *testing.T) {
	srv := stubInstance(t)
	var buf bytes.Buffer
	err := run([]string{"-url", srv.URL, "-token", "t", "octocat/hello"}, &buf)
	if err != nil {
		t.Fatalf("run reported failures:\n%s\nerr: %v", buf.String(), err)
	}
	out := buf.String()
	if strings.Contains(out, "FAIL") {
		t.Errorf("report contains a FAIL:\n%s", out)
	}
	for _, want := range []string{"full_name", "304 on match", "no rate-limit spend", "Link rels", "introspection", "issues connection"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing the %q check:\n%s", want, out)
		}
	}
}

func TestConformDetectsUpstreamLeak(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/octocat/hello", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"full_name": "octocat/hello",
			"url":       "https://api.github.com/repos/octocat/hello",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	err := run([]string{"-url", srv.URL, "octocat/hello"}, &buf)
	if err == nil {
		t.Fatalf("expected a non-nil error for the leaking instance, report:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "found api.github.com") {
		t.Errorf("leak not reported:\n%s", buf.String())
	}
}

func TestConformRequiresURL(t *testing.T) {
	var buf bytes.Buffer
	if err := run([]string{"octocat/hello"}, &buf); err == nil {
		t.Error("expected an error when no URL is given")
	}
}

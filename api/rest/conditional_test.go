package rest

import (
	"net/http"
	"testing"
)

// TestConditionalGETReturns304 confirms a GET emits a weak ETag and that
// replaying it under If-None-Match returns 304 with no body, the way a
// conditional GitHub GET answers an unchanged resource.
func TestConditionalGETReturns304(t *testing.T) {
	fx := issueServer(t)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"cacheable"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issue: status %d, body %s", resp.StatusCode, body)
	}

	resp, body := get(t, fx.srv, "/repos/octocat/hello/issues/1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first GET status %d", resp.StatusCode)
	}
	tag := resp.Header.Get("ETag")
	if tag == "" {
		t.Fatal("first GET did not set an ETag")
	}
	if len(body) == 0 {
		t.Fatal("first GET returned an empty body")
	}

	resp2, body2 := getWith(t, fx.srv, "/repos/octocat/hello/issues/1", map[string]string{"If-None-Match": tag})
	if resp2.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional GET status %d, want 304", resp2.StatusCode)
	}
	if len(body2) != 0 {
		t.Errorf("304 carried a body of %d bytes, want none", len(body2))
	}
	if resp2.Header.Get("ETag") != tag {
		t.Errorf("304 ETag = %q, want %q", resp2.Header.Get("ETag"), tag)
	}
}

// TestConditionalETagChangesOnEdit confirms the validator tracks the resource:
// editing the issue changes the body, so the stale tag no longer matches and the
// server serves a fresh 200 rather than a 304.
func TestConditionalETagChangesOnEdit(t *testing.T) {
	fx := issueServer(t)
	if resp, _ := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"before"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issue status %d", resp.StatusCode)
	}
	resp, _ := get(t, fx.srv, "/repos/octocat/hello/issues/1")
	tag := resp.Header.Get("ETag")

	if r, _ := authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello/issues/1", fx.token,
		`{"title":"after"}`); r.StatusCode != http.StatusOK {
		t.Fatalf("edit status %d", r.StatusCode)
	}

	resp2, _ := getWith(t, fx.srv, "/repos/octocat/hello/issues/1", map[string]string{"If-None-Match": tag})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("GET after edit with stale tag status %d, want 200", resp2.StatusCode)
	}
	if resp2.Header.Get("ETag") == tag {
		t.Error("ETag did not change after the issue was edited")
	}
}

// TestVersionedRepoGET304 confirms GET /repos/{owner}/{repo} carries a version
// ETag and returns 304 on a repeated conditional GET.
func TestVersionedRepoGET304(t *testing.T) {
	fx := issueServer(t)

	resp, body := get(t, fx.srv, "/repos/octocat/hello")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first GET status %d, body %s", resp.StatusCode, body)
	}
	tag := resp.Header.Get("ETag")
	if tag == "" {
		t.Fatal("first GET did not set an ETag")
	}

	resp2, body2 := getWith(t, fx.srv, "/repos/octocat/hello", map[string]string{"If-None-Match": tag})
	if resp2.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional GET status %d, want 304", resp2.StatusCode)
	}
	if len(body2) != 0 {
		t.Errorf("304 carried a body of %d bytes, want none", len(body2))
	}
	if resp2.Header.Get("ETag") != tag {
		t.Errorf("304 ETag = %q, want %q", resp2.Header.Get("ETag"), tag)
	}
}

// BenchmarkConditional_304path measures the cost of a 304 response on a
// version-ETag endpoint. The 304 path skips JSON marshaling; this benchmark
// confirms the savings: throughput should be bounded only by version-key
// derivation and HTTP overhead, not marshal cost.
func BenchmarkConditional_304path(b *testing.B) {
	fx := issueServer(b)
	if resp, body := authedSend(b, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"bench issue"}`); resp.StatusCode != http.StatusCreated {
		b.Fatalf("seed issue: status %d, body %s", resp.StatusCode, body)
	}

	resp, _ := get(b, fx.srv, "/repos/octocat/hello/issues/1")
	tag := resp.Header.Get("ETag")
	if tag == "" {
		b.Fatal("no ETag on first GET")
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		client := fx.srv.Client()
		for pb.Next() {
			req, _ := http.NewRequest(http.MethodGet, fx.srv.URL+"/repos/octocat/hello/issues/1", nil)
			req.Header.Set("If-None-Match", tag)
			resp, err := client.Do(req)
			if err != nil {
				b.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusNotModified {
				b.Fatalf("want 304, got %d", resp.StatusCode)
			}
		}
	})
}


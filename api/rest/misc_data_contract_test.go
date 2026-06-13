package rest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/markup"
)

// markupServer builds a server with the shared markup renderer wired, so the
// POST /markdown endpoints mount.
func markupServer(t *testing.T) *httptest.Server {
	t.Helper()
	root := mizu.NewRouter()
	Mount(root, Deps{Config: testConfig(), Markup: markup.New(markup.Config{})})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)
	return srv
}

// postBody POSTs body to path with the given content type and returns the
// response and its body, following no redirects.
func postBody(t *testing.T, srv *httptest.Server, path, contentType, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request %s: %v", path, err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(buf)
		out = append(out, buf[:n]...)
		if rerr != nil {
			break
		}
	}
	return resp, out
}

// TestZenContract checks GET /zen is a single text/plain line.
func TestZenContract(t *testing.T) {
	srv := testServer(t)
	resp, body := get(t, srv, "/zen")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content type %q, want text/plain", ct)
	}
	if strings.TrimSpace(string(body)) == "" {
		t.Errorf("zen body empty")
	}
}

// TestOctocatContract checks the speech bubble carries the s parameter.
func TestOctocatContract(t *testing.T) {
	srv := testServer(t)
	resp, body := get(t, srv, "/octocat?s=hi+there")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "hi there") {
		t.Errorf("octocat did not echo the s parameter:\n%s", body)
	}
}

// TestLicensesContract checks the index and the single-license lookup, and that
// an unknown key is a 404.
func TestLicensesContract(t *testing.T) {
	srv := testServer(t)

	resp, body := get(t, srv, "/licenses")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status %d", resp.StatusCode)
	}
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("license list empty")
	}
	found := false
	for _, l := range list {
		if l["key"] == "mit" {
			found = true
			if l["spdx_id"] != "MIT" {
				t.Errorf("mit spdx_id = %v, want MIT", l["spdx_id"])
			}
		}
	}
	if !found {
		t.Error("mit missing from license list")
	}

	resp, body = get(t, srv, "/licenses/mit")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status %d", resp.StatusCode)
	}
	var one map[string]any
	if err := json.Unmarshal(body, &one); err != nil {
		t.Fatalf("decode one: %v", err)
	}
	if one["key"] != "mit" {
		t.Errorf("key = %v, want mit", one["key"])
	}

	resp, _ = get(t, srv, "/licenses/does-not-exist")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown license status %d, want 404", resp.StatusCode)
	}

	resp, body = get(t, srv, "/licenses?featured=true")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("featured status %d", resp.StatusCode)
	}
	var featured []map[string]any
	if err := json.Unmarshal(body, &featured); err != nil {
		t.Fatalf("decode featured: %v", err)
	}
	if len(featured) == 0 || len(featured) >= len(list) {
		t.Errorf("featured=%d list=%d, expected a non-empty narrower set", len(featured), len(list))
	}
}

// TestEmojisContract checks GET /emojis is a non-empty name to URL map.
func TestEmojisContract(t *testing.T) {
	srv := testServer(t)
	resp, body := get(t, srv, "/emojis")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var m map[string]string
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m["+1"] == "" {
		t.Errorf("emoji map missing +1:\n%s", body)
	}
}

// TestGitignoreTemplatesContract checks the index and a single template, with a
// 404 for an unknown name.
func TestGitignoreTemplatesContract(t *testing.T) {
	srv := testServer(t)

	resp, body := get(t, srv, "/gitignore/templates")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status %d", resp.StatusCode)
	}
	var names []string
	if err := json.Unmarshal(body, &names); err != nil {
		t.Fatalf("decode: %v", err)
	}
	has := false
	for _, n := range names {
		if n == "Go" {
			has = true
		}
	}
	if !has {
		t.Errorf("Go template missing from list:\n%s", body)
	}

	resp, body = get(t, srv, "/gitignore/templates/Go")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status %d", resp.StatusCode)
	}
	var tmpl map[string]any
	if err := json.Unmarshal(body, &tmpl); err != nil {
		t.Fatalf("decode template: %v", err)
	}
	if tmpl["name"] != "Go" {
		t.Errorf("name = %v, want Go", tmpl["name"])
	}

	resp, _ = get(t, srv, "/gitignore/templates/Nonsense")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown template status %d, want 404", resp.StatusCode)
	}
}

// TestFeedsContract checks the feeds object carries the timeline and _links.
func TestFeedsContract(t *testing.T) {
	srv := testServer(t)
	resp, body := get(t, srv, "/feeds")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var f map[string]any
	if err := json.Unmarshal(body, &f); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := f["timeline_url"]; !ok {
		t.Errorf("feeds missing timeline_url:\n%s", body)
	}
	if _, ok := f["_links"]; !ok {
		t.Errorf("feeds missing _links:\n%s", body)
	}
}

// TestRateLimitStubBuckets checks the five added buckets are present and report
// their full budget.
func TestRateLimitStubBuckets(t *testing.T) {
	srv := testServer(t)
	resp, body := get(t, srv, "/rate_limit")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var rl struct {
		Resources map[string]struct {
			Limit     int    `json:"limit"`
			Remaining int    `json:"remaining"`
			Resource  string `json:"resource"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(body, &rl); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, name := range []string{"source_import", "actions_runner_registration", "scim", "dependency_snapshots", "audit_log"} {
		b, ok := rl.Resources[name]
		if !ok {
			t.Errorf("rate_limit missing %s bucket", name)
			continue
		}
		if b.Limit == 0 || b.Remaining != b.Limit {
			t.Errorf("%s bucket = limit %d remaining %d, want full budget", name, b.Limit, b.Remaining)
		}
		if b.Resource != name {
			t.Errorf("%s bucket resource = %q", name, b.Resource)
		}
	}
}

// TestMarkdownRenderContract checks POST /markdown renders to HTML, that the gfm
// mode is accepted, and that /markdown/raw renders a raw body.
func TestMarkdownRenderContract(t *testing.T) {
	srv := markupServer(t)

	resp, body := postBody(t, srv, "/markdown", "application/json", `{"text":"# Title\n\nHello **world**"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content type %q, want text/html", ct)
	}
	if !strings.Contains(string(body), "<h1") || !strings.Contains(string(body), "<strong>world</strong>") {
		t.Errorf("markdown not rendered to HTML:\n%s", body)
	}

	resp, body = postBody(t, srv, "/markdown", "application/json", `{"text":"a list:\n\n- one\n- two","mode":"gfm"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gfm status %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "<li>one</li>") {
		t.Errorf("gfm list not rendered:\n%s", body)
	}

	resp, body = postBody(t, srv, "/markdown/raw", "text/markdown", "plain *raw* text")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("raw status %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "<em>raw</em>") {
		t.Errorf("raw markdown not rendered:\n%s", body)
	}
}

// TestMarkdownUnmountedWithoutRenderer checks the markdown routes stay unmounted
// when no renderer is wired: the catch-all answers, not a 200.
func TestMarkdownUnmountedWithoutRenderer(t *testing.T) {
	srv := testServer(t)
	resp, _ := postBody(t, srv, "/markdown", "application/json", `{"text":"x"}`)
	if resp.StatusCode == http.StatusOK {
		t.Errorf("POST /markdown returned 200 without a renderer wired")
	}
}

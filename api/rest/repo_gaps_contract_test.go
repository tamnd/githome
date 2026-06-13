package rest

import (
	"net/http"
	"strings"
	"testing"
)

// TestBranchesProtectedFilter checks the protected query narrows the listing:
// the fixture's single branch is unprotected, so protected=false lists it and
// protected=true narrows to none.
func TestBranchesProtectedFilter(t *testing.T) {
	fx := repoServer(t)

	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/branches?protected=false", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"name":"`+fx.branch+`"`) {
		t.Errorf("protected=false dropped the unprotected branch:\n%s", body)
	}

	resp, body = authedGet(t, fx.srv, "/repos/octocat/hello/branches?protected=true", "token "+fx.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("protected=true should narrow to an empty list, got:\n%s", body)
	}
}

// TestContentsRawAccept checks GET contents with the vnd.github.raw media type
// returns the file's raw bytes as text/plain, not the base64 JSON object.
func TestContentsRawAccept(t *testing.T) {
	fx := repoServer(t)

	req, _ := http.NewRequest(http.MethodGet, fx.srv.URL+"/repos/octocat/hello/contents/README.md", nil)
	req.Header.Set("Authorization", "token "+fx.token)
	req.Header.Set("Accept", "application/vnd.github.raw")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := readAll(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("raw content type %q, want text/plain", ct)
	}
	if string(body) != "# Hello\n" {
		t.Errorf("raw body = %q, want %q", body, "# Hello\n")
	}
}

// TestContentsHTMLAccept checks the vnd.github.html media type renders the
// markdown file to HTML through the wired renderer.
func TestContentsHTMLAccept(t *testing.T) {
	fx := repoServer(t)

	req, _ := http.NewRequest(http.MethodGet, fx.srv.URL+"/repos/octocat/hello/contents/README.md", nil)
	req.Header.Set("Authorization", "token "+fx.token)
	req.Header.Set("Accept", "application/vnd.github.html")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := readAll(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("html content type %q, want text/html", ct)
	}
	if !strings.Contains(string(body), "<h1") || !strings.Contains(string(body), "Hello") {
		t.Errorf("html body did not render the heading:\n%s", body)
	}
}

// TestReadmeRawAccept checks the readme endpoint honors the same negotiation.
func TestReadmeRawAccept(t *testing.T) {
	fx := repoServer(t)

	req, _ := http.NewRequest(http.MethodGet, fx.srv.URL+"/repos/octocat/hello/readme", nil)
	req.Header.Set("Authorization", "token "+fx.token)
	req.Header.Set("Accept", "application/vnd.github.raw")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := readAll(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	if string(body) != "# Hello\n" {
		t.Errorf("readme raw body = %q, want %q", body, "# Hello\n")
	}
}

// TestReadmeInDirectory checks GET /readme/{dir}: a directory with no README is
// a 404, matching octokit's getReadmeInDirectory.
func TestReadmeInDirectory(t *testing.T) {
	fx := repoServer(t)
	// docs/ holds guide.md but no README, so the directory readme is a 404.
	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/readme/docs", "token "+fx.token)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("readme/docs status %d, want 404, body %s", resp.StatusCode, body)
	}
}

// readAll drains a response body.
func readAll(resp *http.Response) []byte {
	out := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			break
		}
	}
	return out
}

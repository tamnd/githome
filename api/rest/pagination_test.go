package rest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// seedIssues opens n issues on octocat/hello so a list spans several pages.
func seedIssues(t *testing.T, fx issueFixture, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
			fmt.Sprintf(`{"title":"issue %d"}`, i+1))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("seed issue %d: status %d, body %s", i+1, resp.StatusCode, body)
		}
	}
}

// linkRels parses a Link header into a rel -> value map. For page-number URLs
// the value is the page number string ("2"). For cursor URLs the value is the
// literal string "cursor" so tests can assert that cursor pagination is in use
// without pinning the opaque token value.
func linkRels(t *testing.T, header string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if header == "" {
		return out
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		lt := strings.IndexByte(part, '<')
		gt := strings.IndexByte(part, '>')
		if lt != 0 || gt < 0 {
			t.Fatalf("malformed link part %q", part)
		}
		target := part[1:gt]
		rel := ""
		if i := strings.Index(part, `rel="`); i >= 0 {
			rest := part[i+len(`rel="`):]
			rel = rest[:strings.IndexByte(rest, '"')]
		}
		q := target[strings.IndexByte(target, '?')+1:]
		value := ""
		for _, kv := range strings.Split(q, "&") {
			if strings.HasPrefix(kv, "page=") {
				value = strings.TrimPrefix(kv, "page=")
			}
			if strings.HasPrefix(kv, "cursor=") {
				value = "cursor" // opaque token; tests assert presence, not value
			}
		}
		out[rel] = value
	}
	return out
}

func TestPaginationLinkHeader(t *testing.T) {
	fx := issueServer(t)
	seedIssues(t, fx, 5) // 5 issues, per_page 2 -> 3 pages

	// First page: rel="next" is a cursor URL (keyset), rel="last" is page-based.
	resp, _ := get(t, fx.srv, "/repos/octocat/hello/issues?per_page=2&page=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page 1 status %d", resp.StatusCode)
	}
	rels := linkRels(t, resp.Header.Get("Link"))
	if rels["next"] != "cursor" {
		t.Errorf("page 1 next = %q, want cursor-based URL", rels["next"])
	}
	if rels["last"] != "3" {
		t.Errorf("page 1 last = %q, want 3", rels["last"])
	}
	if _, ok := rels["prev"]; ok {
		t.Errorf("page 1 should have no prev, got %+v", rels)
	}

	// Middle page accessed via explicit ?page=2: uses OFFSET, page-number rels.
	resp, _ = get(t, fx.srv, "/repos/octocat/hello/issues?per_page=2&page=2")
	rels = linkRels(t, resp.Header.Get("Link"))
	if rels["prev"] != "1" || rels["last"] != "3" || rels["first"] != "1" {
		t.Errorf("page 2 rels = %+v, want prev=1 last=3 first=1", rels)
	}
	// next is cursor-based since default sort applies
	if rels["next"] != "cursor" {
		t.Errorf("page 2 next = %q, want cursor-based URL", rels["next"])
	}

	// Last page: prev and first, no next/last.
	resp, _ = get(t, fx.srv, "/repos/octocat/hello/issues?per_page=2&page=3")
	rels = linkRels(t, resp.Header.Get("Link"))
	if rels["prev"] != "2" || rels["first"] != "1" {
		t.Errorf("page 3 rels = %+v, want prev=2 first=1", rels)
	}
	if _, ok := rels["next"]; ok {
		t.Errorf("page 3 should have no next, got %+v", rels)
	}
}

// nextPath returns the path and query of the rel="next" link as a server-local
// request target, or "" when there is no next link. It lets a test follow the
// keyset walk the way a client does, by replaying the URL the server handed back.
func nextPath(t *testing.T, header string) string {
	t.Helper()
	if header == "" {
		return ""
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		lt := strings.IndexByte(part, '<')
		gt := strings.IndexByte(part, '>')
		target := part[lt+1 : gt]
		slash := strings.Index(target, "/repos/")
		if slash < 0 {
			t.Fatalf("next link has no /repos/ path: %q", target)
		}
		return target[slash:]
	}
	return ""
}

// TestCursorWalkCoversAllIssues follows the rel="next" cursor from page to page
// the way a client does and checks the flat path returns every issue exactly
// once and stops cleanly. The walk never touches a page number, so it exercises
// the no-COUNT keyset path end to end.
func TestCursorWalkCoversAllIssues(t *testing.T) {
	fx := issueServer(t)
	seedIssues(t, fx, 7) // 7 issues, per_page 2 -> 4 pages

	seen := map[string]bool{}
	// Start from the first page's cursor next-link, then follow cursors only.
	path := "/repos/octocat/hello/issues?per_page=2&page=1"
	usedCursor := false
	for pages := 0; path != "" && pages < 20; pages++ {
		resp, body := get(t, fx.srv, path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("walk page status %d, body %s", resp.StatusCode, body)
		}
		var arr []map[string]any
		if err := json.Unmarshal(body, &arr); err != nil {
			t.Fatalf("decode page body: %v", err)
		}
		for _, item := range arr {
			title, _ := item["title"].(string)
			if seen[title] {
				t.Fatalf("issue %q returned twice during cursor walk", title)
			}
			seen[title] = true
		}
		next := nextPath(t, resp.Header.Get("Link"))
		if strings.Contains(next, "cursor=") {
			usedCursor = true
		}
		path = next
	}
	if !usedCursor {
		t.Fatalf("cursor walk never followed a cursor link")
	}
	if len(seen) != 7 {
		t.Fatalf("cursor walk covered %d issues, want 7", len(seen))
	}
}

func TestPaginationSinglePageHasNoLink(t *testing.T) {
	fx := issueServer(t)
	seedIssues(t, fx, 2)
	resp, _ := get(t, fx.srv, "/repos/octocat/hello/issues?per_page=30")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if link := resp.Header.Get("Link"); link != "" {
		t.Errorf("single page should carry no Link header, got %q", link)
	}
}

func TestPaginationPerPageClampAndBounds(t *testing.T) {
	fx := issueServer(t)
	seedIssues(t, fx, 1)

	// per_page over 100 is clamped, not rejected.
	if resp, body := get(t, fx.srv, "/repos/octocat/hello/issues?per_page=500"); resp.StatusCode != http.StatusOK {
		t.Errorf("per_page=500 status %d, want 200 (clamped), body %s", resp.StatusCode, body)
	}
	// A non-integer page is a 422 before any work.
	if resp, _ := get(t, fx.srv, "/repos/octocat/hello/issues?page=abc"); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("page=abc status %d, want 422", resp.StatusCode)
	}
	// per_page below 1 is a 422.
	if resp, _ := get(t, fx.srv, "/repos/octocat/hello/issues?per_page=0"); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("per_page=0 status %d, want 422", resp.StatusCode)
	}
}

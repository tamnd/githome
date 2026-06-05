package rest

import (
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

// linkRels parses a Link header into a rel -> page map, reading the page query
// of each link target so a test asserts navigation without pinning the host.
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
		page := ""
		for _, kv := range strings.Split(q, "&") {
			if strings.HasPrefix(kv, "page=") {
				page = strings.TrimPrefix(kv, "page=")
			}
		}
		out[rel] = page
	}
	return out
}

func TestPaginationLinkHeader(t *testing.T) {
	fx := issueServer(t)
	seedIssues(t, fx, 5) // 5 issues, per_page 2 -> 3 pages

	// First page: next and last, no prev/first.
	resp, _ := get(t, fx.srv, "/repos/octocat/hello/issues?per_page=2&page=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page 1 status %d", resp.StatusCode)
	}
	rels := linkRels(t, resp.Header.Get("Link"))
	if rels["next"] != "2" || rels["last"] != "3" {
		t.Errorf("page 1 rels = %+v, want next=2 last=3", rels)
	}
	if _, ok := rels["prev"]; ok {
		t.Errorf("page 1 should have no prev, got %+v", rels)
	}

	// Middle page: all four rels.
	resp, _ = get(t, fx.srv, "/repos/octocat/hello/issues?per_page=2&page=2")
	rels = linkRels(t, resp.Header.Get("Link"))
	if rels["prev"] != "1" || rels["next"] != "3" || rels["last"] != "3" || rels["first"] != "1" {
		t.Errorf("page 2 rels = %+v, want prev=1 next=3 last=3 first=1", rels)
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

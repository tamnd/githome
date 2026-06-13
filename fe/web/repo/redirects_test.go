package repo

import (
	"fmt"
	"net/http"
	"testing"
)

// TestCommitShortSHARedirects covers the short-SHA→full-SHA 301: an abbreviated
// hex SHA canonicalizes to the 40-char address (carrying the diff query), while
// a branch or tag name that resolves to the same commit keeps its own URL.
func TestCommitShortSHARedirects(t *testing.T) {
	fx := newFixture(t)

	short := fx.headSHA[:8]
	resp, _ := get(t, fx.srv, "/octocat/hello/commit/"+short)
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("short SHA: status %d, want 301", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/octocat/hello/commit/"+fx.headSHA {
		t.Errorf("short SHA Location = %q, want the full-SHA path", loc)
	}

	// The diff-axis query rides across the redirect.
	resp, _ = get(t, fx.srv, "/octocat/hello/commit/"+short+"?diff=split")
	if loc := resp.Header.Get("Location"); loc != "/octocat/hello/commit/"+fx.headSHA+"?diff=split" {
		t.Errorf("short SHA Location dropped the query: %q", loc)
	}

	// A branch name is not canonicalized: the page renders in place at /commit/master.
	resp, _ = get(t, fx.srv, "/octocat/hello/commit/master")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("branch ref: status %d, want 200 (no redirect)", resp.StatusCode)
	}
	// Neither is a tag name.
	resp, _ = get(t, fx.srv, "/octocat/hello/commit/v0.1.0")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("tag ref: status %d, want 200 (no redirect)", resp.StatusCode)
	}
}

// TestRepositoryByIDRedirects covers the numeric /repositories/{id} permalink:
// it 301s to the canonical /{owner}/{repo} path, a bad id is a 404, and a
// private repo the anonymous viewer cannot see is the same 404 (404-not-403).
func TestRepositoryByIDRedirects(t *testing.T) {
	fx := newFixture(t)

	resp, _ := get(t, fx.srv, fmt.Sprintf("/repositories/%d", fx.helloID))
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("hello id: status %d, want 301", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/octocat/hello" {
		t.Errorf("hello id Location = %q, want /octocat/hello", loc)
	}

	// A private repo is a hard 404 for the anonymous viewer, never a redirect
	// that would confirm the id exists.
	resp, _ = get(t, fx.srv, fmt.Sprintf("/repositories/%d", fx.secretID))
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("private id: status %d, want 404", resp.StatusCode)
	}

	for _, path := range []string{"/repositories/0", "/repositories/999999", "/repositories/abc"} {
		resp, _ := get(t, fx.srv, path)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, resp.StatusCode)
		}
	}
}

// TestIsAbbreviatedSHA unit-checks the hex-prefix rule the commit redirect uses.
func TestIsAbbreviatedSHA(t *testing.T) {
	const full = "1234567890abcdef1234567890abcdef12345678"
	cases := []struct {
		req  string
		want bool
	}{
		{"1234567", true},
		{"1234567890ABCDEF", true}, // case-insensitive
		{full, false},              // the full SHA is canonical, not abbreviated
		{"master", false},          // a ref name, not hex
		{"v0.1.0", false},
		{"", false},
		{"1234568", false}, // hex but not a prefix
		{"xyz", false},
	}
	for _, tc := range cases {
		if got := isAbbreviatedSHA(tc.req, full); got != tc.want {
			t.Errorf("isAbbreviatedSHA(%q) = %v, want %v", tc.req, got, tc.want)
		}
	}
}

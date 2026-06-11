package compare

import (
	"net/http"
	"strings"
	"testing"
)

// TestCompareDiffText covers /compare/{basehead}.diff: the raw diff of the
// range as plain text. The three-dot form diffs against the merge base, so
// master's own change after the fork point stays out; the two-dot form diffs
// the trees directly and includes it.
func TestCompareDiffText(t *testing.T) {
	srv := newFixture(t)

	resp, body := get(t, srv, "/octocat/hello/compare/master...feature.diff")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("three-dot: status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type %q, want text/plain", ct)
	}
	if !strings.Contains(body, "diff --git a/feature.txt b/feature.txt") {
		t.Errorf("three-dot diff is missing feature.txt:\n%.500s", body)
	}
	if strings.Contains(body, "master-only.txt") {
		t.Error("three-dot diff shows master's own change")
	}
	if strings.Contains(body, "<html") {
		t.Error("diff body carries HTML")
	}

	resp, body = get(t, srv, "/octocat/hello/compare/master..feature.diff")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("two-dot: status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "feature.txt") || !strings.Contains(body, "master-only.txt") {
		t.Error("two-dot diff must include both sides' changes")
	}
}

// TestComparePatchText covers /compare/{basehead}.patch: the range's own
// commits as an mbox series. Only feature's commit is in base..head.
func TestComparePatchText(t *testing.T) {
	srv := newFixture(t)
	resp, body := get(t, srv, "/octocat/hello/compare/master...feature.patch")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type %q, want text/plain", ct)
	}
	if !strings.Contains(body, "Subject: [PATCH] add the feature") {
		t.Errorf("patch is missing feature's commit:\n%.500s", body)
	}
	if strings.Contains(body, "add master-only") {
		t.Error("patch series carries a commit outside base..head")
	}
}

func TestCompareTextNotFound(t *testing.T) {
	srv := newFixture(t)
	for _, path := range []string{
		"/octocat/hello/compare/master...nope.diff",          // unknown head
		"/octocat/hello/compare/master...ghost:feature.diff", // foreign qualifier
		"/octocat/hello/compare/.diff",                       // nothing but the suffix
	} {
		resp, _ := get(t, srv, path)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, resp.StatusCode)
		}
	}
}

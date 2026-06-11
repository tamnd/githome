package pulls

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// TestPullDiffText covers /pull/{number}.diff: the PR's raw unified diff as
// plain text, the same body gh pr diff prints.
func TestPullDiffText(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/pull/"+strconv.FormatInt(fx.prNum, 10)+".diff")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type %q, want text/plain", ct)
	}
	if !strings.Contains(body, "diff --git a/b.txt b/b.txt") {
		t.Errorf("diff is missing the PR's file:\n%.500s", body)
	}
	if strings.Contains(body, "<html") {
		t.Error("diff body carries HTML")
	}
}

// TestPullPatchText covers /pull/{number}.patch: the PR's commits as an mbox
// patch series.
func TestPullPatchText(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/pull/"+strconv.FormatInt(fx.prNum, 10)+".patch")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type %q, want text/plain", ct)
	}
	if !strings.Contains(body, "Subject: [PATCH]") {
		t.Errorf("patch is missing a mail subject:\n%.500s", body)
	}
	if !strings.Contains(body, "b.txt") {
		t.Error("patch series is missing the PR's file")
	}
}

func TestPullTextNotFound(t *testing.T) {
	fx := newFixture(t)
	for _, path := range []string{
		"/octocat/hello/pull/999.diff",   // no such PR
		"/octocat/hello/pull/zero.patch", // not a number
		"/octocat/hello/pull/.diff",      // nothing but the suffix
		"/octocat/secret/pull/1.diff",    // private repo, anonymous viewer
	} {
		resp, _ := get(t, fx.srv, path)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, resp.StatusCode)
		}
	}
}

// TestPullTextPlainIssueNumber pins the crossover: the shared number sequence
// means a plain issue's number resolves under /pull/{n} as a redirect for the
// HTML page, but the text twins have no issue counterpart, so they 404.
func TestPullTextPlainIssueNumber(t *testing.T) {
	fx := newFixture(t)
	path := "/octocat/hello/pull/" + strconv.FormatInt(fx.issueNum, 10) + ".diff"
	resp, _ := get(t, fx.srv, path)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET %s: status %d, want 404", path, resp.StatusCode)
	}
}

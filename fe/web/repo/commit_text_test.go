package repo

import (
	"net/http"
	"strings"
	"testing"
)

// TestCommitDiffText covers /commit/{sha}.diff: the plain unified diff of the
// commit against its first parent, as text, not a rendered page.
func TestCommitDiffText(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/commit/"+fx.headSHA+".diff")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type %q, want text/plain", ct)
	}
	if !strings.Contains(body, "diff --git a/docs/guide.md b/docs/guide.md") {
		t.Errorf("diff is missing the guide hunk:\n%.500s", body)
	}
	if strings.Contains(body, "<html") {
		t.Error("diff body carries HTML")
	}
	// The .diff body is the full diff: the page-side inline cap (big.txt is
	// seeded past it) must not bite here.
	if !strings.Contains(body, "diff --git a/big.txt b/big.txt") {
		t.Error("diff lost the oversized file the page truncates")
	}
}

// TestCommitPatchText covers /commit/{sha}.patch: the commit as one
// format-patch mail, subject line and all.
func TestCommitPatchText(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat/hello/commit/"+fx.headSHA+".patch")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type %q, want text/plain", ct)
	}
	if !strings.Contains(body, "Subject: [PATCH] add the guide") {
		t.Errorf("patch is missing the mail subject:\n%.500s", body)
	}
	if !strings.Contains(body, "From: Octo Cat <octo@example.com>") {
		t.Error("patch is missing the author header")
	}
}

// TestCommitTextRootCommit covers the initial commit, which has no parent: the
// .diff form diffs against the empty tree (the HTML page shows nothing), and
// the .patch form still formats, since format-patch handles a root commit.
func TestCommitTextRootCommit(t *testing.T) {
	fx := newFixture(t)
	// v0.1.0 tags the first commit.
	resp, body := get(t, fx.srv, "/octocat/hello/commit/v0.1.0.diff")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("diff status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "diff --git a/README.md b/README.md") {
		t.Errorf("root-commit diff is missing the README against the empty tree:\n%.500s", body)
	}
	resp, body = get(t, fx.srv, "/octocat/hello/commit/v0.1.0.patch")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Subject: [PATCH] initial commit") {
		t.Errorf("root-commit patch is missing the subject:\n%.500s", body)
	}
}

func TestCommitTextNotFound(t *testing.T) {
	fx := newFixture(t)
	for _, path := range []string{
		"/octocat/hello/commit/0000000000000000000000000000000000000000.diff",
		"/octocat/hello/commit/no-such-ref.patch",
		"/octocat/hello/commit/.diff", // nothing but the suffix
	} {
		resp, _ := get(t, fx.srv, path)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, resp.StatusCode)
		}
	}
}

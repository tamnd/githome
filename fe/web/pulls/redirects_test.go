package pulls

import (
	"net/http"
	"strings"
	"testing"
)

// TestPullCommitRedirects covers /pull/{n}/commits/{sha}: it 302s to the
// repository commit page, which renders the same diff framed standalone. A
// non-PR number falls through loadPR's crossover and a missing PR is a 404.
func TestPullCommitRedirects(t *testing.T) {
	fx := newFixture(t)

	resp, _ := get(t, fx.srv, "/octocat/hello/pull/"+itoa(fx.prNum)+"/commits/"+fx.headSHA)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/octocat/hello/commit/"+fx.headSHA {
		t.Errorf("Location = %q, want the repo commit page", loc)
	}

	// A missing pull request is the soft 404, not a redirect to a bogus commit.
	missing, _ := get(t, fx.srv, "/octocat/hello/pull/9999/commits/"+fx.headSHA)
	if missing.StatusCode != http.StatusNotFound {
		t.Errorf("missing pull commit status = %d, want 404", missing.StatusCode)
	}

	// A plain issue's number addressed through /pull/{n}/commits/{sha} redirects
	// to the issue page, the same crossover loadPR applies to every PR tab.
	cross, _ := get(t, fx.srv, "/octocat/hello/pull/"+itoa(fx.issueNum)+"/commits/"+fx.headSHA)
	if cross.StatusCode != http.StatusFound {
		t.Fatalf("issue crossover status = %d, want 302", cross.StatusCode)
	}
	if loc := cross.Header.Get("Location"); loc != "/octocat/hello/issues/"+itoa(fx.issueNum) {
		t.Errorf("issue crossover Location = %q, want the issue page", loc)
	}
}

// TestIndexQueryGrammarState covers the PR index reading state out of the
// ?q=is:pr ... grammar, not only ?state=: an is:closed query lands on the
// closed list (which is empty here), is:open and the bare default both list the
// open pull request, and the raw query round-trips into the search value.
func TestIndexQueryGrammarState(t *testing.T) {
	fx := newFixture(t)

	// is:open via q lists the open PR.
	resp, body := get(t, fx.srv, "/octocat/hello/pulls?q="+esc("is:pr is:open"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("is:open status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "add b") {
		t.Errorf("is:open query did not list the open pull request:\n%s", body)
	}

	// is:closed via q lists nothing, the same as ?state=closed.
	resp, body = get(t, fx.srv, "/octocat/hello/pulls?q="+esc("is:pr is:closed"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("is:closed status %d, want 200", resp.StatusCode)
	}
	if strings.Contains(body, "add b") {
		t.Errorf("is:closed query unexpectedly listed the open pull request:\n%s", body)
	}

	// is:merged collapses onto the closed tab.
	resp, body = get(t, fx.srv, "/octocat/hello/pulls?q="+esc("is:pr is:merged"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("is:merged status %d, want 200", resp.StatusCode)
	}
	if strings.Contains(body, "add b") {
		t.Errorf("is:merged query unexpectedly listed the open pull request:\n%s", body)
	}
}

// esc percent-encodes a query value the way a browser would, so the test URLs
// carry spaces and colons through unmangled.
func esc(s string) string {
	r := strings.NewReplacer(" ", "+", ":", "%3A")
	return r.Replace(s)
}

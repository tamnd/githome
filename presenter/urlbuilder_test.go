package presenter

import (
	"testing"
)

// TestJoinFastPathMatchesURLJoin pins the fast join to url.URL.JoinPath for
// every shape of segment the presenters pass: plain route words and names take
// the one-allocation path, while anything needing escaping or path cleaning
// must fall through and still render exactly what JoinPath renders.
func TestJoinFastPathMatchesURLJoin(t *testing.T) {
	b := testBuilder(t)
	cases := [][]string{
		{"repos", "octocat", "hello"},
		{"users", "octo-cat"},
		{"repos", "octocat", "repo.with.dots", "issues"},
		{"repos", "octocat", "hello", "labels", "needs triage"},    // space escapes
		{"repos", "octocat", "hello", "contents", "docs/guide.md"}, // slash survives
		{"repos", "octocat", "hello", "compare", "main...feature"},
		{"a", "..", "b"}, // dot segment cleans
		{"a", "", "b"},   // empty segment collapses
		{"100%"},         // percent escapes
	}
	for _, segs := range cases {
		want := b.api.JoinPath(segs...).String()
		if got := b.API(segs...); got != want {
			t.Errorf("API(%q) = %q, want %q", segs, got, want)
		}
		wantHTML := b.html.JoinPath(segs...).String()
		if got := b.HTML(segs...); got != wantHTML {
			t.Errorf("HTML(%q) = %q, want %q", segs, got, wantHTML)
		}
	}
}

// TestSuffixLinks confirms the shared-backing link renderer produces the same
// strings as plain concatenation.
func TestSuffixLinks(t *testing.T) {
	base := "https://git.test.internal/api/v3/users/octocat"
	out := make([]string, len(userLinkSuffixes))
	suffixLinks(base, userLinkSuffixes[:], out)
	for i, suf := range userLinkSuffixes {
		if want := base + suf; out[i] != want {
			t.Errorf("link[%d] = %q, want %q", i, out[i], want)
		}
	}
}

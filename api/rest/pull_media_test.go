package rest

import "testing"

// TestPullMedia pins the Accept matching for the pull request raw views: only
// GitHub's vendor diff and patch media types flip the body, exactly matched
// per entry, and any other Accept value keeps the JSON default.
func TestPullMedia(t *testing.T) {
	cases := []struct {
		accept string
		want   int
	}{
		{"", mediaJSON},
		{"application/vnd.github+json", mediaJSON},
		{"application/vnd.github.diff", mediaDiff},
		{"application/vnd.github.v3.diff", mediaDiff},
		{"application/vnd.github.patch", mediaPatch},
		{"application/vnd.github.v3.patch", mediaPatch},
		{"Application/VND.GitHub.Diff", mediaDiff},
		{"application/vnd.github.diff; charset=utf-8", mediaDiff},
		{"application/json, application/vnd.github.patch", mediaPatch},
		{"application/vnd.github.diff,application/vnd.github.patch", mediaDiff},
		// Mentioning diff inside an unrelated value must not flip the body.
		{"multipart/mixed; boundary=diff", mediaJSON},
		{"application/vnd.acme.diff", mediaJSON},
		{"application/vnd.github.diff.extra", mediaJSON},
		{"text/x-patchwork", mediaJSON},
	}
	for _, tc := range cases {
		if got := pullMedia(tc.accept); got != tc.want {
			t.Errorf("pullMedia(%q) = %d, want %d", tc.accept, got, tc.want)
		}
	}
}

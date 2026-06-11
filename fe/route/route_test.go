package route

import "testing"

// The route package is pure, so these tests are the first arm of the route
// fidelity oracle: they pin the URL-space rules (reserved top-level names and the
// ref/path split) that the dispatcher and the tree and blob handlers depend on.
// See implementation/15 section 2.

func TestChecks(t *testing.T) {
	cases := map[string]string{
		"main":      "/octocat/hello/checks/main",
		"feature/x": "/octocat/hello/checks/feature/x",
		"deadbeef":  "/octocat/hello/checks/deadbeef",
		"a b":       "/octocat/hello/checks/a%20b",
	}
	for ref, want := range cases {
		if got := Checks("octocat", "hello", ref); got != want {
			t.Errorf("Checks(ref=%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestIsReservedTop(t *testing.T) {
	reserved := []string{"login", "settings", "search", "notifications", "new",
		"assets", "gist", "favicon.ico", "robots.txt",
		// The spec 02 §2.3 set: served, reserved-only, system mounts, and
		// well-known root paths all count.
		"about", "api", "billing", "blog", "codespaces", "customer-stories",
		"enterprise", "help", "marketplace", "open-source", "pricing", "pulse",
		"raw", "readme", "repositories", "security", "session", "signup",
		"sponsors", "stars", "topics", "trending", "watching", "wiki",
		"works-with", ".well-known"}
	for _, name := range reserved {
		if !IsReservedTop(name) {
			t.Errorf("expected %q to be reserved", name)
		}
	}

	// Case folds, so a reserved name cannot be taken by changing case.
	if !IsReservedTop("Settings") || !IsReservedTop("LOGIN") {
		t.Error("reserved check must be case-insensitive")
	}

	// A plausible user login is not reserved.
	for _, name := range []string{"octocat", "torvalds", "rails", "go"} {
		if IsReservedTop(name) {
			t.Errorf("did not expect %q to be reserved", name)
		}
	}
}

func TestCanonicalRepoPath(t *testing.T) {
	cases := []struct {
		name        string
		path, query string
		reqO, reqR  string
		owner, repo string
		want        string
		wantOK      bool
	}{
		{"already canonical", "/octocat/hello", "", "octocat", "hello", "octocat", "hello", "", false},
		{"wrong-case owner", "/Octocat/hello", "", "Octocat", "hello", "octocat", "hello", "/octocat/hello", true},
		{"wrong-case repo", "/octocat/Hello", "", "octocat", "Hello", "octocat", "hello", "/octocat/hello", true},
		// The sub-path keeps its exact bytes: refs and file paths are
		// case-sensitive, so only the first two segments are rewritten.
		{"sub-path preserved", "/Octocat/Hello/tree/Main/Docs", "", "Octocat", "Hello", "octocat", "hello",
			"/octocat/hello/tree/Main/Docs", true},
		{"query preserved", "/Octocat/hello/issues", "q=is%3Aopen", "Octocat", "hello", "octocat", "hello",
			"/octocat/hello/issues?q=is%3Aopen", true},
		// Escaped segments in the tail survive untouched.
		{"escaped tail", "/Octocat/hello/blob/main/a%20b.txt", "", "Octocat", "hello", "octocat", "hello",
			"/octocat/hello/blob/main/a%20b.txt", true},
		// A rename: the request spelling maps to a different name entirely.
		{"renamed repo", "/octocat/old-name/pulls", "", "octocat", "old-name", "octocat", "new-name",
			"/octocat/new-name/pulls", true},
		// An incomplete canonical pair never redirects.
		{"missing owner", "/Octocat/hello", "", "Octocat", "hello", "", "hello", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := CanonicalRepoPath(tc.path, tc.query, tc.reqO, tc.reqR, tc.owner, tc.repo)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("CanonicalRepoPath(%q, %q, %q, %q, %q, %q) = (%q, %v), want (%q, %v)",
					tc.path, tc.query, tc.reqO, tc.reqR, tc.owner, tc.repo, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestSplitRefPath(t *testing.T) {
	// The repository has a branch "main", a slash-containing branch
	// "release/1.0", and a tag "v2". Anything else does not resolve.
	refs := map[string]bool{"main": true, "release/1.0": true, "v2": true}
	exists := func(ref string) bool { return refs[ref] }

	cases := []struct {
		tail     string
		wantRef  string
		wantPath string
		wantOK   bool
	}{
		{"main/cmd/githome/main.go", "main", "cmd/githome/main.go", true},
		{"main", "main", "", true},
		{"release/1.0/README.md", "release/1.0", "README.md", true},
		{"release/1.0", "release/1.0", "", true},
		{"v2/docs", "v2", "docs", true},
		{"nope/file.go", "", "", false},
		{"", "", "", false},
		// A path under a branch whose name is a prefix of another ref still picks
		// the exact ref, not the longer string.
		{"main/release/1.0", "main", "release/1.0", true},
	}
	for _, tc := range cases {
		ref, path, ok := SplitRefPath(tc.tail, exists)
		if ref != tc.wantRef || path != tc.wantPath || ok != tc.wantOK {
			t.Errorf("SplitRefPath(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.tail, ref, path, ok, tc.wantRef, tc.wantPath, tc.wantOK)
		}
	}
}

func TestSplitRefPathPrefersLongestRef(t *testing.T) {
	// Both "feature" and "feature/x" are branches. A URL for "feature/x/file"
	// must resolve to the longer branch, matching git's own preference.
	refs := map[string]bool{"feature": true, "feature/x": true}
	exists := func(ref string) bool { return refs[ref] }

	ref, path, ok := SplitRefPath("feature/x/file.go", exists)
	if !ok || ref != "feature/x" || path != "file.go" {
		t.Errorf("got (%q, %q, %v), want (feature/x, file.go, true)", ref, path, ok)
	}
}

func TestFirstSegment(t *testing.T) {
	cases := []struct{ in, head, rest string }{
		{"/octocat/hello-world/tree/main", "octocat", "hello-world/tree/main"},
		{"octocat", "octocat", ""},
		{"/octocat", "octocat", ""},
		{"/", "", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		head, rest := FirstSegment(tc.in)
		if head != tc.head || rest != tc.rest {
			t.Errorf("FirstSegment(%q) = (%q, %q), want (%q, %q)", tc.in, head, rest, tc.head, tc.rest)
		}
	}
}

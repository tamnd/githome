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

// fakeRefs is the test RefLookup: fixed branch and tag sets, and a commit-ish
// set for the sha and HEAD forms.
type fakeRefs struct {
	branches map[string]bool
	tags     map[string]bool
	commits  map[string]bool
}

func (f fakeRefs) Branch(name string) bool   { return f.branches[name] }
func (f fakeRefs) Tag(name string) bool      { return f.tags[name] }
func (f fakeRefs) Commitish(rev string) bool { return f.commits[rev] }

func TestSplitRefPath(t *testing.T) {
	// The repository has a branch "main", a slash-containing branch
	// "release/1.0", a tag "v2", and a name "dual" that is both a branch and a
	// tag. HEAD and one abbreviated sha resolve as commit-ish. Anything else
	// does not resolve.
	refs := fakeRefs{
		branches: map[string]bool{"main": true, "release/1.0": true, "dual": true},
		tags:     map[string]bool{"v2": true, "dual": true},
		commits:  map[string]bool{"HEAD": true, "abc1234": true},
	}

	cases := []struct {
		tail     string
		wantRef  string
		wantKind RefMatch
		wantPath string
		wantOK   bool
	}{
		{"main/cmd/githome/main.go", "main", RefBranch, "cmd/githome/main.go", true},
		{"main", "main", RefBranch, "", true},
		{"release/1.0/README.md", "release/1.0", RefBranch, "README.md", true},
		{"release/1.0", "release/1.0", RefBranch, "", true},
		{"v2/docs", "v2", RefTag, "docs", true},
		{"abc1234/docs", "abc1234", RefCommit, "docs", true},
		{"nope/file.go", "", RefNone, "", false},
		{"", "", RefNone, "", false},
		// A path under a branch whose name is a prefix of another ref still picks
		// the exact ref, not the longer string.
		{"main/release/1.0", "main", RefBranch, "release/1.0", true},
		// A name that is both a branch and a tag resolves to the branch, the
		// precedence github.com documents.
		{"dual/docs", "dual", RefBranch, "docs", true},
		// The qualified forms name their namespace outright and yield the short
		// name, so the page links stay in the short form. The tag side is the only
		// way to address the tag half of a dual name.
		{"refs/heads/main/cmd/main.go", "main", RefBranch, "cmd/main.go", true},
		{"refs/heads/release/1.0/README.md", "release/1.0", RefBranch, "README.md", true},
		{"refs/tags/v2", "v2", RefTag, "", true},
		{"refs/tags/dual/docs", "dual", RefTag, "docs", true},
		// A qualified name in the wrong namespace does not resolve: main is not
		// a tag.
		{"refs/tags/main", "", RefNone, "", false},
		{"refs/heads/v2", "", RefNone, "", false},
		// HEAD resolves as a commit-ish, alone or with a path.
		{"HEAD", "HEAD", RefCommit, "", true},
		{"HEAD/docs/guide.md", "HEAD", RefCommit, "docs/guide.md", true},
	}
	for _, tc := range cases {
		ref, kind, path, ok := SplitRefPath(tc.tail, refs)
		if ref != tc.wantRef || kind != tc.wantKind || path != tc.wantPath || ok != tc.wantOK {
			t.Errorf("SplitRefPath(%q) = (%q, %v, %q, %v), want (%q, %v, %q, %v)",
				tc.tail, ref, kind, path, ok, tc.wantRef, tc.wantKind, tc.wantPath, tc.wantOK)
		}
	}
}

func TestSplitRefPathPrefersLongestRef(t *testing.T) {
	// Both "feature" and "feature/x" are branches. A URL for "feature/x/file"
	// must resolve to the longer branch, matching git's own preference.
	refs := fakeRefs{
		branches: map[string]bool{"feature": true, "feature/x": true},
	}

	ref, kind, path, ok := SplitRefPath("feature/x/file.go", refs)
	if !ok || ref != "feature/x" || kind != RefBranch || path != "file.go" {
		t.Errorf("got (%q, %v, %q, %v), want (feature/x, RefBranch, file.go, true)", ref, kind, path, ok)
	}
}

func TestParseBaseHead(t *testing.T) {
	cases := []struct {
		in     string
		want   CompareSpec
		wantOK bool
	}{
		{"main...feature", CompareSpec{Base: CompareSide{Ref: "main"}, Head: CompareSide{Ref: "feature"}}, true},
		{"main..feature", CompareSpec{Base: CompareSide{Ref: "main"}, Head: CompareSide{Ref: "feature"}, TwoDot: true}, true},
		// No separator: the whole string is the head, the caller substitutes
		// the default branch as base.
		{"feature", CompareSpec{Head: CompareSide{Ref: "feature"}}, true},
		// Dots inside a ref survive; only the separator splits.
		{"release-1.2...main", CompareSpec{Base: CompareSide{Ref: "release-1.2"}, Head: CompareSide{Ref: "main"}}, true},
		// The qualified sides: owner:ref and owner:repo:ref.
		{"main...octocat:feature", CompareSpec{Base: CompareSide{Ref: "main"}, Head: CompareSide{Owner: "octocat", Ref: "feature"}}, true},
		{"octocat:hello:main...feature", CompareSpec{Base: CompareSide{Owner: "octocat", Repo: "hello", Ref: "main"}, Head: CompareSide{Ref: "feature"}}, true},
		{"a:b:c...d:e:f", CompareSpec{Base: CompareSide{Owner: "a", Repo: "b", Ref: "c"}, Head: CompareSide{Owner: "d", Repo: "e", Ref: "f"}}, true},
		// Malformed: empty sides, empty components, too many colons.
		{"", CompareSpec{}, false},
		{"...feature", CompareSpec{}, false},
		{"main...", CompareSpec{}, false},
		{"main..", CompareSpec{}, false},
		{"main...:feature", CompareSpec{}, false},
		{"a:b:c:d...main", CompareSpec{}, false},
		{"octocat:...main", CompareSpec{}, false},
	}
	for _, tc := range cases {
		got, ok := ParseBaseHead(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("ParseBaseHead(%q) = (%+v, %v), want (%+v, %v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
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

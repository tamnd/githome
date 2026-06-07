package route

import "testing"

// These pin the pull-request URL shapes the templates link to. The load-bearing
// invariant is GitHub's singular/plural split: the index is /pulls but a single
// PR lives under /pull/{n}, and a drift there would 404 every PR link. The query
// helpers must also keep a filter value a single, properly encoded q parameter.

func TestPullsIndex(t *testing.T) {
	if got := Pulls("octocat", "hello", ""); got != "/octocat/hello/pulls" {
		t.Errorf("Pulls bare = %q, want /octocat/hello/pulls", got)
	}
	if got := Pulls("octocat", "hello", "page=2"); got != "/octocat/hello/pulls?page=2" {
		t.Errorf("Pulls with rawQuery = %q, want ...?page=2", got)
	}
}

func TestPullsQueryEncodesValue(t *testing.T) {
	// A filter with a space and a colon must stay one q parameter, percent encoded.
	got := PullsQuery("octocat", "hello", "is:open review:required")
	want := "/octocat/hello/pulls?q=is%3Aopen+review%3Arequired"
	if got != want {
		t.Errorf("PullsQuery = %q, want %q", got, want)
	}
	if got := PullsQuery("octocat", "hello", ""); got != "/octocat/hello/pulls" {
		t.Errorf("PullsQuery empty = %q, want the bare index", got)
	}
}

func TestPullIsSingular(t *testing.T) {
	if got := Pull("octocat", "hello", 42); got != "/octocat/hello/pull/42" {
		t.Errorf("Pull = %q, want /octocat/hello/pull/42 (singular)", got)
	}
}

func TestPullTabs(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"commits", PullCommits("octocat", "hello", 42), "/octocat/hello/pull/42/commits"},
		{"files", PullFiles("octocat", "hello", 42), "/octocat/hello/pull/42/files"},
		{"comments", PullComments("octocat", "hello", 42), "/octocat/hello/pull/42/comments"},
		{"merge", PullMerge("octocat", "hello", 42), "/octocat/hello/pull/42/merge"},
		{"mergebox", PullMergeBox("octocat", "hello", 42), "/octocat/hello/pull/42/partials/merge-box"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestPullCommentAnchor(t *testing.T) {
	// A PR shares the issue number space, so its conversation comment carries the
	// same issuecomment anchor the issues timeline uses.
	got := PullComment("octocat", "hello", 42, 7)
	want := "/octocat/hello/pull/42#issuecomment-7"
	if got != want {
		t.Errorf("PullComment = %q, want %q", got, want)
	}
}

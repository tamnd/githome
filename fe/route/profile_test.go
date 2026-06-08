package route

import "testing"

// The profile builders are pure, so these tests pin the /{owner} URL space the
// header viewer menu, the activity actor links, and the repository owner links all
// route through. See implementation/12 sections 5 and 6.

func TestProfile(t *testing.T) {
	if got := Profile("octocat"); got != "/octocat" {
		t.Errorf("Profile = %q, want /octocat", got)
	}
	// A login with a character that needs escaping is escaped per segment.
	if got := Profile("a b"); got != "/a%20b" {
		t.Errorf("Profile escaping = %q, want /a%%20b", got)
	}
}

func TestProfileTab(t *testing.T) {
	cases := []struct {
		login, tab, want string
	}{
		// The overview tab is the bare profile, never a redundant ?tab=overview.
		{"octocat", "", "/octocat"},
		{"octocat", "overview", "/octocat"},
		{"octocat", "repositories", "/octocat?tab=repositories"},
	}
	for _, c := range cases {
		if got := ProfileTab(c.login, c.tab); got != c.want {
			t.Errorf("ProfileTab(%q, %q) = %q, want %q", c.login, c.tab, got, c.want)
		}
	}
}

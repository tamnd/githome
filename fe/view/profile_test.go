package view

import "testing"

// ProfileTabOr is the view's tolerance for a hand-edited ?tab=: it validates
// against the backed tabs and degrades an unknown or empty value to the
// overview rather than erroring. See implementation/12 section 5.

func TestProfileTabOr(t *testing.T) {
	cases := []struct {
		raw, want string
	}{
		{"", ProfileOverview},
		{"overview", ProfileOverview},
		{"repositories", ProfileRepositories},
		{"stars", ProfileStars},
		{"followers", ProfileFollowers},
		{"following", ProfileFollowing},
		// An unknown tab falls back to the overview, never an error or a blank page.
		{"projects", ProfileOverview},
		{"REPOSITORIES", ProfileOverview}, // case-sensitive: only the exact key selects the tab
	}
	for _, c := range cases {
		if got := ProfileTabOr(c.raw); got != c.want {
			t.Errorf("ProfileTabOr(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

package view

import "testing"

func TestAppearanceOptionsMarkSelected(t *testing.T) {
	modes := AppearanceModeOptions("dark")
	var sawSelected bool
	for _, o := range modes {
		if o.Value == "dark" {
			if !o.Selected {
				t.Errorf("dark mode not marked selected")
			}
			sawSelected = true
		} else if o.Selected {
			t.Errorf("mode %q marked selected, want only dark", o.Value)
		}
	}
	if !sawSelected {
		t.Fatalf("dark mode missing from the catalog")
	}
}

// TestMarkSelectedDoesNotMutateCatalog guards against a request leaking its
// selection into the shared catalog, which would mark the wrong option on the
// next request.
func TestMarkSelectedDoesNotMutateCatalog(t *testing.T) {
	_ = LightThemeOptions("light_tritanopia")
	again := LightThemeOptions("light")
	for _, o := range again {
		if o.Value == "light_tritanopia" && o.Selected {
			t.Errorf("catalog mutated: light_tritanopia stayed selected")
		}
	}
}

func TestValidMode(t *testing.T) {
	for _, m := range []string{"auto", "light", "dark"} {
		if !ValidMode(m) {
			t.Errorf("ValidMode(%q) = false, want true", m)
		}
	}
	for _, m := range []string{"", "neon", "Dark", "system"} {
		if ValidMode(m) {
			t.Errorf("ValidMode(%q) = true, want false", m)
		}
	}
}

func TestValidThemesAreSlotted(t *testing.T) {
	// A light theme is valid only in the light slot and a dark theme only in the
	// dark slot, so a hand-edited cookie cannot put a dark theme in the light slot.
	if !ValidLightTheme("light_high_contrast") || ValidLightTheme("dark") {
		t.Errorf("light slot accepted the wrong theme")
	}
	if !ValidDarkTheme("dark_dimmed") || ValidDarkTheme("light") {
		t.Errorf("dark slot accepted the wrong theme")
	}
}

func TestHookEventChoicesChecksSubscription(t *testing.T) {
	choices := HookEventChoices([]string{"push", "issues"})
	want := map[string]bool{"push": true, "issues": true}
	for _, c := range choices {
		if c.Checked != want[c.Value] {
			t.Errorf("event %q checked=%v, want %v", c.Value, c.Checked, want[c.Value])
		}
	}
}

func TestHookEventChoicesWildcardChecksNothing(t *testing.T) {
	// The wildcard drives the separate "everything" control, so no individual box
	// is checked when the hook subscribes to all events.
	for _, c := range HookEventChoices([]string{"*"}) {
		if c.Checked {
			t.Errorf("event %q checked under a wildcard subscription", c.Value)
		}
	}
	if !HookSubscribesAll([]string{"*"}) {
		t.Errorf("HookSubscribesAll([\"*\"]) = false, want true")
	}
}

func TestEventsSummary(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"*"}, "everything"},
		{nil, "push"},
		{[]string{"push", "pull_request"}, "push, pull_request"},
	}
	for _, c := range cases {
		if got := EventsSummary(c.in); got != c.want {
			t.Errorf("EventsSummary(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHookStatusGlyph(t *testing.T) {
	cases := []struct {
		status string
		icon   string
		kind   string
	}{
		{"OK", "check-circle", "success"},
		{"unused", "dot-fill", "muted"},
		{"", "dot-fill", "muted"},
		{"failed to connect", "x-circle", "danger"},
		{"Invalid HTTP Response: 500", "x-circle", "danger"},
	}
	for _, c := range cases {
		icon, kind := HookStatusGlyph(c.status)
		if icon != c.icon || kind != c.kind {
			t.Errorf("HookStatusGlyph(%q) = (%q,%q), want (%q,%q)", c.status, icon, kind, c.icon, c.kind)
		}
	}
}

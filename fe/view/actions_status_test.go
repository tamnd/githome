package view

import (
	"testing"

	"github.com/tamnd/githome/fe/assets"
)

// knownColorClass reports whether a token's color class is one of the four
// check-state classes the stylesheet defines. A token that returns a class the CSS
// does not style would render uncolored, so the vocabulary and the stylesheet are
// pinned together here.
func knownColorClass(c string) bool {
	switch c {
	case checkStateSuccess, checkStateDanger, checkStatePending, checkStateMuted:
		return true
	}
	return false
}

// assertToken checks the invariants every token must hold: a non-empty title, a
// color class the CSS styles, and an icon registered in the asset set. The icon
// check is the one the template coverage test cannot make, because the templates
// print a precomputed .Token.Icon field rather than a literal octicon name, so an
// unregistered icon would silently render the dashed placeholder without this.
func assertToken(t *testing.T, label string, tok StatusToken) {
	t.Helper()
	if tok.Title == "" {
		t.Errorf("%s: empty title", label)
	}
	if !knownColorClass(tok.ColorClass) {
		t.Errorf("%s: color class %q is not a styled check-state class", label, tok.ColorClass)
	}
	if _, ok := assets.Icons[tok.Icon]; !ok {
		t.Errorf("%s: icon %q is not registered in assets.Icons (it would render as a placeholder)", label, tok.Icon)
	}
}

func TestCheckRunTokenCoversTheEnum(t *testing.T) {
	// The full status x conclusion cross-product Spec 2003 doc 05 pins. A renamed or
	// added enum value that this table does not cover still gets a token (the
	// defaults), so the assert catches a drift to an unstyled class or unregistered
	// icon rather than a panic.
	statuses := []string{"queued", "in_progress", "waiting", "requested", "pending", "completed"}
	conclusions := []string{"", "success", "failure", "neutral", "cancelled", "timed_out", "action_required", "skipped", "stale"}
	for _, st := range statuses {
		for _, cc := range conclusions {
			assertToken(t, "check run "+st+"/"+cc, CheckRunToken(st, cc))
		}
	}
}

func TestCheckRunTokenInProgressSpins(t *testing.T) {
	if !CheckRunToken("in_progress", "").Spin {
		t.Fatal("an in-progress check should carry the spin flag")
	}
	// No other state animates: a completed or queued check is a static glyph.
	for _, st := range []string{"queued", "completed", "waiting"} {
		if CheckRunToken(st, "success").Spin {
			t.Errorf("status %q should not spin", st)
		}
	}
}

func TestCheckRunTokenConclusions(t *testing.T) {
	cases := map[string]struct {
		icon  string
		color string
	}{
		"success":         {"check-circle", checkStateSuccess},
		"failure":         {"x-circle", checkStateDanger},
		"timed_out":       {"x-circle", checkStateDanger},
		"cancelled":       {"x-circle", checkStateMuted},
		"action_required": {"alert", checkStatePending},
		"skipped":         {"skip", checkStateMuted},
		"neutral":         {"dot-fill", checkStateMuted},
	}
	for concl, want := range cases {
		tok := CheckRunToken("completed", concl)
		if tok.Icon != want.icon || tok.ColorClass != want.color {
			t.Errorf("completed/%s = {%s,%s}, want {%s,%s}", concl, tok.Icon, tok.ColorClass, want.icon, want.color)
		}
	}
}

func TestCommitStatusTokenCoversTheEnum(t *testing.T) {
	for _, state := range []string{"error", "failure", "pending", "success"} {
		assertToken(t, "commit status "+state, CommitStatusToken(state))
	}
	if CommitStatusToken("success").ColorClass != checkStateSuccess {
		t.Error("a successful commit status should be the success color")
	}
	if CommitStatusToken("error").ColorClass != checkStateDanger {
		t.Error("an errored commit status should fold into the danger color")
	}
}

func TestRollupTokenCoversTheEnum(t *testing.T) {
	for _, state := range []string{"ERROR", "FAILURE", "PENDING", "SUCCESS", "EXPECTED"} {
		assertToken(t, "rollup "+state, RollupToken(state))
	}
	if RollupToken("EXPECTED").ColorClass != checkStatePending {
		t.Error("an expected-but-unreported rollup should read as pending, not as a pass")
	}
	if RollupToken("SUCCESS").Icon != "check-circle" {
		t.Error("a passing rollup should show the success check")
	}
}

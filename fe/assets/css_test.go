package assets

import (
	"os"
	"strings"
	"testing"
)

// TestAppHeaderUsesFunctionalTokens guards review 02 task R02-10. The header is
// canvas-colored in every theme, so nothing inside it may use the emphasis
// palette or white-alpha literals: those are leftovers from a dark header and
// render white-on-white in the four light themes. Every AppHeader rule must
// read functional tokens, which re-theme with the canvas.
func TestAppHeaderUsesFunctionalTokens(t *testing.T) {
	src, err := os.ReadFile("src/css/components.css")
	if err != nil {
		t.Fatalf("read components.css: %v", err)
	}
	for _, rule := range cssRules(string(src)) {
		if !strings.Contains(rule.selector, "AppHeader") {
			continue
		}
		if strings.Contains(rule.body, "rgba(255") {
			t.Errorf("rule %q uses a white-alpha literal; use a functional token so all themes recolor it:\n%s", rule.selector, rule.body)
		}
		if strings.Contains(rule.body, "onEmphasis") {
			t.Errorf("rule %q uses the emphasis palette on the canvas-colored header:\n%s", rule.selector, rule.body)
		}
	}

	// The search input is the piece that was invisible: it must sit on the
	// inset background with default border and text.
	input := ruleBody(t, string(src), ".AppHeader-search .input")
	for _, want := range []string{"var(--bgColor-inset)", "var(--borderColor-default)", "var(--fgColor-default)"} {
		if !strings.Contains(input, want) {
			t.Errorf(".AppHeader-search .input is missing %s:\n%s", want, input)
		}
	}
	placeholder := ruleBody(t, string(src), ".AppHeader-search .input::placeholder")
	if !strings.Contains(placeholder, "var(--fgColor-muted)") {
		t.Errorf("search placeholder must be fgColor-muted:\n%s", placeholder)
	}
}

// TestPrimerVocabularyPresent guards review 02 task R02-11: the component
// sheet must carry the Primer vocabulary pages compose from, and the pieces
// that encode state must read the role tokens so the color vision themes
// recolor them.
func TestPrimerVocabularyPresent(t *testing.T) {
	src, err := os.ReadFile("src/css/components.css")
	if err != nil {
		t.Fatalf("read components.css: %v", err)
	}
	for _, sel := range []string{
		".Counter", ".Label", ".State", ".State--open", ".State--closed", ".State--merged",
		".Box", ".Box-header", ".Box-row", ".Subhead", ".Subhead-heading", ".BtnGroup",
		".btn-invisible", ".topic-tag",
	} {
		ruleBody(t, string(src), sel)
	}
	if body := ruleBody(t, string(src), ".State--open"); !strings.Contains(body, "var(--bgColor-open-emphasis)") {
		t.Errorf(".State--open must read the open role token:\n%s", body)
	}
	if body := ruleBody(t, string(src), ".State--closed"); !strings.Contains(body, "var(--bgColor-closed-emphasis)") {
		t.Errorf(".State--closed must read the closed role token:\n%s", body)
	}
}

// cssRule is one selector { body } pair lifted out of a sheet.
type cssRule struct {
	selector string
	body     string
}

// cssRules splits a flat sheet (no nesting, no at-rules in components.css)
// into its rules. Comments are stripped first so a token name in prose does
// not trip an assertion.
func cssRules(src string) []cssRule {
	for {
		start := strings.Index(src, "/*")
		if start < 0 {
			break
		}
		end := strings.Index(src[start:], "*/")
		if end < 0 {
			src = src[:start]
			break
		}
		src = src[:start] + src[start+end+2:]
	}
	var rules []cssRule
	for {
		open := strings.Index(src, "{")
		if open < 0 {
			break
		}
		closing := strings.Index(src[open:], "}")
		if closing < 0 {
			break
		}
		rules = append(rules, cssRule{
			selector: strings.TrimSpace(src[:open]),
			body:     strings.TrimSpace(src[open+1 : open+closing]),
		})
		src = src[open+closing+1:]
	}
	return rules
}

// ruleBody returns the joined bodies of every rule with exactly the given
// selector (a sheet may declare layout and color in separate blocks), failing
// the test when the sheet no longer has it.
func ruleBody(t *testing.T, src, selector string) string {
	t.Helper()
	var bodies []string
	for _, r := range cssRules(src) {
		if r.selector == selector {
			bodies = append(bodies, r.body)
		}
	}
	if len(bodies) == 0 {
		t.Fatalf("components.css has no rule %q", selector)
	}
	return strings.Join(bodies, "\n")
}

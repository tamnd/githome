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

// TestUnderlineNavReadsItsTokens guards review 02 task R02-12: the selected
// repo tab was underlined with an accent-muted fill (invisible on the muted
// border) and the PR tabs with an undefined attention token. Both bars must
// read the shared underlineNav tokens.
func TestUnderlineNavReadsItsTokens(t *testing.T) {
	repoSrc, err := os.ReadFile("src/css/repo.css")
	if err != nil {
		t.Fatalf("read repo.css: %v", err)
	}
	if body := ruleBody(t, string(repoSrc), ".reponav-item.is-current"); !strings.Contains(body, "var(--underlineNav-borderColor-active)") {
		t.Errorf(".reponav-item.is-current must read the underlineNav active token:\n%s", body)
	}
	pullsSrc, err := os.ReadFile("src/css/pulls.css")
	if err != nil {
		t.Fatalf("read pulls.css: %v", err)
	}
	if body := ruleBody(t, string(pullsSrc), ".pr-tab.is-current"); !strings.Contains(body, "var(--underlineNav-borderColor-active)") {
		t.Errorf(".pr-tab.is-current must read the underlineNav active token:\n%s", body)
	}
}

// TestButtonsReadControlTokens guards review 02 tasks R02-15 and R02-16. The
// default button hover was a no-op (bgColor-subtle equals the muted rest
// face) and the primary button faked its hover with a brightness filter over
// a role token. Both must read their control and button component tokens so
// every theme gets distinct rest, hover, and active steps.
func TestButtonsReadControlTokens(t *testing.T) {
	src, err := os.ReadFile("src/css/components.css")
	if err != nil {
		t.Fatalf("read components.css: %v", err)
	}
	checks := map[string]string{
		".btn":                "var(--control-bgColor-rest)",
		".btn:hover":          "var(--control-bgColor-hover)",
		".btn:active":         "var(--control-bgColor-active)",
		".btn-primary":        "var(--button-primary-bgColor-rest)",
		".btn-primary:hover":  "var(--button-primary-bgColor-hover)",
		".btn-primary:active": "var(--button-primary-bgColor-active)",
	}
	for sel, want := range checks {
		if body := ruleBody(t, string(src), sel); !strings.Contains(body, want) {
			t.Errorf("%s must read %s:\n%s", sel, want, body)
		}
	}
	if body := ruleBody(t, string(src), ".btn-primary"); !strings.Contains(body, "var(--button-primary-borderColor-rest)") {
		t.Errorf(".btn-primary must use the translucent component border:\n%s", body)
	}
	if body := ruleBody(t, string(src), ".btn-primary:hover"); strings.Contains(body, "filter") {
		t.Errorf(".btn-primary:hover must not fake the hover with a filter:\n%s", body)
	}
}

// TestHeaderCollapsesBelowMd guards review 02 task R02-40: the global header
// must collapse below the 768px breakpoint instead of squeezing the logo,
// search, and nav onto one phone-width row. The collapse is a pure CSS wrap
// (search drops to a full-width second row), so it works with scripting off.
func TestHeaderCollapsesBelowMd(t *testing.T) {
	src, err := os.ReadFile("src/css/components.css")
	if err != nil {
		t.Fatalf("read components.css: %v", err)
	}
	at := strings.Index(string(src), "@media (max-width: 767.98px)")
	if at < 0 {
		t.Fatal("components.css has no md-down media query for the header collapse")
	}
	block := string(src)[at:]
	for _, want := range []string{"flex-wrap: wrap", "flex-basis: 100%", "max-width: none", "order:"} {
		if !strings.Contains(block, want) {
			t.Errorf("header md-down block is missing %q", want)
		}
	}
}

// TestTooltipsAndFlashChrome guards review 02 task R02-18: the sheet carries
// the Primer tooltip pattern (the bubble is the element's own aria-label, so
// hover viewers and screen readers get the same text with no script), and the
// flash dismiss button stays hidden until the js-enhanced flag proves the
// click wiring ran.
func TestTooltipsAndFlashChrome(t *testing.T) {
	src, err := os.ReadFile("src/css/components.css")
	if err != nil {
		t.Fatalf("read components.css: %v", err)
	}
	bubble := ruleBody(t, string(src), ".tooltipped::after")
	for _, want := range []string{"content: attr(aria-label)", "var(--bgColor-emphasis)", "var(--fgColor-onEmphasis)"} {
		if !strings.Contains(bubble, want) {
			t.Errorf(".tooltipped::after is missing %s:\n%s", want, bubble)
		}
	}
	for _, sel := range []string{".tooltipped-n::before", ".tooltipped-s::before", ".tooltipped-e::before", ".tooltipped-w::before"} {
		ruleBody(t, string(src), sel)
	}
	if body := ruleBody(t, string(src), ".flash-close"); !strings.Contains(body, "display: none") {
		t.Errorf(".flash-close must stay hidden with scripting off:\n%s", body)
	}
	ruleBody(t, string(src), "html[data-js-enhanced] .flash-close")
}

// cssRule is one selector { body } pair lifted out of a sheet.
type cssRule struct {
	selector string
	body     string
}

// cssRules splits a flat sheet into its rules. Comments are stripped first so
// a token name in prose does not trip an assertion. The parser does not
// understand at-rules: a media query yields garbage selectors for itself and
// the rule after it, so components.css keeps its single responsive block at
// the end of the sheet where nothing follows.
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

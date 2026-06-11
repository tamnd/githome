package assets

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// themeBlocks lifts the explicit per-theme blocks out of themes.gen.css:
// theme name to its token->value map. The explicit blocks are flat (the auto
// duplicates sit inside media queries and repeat the same values), so a
// simple regex is enough.
func themeBlocks(t *testing.T) map[string]map[string]string {
	t.Helper()
	src, err := os.ReadFile("src/css/themes.gen.css")
	if err != nil {
		t.Fatalf("read themes.gen.css: %v", err)
	}
	blockRe := regexp.MustCompile(`\[data-color-mode="(?:light|dark)"\]\[data-(?:light|dark)-theme="([a-z_]+)"\]\s*\{([^}]*)\}`)
	tokenRe := regexp.MustCompile(`--([a-zA-Z0-9-]+):\s*([^;]+);`)
	out := map[string]map[string]string{}
	for _, m := range blockRe.FindAllStringSubmatch(string(src), -1) {
		tokens := map[string]string{}
		for _, tm := range tokenRe.FindAllStringSubmatch(m[2], -1) {
			tokens[tm[1]] = strings.TrimSpace(tm[2])
		}
		out[m[1]] = tokens
	}
	return out
}

// TestThemeCatalogComplete guards review 02 tasks R02-01 and R02-03: every
// theme must define the same functional token set, and the set must cover
// the catalog groups component CSS is allowed to lean on (controls, role
// ramps, shadows, overlays, diff colors, the full prettylights palette).
func TestThemeCatalogComplete(t *testing.T) {
	themes := themeBlocks(t)
	want := []string{
		"light", "light_high_contrast", "light_colorblind", "light_tritanopia",
		"dark", "dark_dimmed", "dark_high_contrast", "dark_colorblind", "dark_tritanopia",
	}
	if len(themes) != len(want) {
		t.Fatalf("themes.gen.css defines %d themes, want %d", len(themes), len(want))
	}
	ref := themes["light"]
	for _, name := range want {
		tokens, ok := themes[name]
		if !ok {
			t.Fatalf("theme %s missing from themes.gen.css", name)
		}
		for k := range ref {
			if _, ok := tokens[k]; !ok {
				t.Errorf("theme %s is missing token --%s that light defines", name, k)
			}
		}
		for k := range tokens {
			if _, ok := ref[k]; !ok {
				t.Errorf("theme %s defines --%s that light does not; the catalog must stay uniform", name, k)
			}
		}
	}

	required := []string{
		"fgColor-subtle", "fgColor-disabled", "fgColor-link",
		"fgColor-open", "fgColor-closed", "fgColor-draft", "fgColor-severe", "fgColor-sponsors",
		"bgColor-disabled", "bgColor-transparent",
		"bgColor-accent-emphasis", "bgColor-attention-emphasis",
		"bgColor-open-emphasis", "bgColor-closed-emphasis", "bgColor-done-muted",
		"borderColor-emphasis", "borderColor-disabled", "borderColor-translucent",
		"control-bgColor-rest", "control-bgColor-hover", "control-bgColor-active",
		"button-primary-bgColor-rest", "button-primary-bgColor-hover",
		"underlineNav-borderColor-active", "underlineNav-borderColor-hover",
		"overlay-bgColor", "overlay-backdrop-bgColor",
		"focus-outlineColor", "selection-bgColor",
		"shadow-resting-small", "shadow-resting-medium",
		"shadow-floating-small", "shadow-floating-large", "shadow-inset",
		"diffBlob-additionLine-bgColor", "diffBlob-additionWord-bgColor",
		"diffBlob-deletionLine-bgColor", "diffBlob-deletionWord-bgColor",
		"diffBlob-hunkLine-bgColor",
		"color-prettylights-syntax-variable",
		"color-prettylights-syntax-string-regexp",
		"color-prettylights-syntax-markup-heading",
		"color-prettylights-syntax-markup-deleted-bg",
		"color-prettylights-syntax-markup-inserted-bg",
		"color-prettylights-syntax-brackethighlighter-unmatched",
		"color-prettylights-syntax-invalid-illegal-text",
		"color-prettylights-syntax-carriage-return-text",
	}
	for _, name := range required {
		if _, ok := ref[name]; !ok {
			t.Errorf("catalog is missing required token --%s", name)
		}
	}
}

// TestEveryVarReferenceIsDefined guards review 02 task R02-06: a component
// sheet must never read a custom property nothing defines, because the
// var() fallback (or worse, nothing) silently takes over and drifts from
// the theme. Declarations from every sheet count, including component-local
// properties.
func TestEveryVarReferenceIsDefined(t *testing.T) {
	sheets, err := filepath.Glob("src/css/*.css")
	if err != nil || len(sheets) == 0 {
		t.Fatalf("glob src/css: %v (%d files)", err, len(sheets))
	}
	declRe := regexp.MustCompile(`--([a-zA-Z0-9-]+)\s*:`)
	refRe := regexp.MustCompile(`var\(\s*--([a-zA-Z0-9-]+)`)
	defined := map[string]bool{}
	type ref struct{ file, name string }
	var refs []ref
	for _, sheet := range sheets {
		src, err := os.ReadFile(sheet)
		if err != nil {
			t.Fatalf("read %s: %v", sheet, err)
		}
		for _, m := range declRe.FindAllStringSubmatch(string(src), -1) {
			defined[m[1]] = true
		}
		for _, m := range refRe.FindAllStringSubmatch(string(src), -1) {
			refs = append(refs, ref{file: filepath.Base(sheet), name: m[1]})
		}
	}
	for _, r := range refs {
		if !defined[r.name] {
			t.Errorf("%s reads var(--%s) but no sheet defines it", r.file, r.name)
		}
	}
}

package main

import (
	"fmt"
	"maps"
	"sort"
	"strings"
)

// This file is the single source of truth for the nine GitHub color themes. Each
// theme is a flat map of functional token -> resolved color. The build generates
// src/css/themes.gen.css from these maps so the CSS source stays compact and a
// palette edit is a Go change with a regenerated, diffed output (assets-check).
// See implementation/04 sections 4 and 9.
//
// The token set here is the shell-and-chrome subset F0 needs. Later milestones
// extend each palette with the additional functional tokens their components
// reference; a component may not name a token that is absent from a theme, which
// the token-coverage check in the visual oracle enforces.

// mode is the light/dark base a theme resolves against. It drives the
// color-scheme declaration and which of data-light-theme / data-dark-theme picks
// the theme up.
type mode string

const (
	modeLight mode = "light"
	modeDark  mode = "dark"
)

// theme is one named palette.
type theme struct {
	id     string
	mode   mode
	tokens map[string]string
}

// themeOrder fixes the emission order so the generated CSS is byte-stable.
var themeOrder = []string{
	"light",
	"light_high_contrast",
	"light_colorblind",
	"light_tritanopia",
	"dark",
	"dark_dimmed",
	"dark_high_contrast",
	"dark_colorblind",
	"dark_tritanopia",
}

// functionalTokens lists every token a theme must define, in emission order. The
// generator fails if a theme omits one, so a half-filled palette cannot ship.
var functionalTokens = []string{
	"fgColor-default",
	"fgColor-muted",
	"fgColor-onEmphasis",
	"fgColor-accent",
	"bgColor-default",
	"bgColor-subtle",
	"bgColor-muted",
	"bgColor-inset",
	"bgColor-emphasis",
	"bgColor-accent-muted",
	"bgColor-success-muted",
	"bgColor-success-emphasis",
	"bgColor-danger-muted",
	// done is the purple "completed" accent: a closed-as-completed issue badge,
	// and later the merged-pull-request badge. not-planned reuses the neutral
	// emphasis, so it needs no token of its own. See implementation/08 section 4.
	"bgColor-done-emphasis",
	"borderColor-default",
	"borderColor-muted",
	"borderColor-accent-muted",
	"borderColor-success-muted",
	"borderColor-danger-muted",
	"borderColor-onEmphasis-muted",
	// The prettylights syntax tokens back the code highlighter (highlight.css maps
	// the chroma-emitted pl-* classes onto these). F2 adds the focused subset the
	// chroma backend emits: comment, keyword, string, constant, entity, entity tag,
	// and the variable/import color. See implementation/10 section 6.2.
	"color-prettylights-syntax-comment",
	"color-prettylights-syntax-keyword",
	"color-prettylights-syntax-string",
	"color-prettylights-syntax-constant",
	"color-prettylights-syntax-entity",
	"color-prettylights-syntax-entity-tag",
	"color-prettylights-syntax-storage-modifier-import",
}

// base light and dark palettes; the variant themes start from these and override
// the few tokens that distinguish them, which keeps the divergences readable.
var lightBase = map[string]string{
	"fgColor-default":              "#1f2328",
	"fgColor-muted":                "#59636e",
	"fgColor-onEmphasis":           "#ffffff",
	"fgColor-accent":               "#0969da",
	"bgColor-default":              "#ffffff",
	"bgColor-subtle":               "#f6f8fa",
	"bgColor-muted":                "#f6f8fa",
	"bgColor-inset":                "#f6f8fa",
	"bgColor-emphasis":             "#24292f",
	"bgColor-accent-muted":         "#ddf4ff",
	"bgColor-success-muted":        "#dafbe1",
	"bgColor-success-emphasis":     "#1f883d",
	"bgColor-danger-muted":         "#ffebe9",
	"bgColor-done-emphasis":        "#8250df",
	"borderColor-default":          "#d1d9e0",
	"borderColor-muted":            "#d1d9e0b3",
	"borderColor-accent-muted":     "#54aeff66",
	"borderColor-success-muted":    "#4ac26b66",
	"borderColor-danger-muted":     "#ff818266",
	"borderColor-onEmphasis-muted": "#ffffff4d",

	"color-prettylights-syntax-comment":                 "#59636e",
	"color-prettylights-syntax-keyword":                 "#cf222e",
	"color-prettylights-syntax-string":                  "#0a3069",
	"color-prettylights-syntax-constant":                "#0550ae",
	"color-prettylights-syntax-entity":                  "#8250df",
	"color-prettylights-syntax-entity-tag":              "#116329",
	"color-prettylights-syntax-storage-modifier-import": "#1f2328",
}

var darkBase = map[string]string{
	"fgColor-default":              "#e6edf3",
	"fgColor-muted":                "#9198a1",
	"fgColor-onEmphasis":           "#ffffff",
	"fgColor-accent":               "#4493f8",
	"bgColor-default":              "#0d1117",
	"bgColor-subtle":               "#151b23",
	"bgColor-muted":                "#151b23",
	"bgColor-inset":                "#010409",
	"bgColor-emphasis":             "#151b23",
	"bgColor-accent-muted":         "#388bfd1a",
	"bgColor-success-muted":        "#2ea04326",
	"bgColor-success-emphasis":     "#238636",
	"bgColor-danger-muted":         "#f851491a",
	"bgColor-done-emphasis":        "#8957e5",
	"borderColor-default":          "#3d444d",
	"borderColor-muted":            "#3d444db3",
	"borderColor-accent-muted":     "#4493f866",
	"borderColor-success-muted":    "#2ea04366",
	"borderColor-danger-muted":     "#f8514966",
	"borderColor-onEmphasis-muted": "#ffffff33",

	"color-prettylights-syntax-comment":                 "#9198a1",
	"color-prettylights-syntax-keyword":                 "#ff7b72",
	"color-prettylights-syntax-string":                  "#a5d6ff",
	"color-prettylights-syntax-constant":                "#79c0ff",
	"color-prettylights-syntax-entity":                  "#d2a8ff",
	"color-prettylights-syntax-entity-tag":              "#7ee787",
	"color-prettylights-syntax-storage-modifier-import": "#f0f6fc",
}

// derive copies base and applies overrides, returning a complete token map.
func derive(base map[string]string, overrides map[string]string) map[string]string {
	out := make(map[string]string, len(base))
	maps.Copy(out, base)
	maps.Copy(out, overrides)
	return out
}

// themes returns the nine palettes keyed by id.
func themes() map[string]theme {
	m := map[string]theme{
		"light": {id: "light", mode: modeLight, tokens: lightBase},
		"light_high_contrast": {id: "light_high_contrast", mode: modeLight, tokens: derive(lightBase, map[string]string{
			"fgColor-default":          "#0e1116",
			"fgColor-muted":            "#0e1116",
			"fgColor-accent":           "#0349b4",
			"bgColor-subtle":           "#ffffff",
			"bgColor-muted":            "#ffffff",
			"bgColor-emphasis":         "#0e1116",
			"bgColor-success-emphasis": "#055d20",
			"borderColor-default":      "#20252c",
			"borderColor-muted":        "#88929d",
		})},
		"light_colorblind": {id: "light_colorblind", mode: modeLight, tokens: derive(lightBase, map[string]string{
			"bgColor-success-muted":     "#ddf4ff",
			"bgColor-success-emphasis":  "#0969da",
			"borderColor-success-muted": "#54aeff66",
		})},
		"light_tritanopia": {id: "light_tritanopia", mode: modeLight, tokens: derive(lightBase, map[string]string{
			"bgColor-success-muted":     "#ddf4ff",
			"bgColor-success-emphasis":  "#1b7c83",
			"borderColor-success-muted": "#54aeff66",
			"bgColor-danger-muted":      "#ffece5",
		})},
		"dark": {id: "dark", mode: modeDark, tokens: darkBase},
		"dark_dimmed": {id: "dark_dimmed", mode: modeDark, tokens: derive(darkBase, map[string]string{
			"fgColor-default":     "#d1d7e0",
			"fgColor-muted":       "#9ea7b3",
			"fgColor-accent":      "#478be6",
			"bgColor-default":     "#212830",
			"bgColor-subtle":      "#2a313c",
			"bgColor-muted":       "#2a313c",
			"bgColor-inset":       "#1c2128",
			"bgColor-emphasis":    "#2a313c",
			"borderColor-default": "#444c56",
			"borderColor-muted":   "#444c56b3",
		})},
		"dark_high_contrast": {id: "dark_high_contrast", mode: modeDark, tokens: derive(darkBase, map[string]string{
			"fgColor-default":          "#f0f3f6",
			"fgColor-muted":            "#f0f3f6",
			"fgColor-accent":           "#71b7ff",
			"bgColor-default":          "#0a0c10",
			"bgColor-subtle":           "#0a0c10",
			"bgColor-muted":            "#0a0c10",
			"bgColor-emphasis":         "#f0f3f6",
			"bgColor-success-emphasis": "#26cd4d",
			"borderColor-default":      "#7a828e",
			"borderColor-muted":        "#7a828e",
		})},
		"dark_colorblind": {id: "dark_colorblind", mode: modeDark, tokens: derive(darkBase, map[string]string{
			"bgColor-success-muted":     "#388bfd1a",
			"bgColor-success-emphasis":  "#1f6feb",
			"borderColor-success-muted": "#4493f866",
		})},
		"dark_tritanopia": {id: "dark_tritanopia", mode: modeDark, tokens: derive(darkBase, map[string]string{
			"bgColor-success-muted":     "#388bfd1a",
			"bgColor-success-emphasis":  "#1b7c83",
			"borderColor-success-muted": "#4493f866",
		})},
	}
	return m
}

// generateThemesCSS emits the themes.gen.css body: for every theme, an explicit
// selector block and an auto-mode block guarded by the prefers-color-scheme media
// so an "auto" reader follows the OS with no JavaScript and no flash. A theme that
// omits a required token fails the build rather than emitting a partial palette.
func generateThemesCSS() (string, error) {
	all := themes()
	var b strings.Builder
	b.WriteString("/* Generated by fe/assets/build from palettes.go. Do not edit by hand. */\n")

	for _, id := range themeOrder {
		t, ok := all[id]
		if !ok {
			return "", fmt.Errorf("themeOrder names unknown theme %q", id)
		}
		if err := checkComplete(t); err != nil {
			return "", err
		}
		decls := declarations(t)
		scheme := string(t.mode)

		// Explicit selection: data-color-mode matches the theme's mode and the
		// matching data-*-theme names this theme.
		attr := "data-light-theme"
		if t.mode == modeDark {
			attr = "data-dark-theme"
		}
		fmt.Fprintf(&b, "[data-color-mode=\"%s\"][%s=\"%s\"] {\n  color-scheme: %s;\n%s}\n",
			scheme, attr, t.id, scheme, decls)

		// Auto selection: data-color-mode=auto and the OS preference matches the
		// theme's mode, so the chosen light theme applies under a light OS and the
		// chosen dark theme under a dark OS.
		fmt.Fprintf(&b, "@media (prefers-color-scheme: %s) {\n  [data-color-mode=\"auto\"][%s=\"%s\"] {\n    color-scheme: %s;\n%s  }\n}\n",
			scheme, attr, t.id, scheme, indent(decls, "  "))
	}
	return b.String(), nil
}

func checkComplete(t theme) error {
	for _, tok := range functionalTokens {
		if _, ok := t.tokens[tok]; !ok {
			return fmt.Errorf("theme %q is missing functional token %q", t.id, tok)
		}
	}
	// Guard against a typo adding a token no theme should carry.
	known := make(map[string]bool, len(functionalTokens))
	for _, tok := range functionalTokens {
		known[tok] = true
	}
	keys := make([]string, 0, len(t.tokens))
	for k := range t.tokens {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !known[k] {
			return fmt.Errorf("theme %q defines unknown token %q", t.id, k)
		}
	}
	return nil
}

func declarations(t theme) string {
	var b strings.Builder
	for _, tok := range functionalTokens {
		fmt.Fprintf(&b, "  --%s: %s;\n", tok, t.tokens[tok])
	}
	return b.String()
}

func indent(s, pad string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, ln := range lines {
		if ln != "" {
			lines[i] = pad + ln
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

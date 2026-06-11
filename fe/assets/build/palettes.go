// The theme generator. The nine Primer-shaped themes share one functional
// token catalog; each theme is the mode base palette plus a small override
// map. genThemes renders the catalog into src/css/themes.gen.css (the per
// theme functional values plus the mode-neutral base color scale) and
// src/css/aliases.gen.css (the legacy --color-* names mapped onto their
// canonical tokens), then the normal esbuild bundling picks both up through
// app.css. Edit the palettes here and rerun the build; never edit the
// generated sheets by hand. See implementation/04 and the design-system spec
// sections 2.3 to 2.6.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// tokenOrder fixes the emit order so regenerating the sheet produces stable
// diffs. Grouped the way the catalog reads: foregrounds, backgrounds, borders,
// controls, overlays and shadows, diff colors, then the prettylights syntax
// palette.
var tokenOrder = []string{
	// Foregrounds.
	"fgColor-default",
	"fgColor-muted",
	"fgColor-subtle",
	"fgColor-disabled",
	"fgColor-link",
	"fgColor-onEmphasis",
	"fgColor-accent",
	"fgColor-success",
	"fgColor-attention",
	"fgColor-severe",
	"fgColor-danger",
	"fgColor-open",
	"fgColor-closed",
	"fgColor-done",
	"fgColor-draft",
	"fgColor-sponsors",
	// Backgrounds.
	"bgColor-default",
	"bgColor-muted",
	"bgColor-subtle",
	"bgColor-inset",
	"bgColor-emphasis",
	"bgColor-disabled",
	"bgColor-transparent",
	"bgColor-accent-muted",
	"bgColor-accent-emphasis",
	"bgColor-success-muted",
	"bgColor-success-emphasis",
	"bgColor-attention-muted",
	"bgColor-attention-emphasis",
	"bgColor-severe-muted",
	"bgColor-severe-emphasis",
	"bgColor-danger-muted",
	"bgColor-danger-emphasis",
	"bgColor-open-muted",
	"bgColor-open-emphasis",
	"bgColor-closed-muted",
	"bgColor-closed-emphasis",
	"bgColor-done-muted",
	"bgColor-done-emphasis",
	"bgColor-sponsors-muted",
	"bgColor-sponsors-emphasis",
	"bgColor-neutral-muted",
	"bgColor-neutral-emphasis",
	// Borders.
	"borderColor-default",
	"borderColor-muted",
	"borderColor-emphasis",
	"borderColor-disabled",
	"borderColor-translucent",
	"borderColor-accent-muted",
	"borderColor-accent-emphasis",
	"borderColor-success-muted",
	"borderColor-success-emphasis",
	"borderColor-attention-muted",
	"borderColor-attention-emphasis",
	"borderColor-severe-muted",
	"borderColor-severe-emphasis",
	"borderColor-danger-muted",
	"borderColor-danger-emphasis",
	"borderColor-done-muted",
	"borderColor-done-emphasis",
	"borderColor-sponsors-muted",
	"borderColor-sponsors-emphasis",
	"borderColor-neutral-muted",
	"borderColor-onEmphasis-muted",
	// Controls and component accents.
	"control-bgColor-rest",
	"control-bgColor-hover",
	"control-bgColor-active",
	"control-transparent-bgColor-hover",
	"control-transparent-bgColor-active",
	"button-primary-fgColor-rest",
	"button-primary-bgColor-rest",
	"button-primary-bgColor-hover",
	"button-primary-bgColor-active",
	"button-primary-borderColor-rest",
	"underlineNav-borderColor-active",
	"underlineNav-borderColor-hover",
	"label-fgColor",
	"label-bgColor",
	"label-borderColor",
	// Overlays, shadows, focus, selection.
	"overlay-bgColor",
	"overlay-backdrop-bgColor",
	"focus-outlineColor",
	"selection-bgColor",
	"shadow-resting-small",
	"shadow-resting-medium",
	"shadow-floating-small",
	"shadow-floating-medium",
	"shadow-floating-large",
	"shadow-inset",
	// Diff colors (doc 10).
	"diffBlob-additionLine-bgColor",
	"diffBlob-additionWord-bgColor",
	"diffBlob-additionNum-bgColor",
	"diffBlob-deletionLine-bgColor",
	"diffBlob-deletionWord-bgColor",
	"diffBlob-deletionNum-bgColor",
	"diffBlob-hunkLine-bgColor",
	// Prettylights syntax palette (doc 10 highlighting).
	"color-prettylights-syntax-comment",
	"color-prettylights-syntax-constant",
	"color-prettylights-syntax-constant-other-reference-link",
	"color-prettylights-syntax-entity",
	"color-prettylights-syntax-entity-tag",
	"color-prettylights-syntax-keyword",
	"color-prettylights-syntax-storage-modifier-import",
	"color-prettylights-syntax-string",
	"color-prettylights-syntax-string-regexp",
	"color-prettylights-syntax-variable",
	"color-prettylights-syntax-brackethighlighter-angle",
	"color-prettylights-syntax-brackethighlighter-unmatched",
	"color-prettylights-syntax-carriage-return-bg",
	"color-prettylights-syntax-carriage-return-text",
	"color-prettylights-syntax-invalid-illegal-bg",
	"color-prettylights-syntax-invalid-illegal-text",
	"color-prettylights-syntax-markup-bold",
	"color-prettylights-syntax-markup-changed-bg",
	"color-prettylights-syntax-markup-changed-text",
	"color-prettylights-syntax-markup-deleted-bg",
	"color-prettylights-syntax-markup-deleted-text",
	"color-prettylights-syntax-markup-heading",
	"color-prettylights-syntax-markup-ignored-bg",
	"color-prettylights-syntax-markup-ignored-text",
	"color-prettylights-syntax-markup-inserted-bg",
	"color-prettylights-syntax-markup-inserted-text",
	"color-prettylights-syntax-markup-italic",
	"color-prettylights-syntax-markup-list",
	"color-prettylights-syntax-meta-diff-range",
	"color-prettylights-syntax-sublimelinter-gutter-mark",
}

// lightPrettylights is the light-mode syntax palette every light theme shares.
var lightPrettylights = map[string]string{
	"color-prettylights-syntax-comment":                       "#59636e",
	"color-prettylights-syntax-constant":                      "#0550ae",
	"color-prettylights-syntax-constant-other-reference-link": "#0a3069",
	"color-prettylights-syntax-entity":                        "#8250df",
	"color-prettylights-syntax-entity-tag":                    "#116329",
	"color-prettylights-syntax-keyword":                       "#cf222e",
	"color-prettylights-syntax-storage-modifier-import":       "#1f2328",
	"color-prettylights-syntax-string":                        "#0a3069",
	"color-prettylights-syntax-string-regexp":                 "#116329",
	"color-prettylights-syntax-variable":                      "#953800",
	"color-prettylights-syntax-brackethighlighter-angle":      "#57606a",
	"color-prettylights-syntax-brackethighlighter-unmatched":  "#82071e",
	"color-prettylights-syntax-carriage-return-bg":            "#cf222e",
	"color-prettylights-syntax-carriage-return-text":          "#f6f8fa",
	"color-prettylights-syntax-invalid-illegal-bg":            "#82071e",
	"color-prettylights-syntax-invalid-illegal-text":          "#f6f8fa",
	"color-prettylights-syntax-markup-bold":                   "#1f2328",
	"color-prettylights-syntax-markup-changed-bg":             "#ffd8b5",
	"color-prettylights-syntax-markup-changed-text":           "#953800",
	"color-prettylights-syntax-markup-deleted-bg":             "#ffebe9",
	"color-prettylights-syntax-markup-deleted-text":           "#82071e",
	"color-prettylights-syntax-markup-heading":                "#0550ae",
	"color-prettylights-syntax-markup-ignored-bg":             "#0550ae",
	"color-prettylights-syntax-markup-ignored-text":           "#eaeef2",
	"color-prettylights-syntax-markup-inserted-bg":            "#dafbe1",
	"color-prettylights-syntax-markup-inserted-text":          "#116329",
	"color-prettylights-syntax-markup-italic":                 "#1f2328",
	"color-prettylights-syntax-markup-list":                   "#3b2300",
	"color-prettylights-syntax-meta-diff-range":               "#8250df",
	"color-prettylights-syntax-sublimelinter-gutter-mark":     "#8c959f",
}

// darkPrettylights is the dark-mode syntax palette every dark theme shares.
var darkPrettylights = map[string]string{
	"color-prettylights-syntax-comment":                       "#9198a1",
	"color-prettylights-syntax-constant":                      "#79c0ff",
	"color-prettylights-syntax-constant-other-reference-link": "#a5d6ff",
	"color-prettylights-syntax-entity":                        "#d2a8ff",
	"color-prettylights-syntax-entity-tag":                    "#7ee787",
	"color-prettylights-syntax-keyword":                       "#ff7b72",
	"color-prettylights-syntax-storage-modifier-import":       "#f0f6fc",
	"color-prettylights-syntax-string":                        "#a5d6ff",
	"color-prettylights-syntax-string-regexp":                 "#7ee787",
	"color-prettylights-syntax-variable":                      "#ffa657",
	"color-prettylights-syntax-brackethighlighter-angle":      "#8b949e",
	"color-prettylights-syntax-brackethighlighter-unmatched":  "#f85149",
	"color-prettylights-syntax-carriage-return-bg":            "#b62324",
	"color-prettylights-syntax-carriage-return-text":          "#f0f6fc",
	"color-prettylights-syntax-invalid-illegal-bg":            "#8e1519",
	"color-prettylights-syntax-invalid-illegal-text":          "#f0f6fc",
	"color-prettylights-syntax-markup-bold":                   "#e6edf3",
	"color-prettylights-syntax-markup-changed-bg":             "#5a1e02",
	"color-prettylights-syntax-markup-changed-text":           "#ffdfb6",
	"color-prettylights-syntax-markup-deleted-bg":             "#67060c",
	"color-prettylights-syntax-markup-deleted-text":           "#ffdcd7",
	"color-prettylights-syntax-markup-heading":                "#1f6feb",
	"color-prettylights-syntax-markup-ignored-bg":             "#1158c7",
	"color-prettylights-syntax-markup-ignored-text":           "#c9d1d9",
	"color-prettylights-syntax-markup-inserted-bg":            "#033a16",
	"color-prettylights-syntax-markup-inserted-text":          "#aff5b4",
	"color-prettylights-syntax-markup-italic":                 "#e6edf3",
	"color-prettylights-syntax-markup-list":                   "#f2cc60",
	"color-prettylights-syntax-meta-diff-range":               "#d2a8ff",
	"color-prettylights-syntax-sublimelinter-gutter-mark":     "#484f58",
}

// lightBase is the light theme; the other light themes override it.
var lightBase = map[string]string{
	"fgColor-default":    "#1f2328",
	"fgColor-muted":      "#59636e",
	"fgColor-subtle":     "#818b98",
	"fgColor-disabled":   "#818b98",
	"fgColor-link":       "#0969da",
	"fgColor-onEmphasis": "#ffffff",
	"fgColor-accent":     "#0969da",
	"fgColor-success":    "#1a7f37",
	"fgColor-attention":  "#9a6700",
	"fgColor-severe":     "#bc4c00",
	"fgColor-danger":     "#cf222e",
	"fgColor-open":       "#1a7f37",
	"fgColor-closed":     "#cf222e",
	"fgColor-done":       "#8250df",
	"fgColor-draft":      "#59636e",
	"fgColor-sponsors":   "#bf3989",

	"bgColor-default":            "#ffffff",
	"bgColor-muted":              "#f6f8fa",
	"bgColor-subtle":             "#f6f8fa",
	"bgColor-inset":              "#f6f8fa",
	"bgColor-emphasis":           "#24292f",
	"bgColor-disabled":           "#eff2f5",
	"bgColor-transparent":        "transparent",
	"bgColor-accent-muted":       "#ddf4ff",
	"bgColor-accent-emphasis":    "#0969da",
	"bgColor-success-muted":      "#dafbe1",
	"bgColor-success-emphasis":   "#1f883d",
	"bgColor-attention-muted":    "#fff8c5",
	"bgColor-attention-emphasis": "#bf8700",
	"bgColor-severe-muted":       "#fff1e5",
	"bgColor-severe-emphasis":    "#bc4c00",
	"bgColor-danger-muted":       "#ffebe9",
	"bgColor-danger-emphasis":    "#cf222e",
	"bgColor-open-muted":         "#dafbe1",
	"bgColor-open-emphasis":      "#1f883d",
	"bgColor-closed-muted":       "#ffebe9",
	"bgColor-closed-emphasis":    "#cf222e",
	"bgColor-done-muted":         "#fbefff",
	"bgColor-done-emphasis":      "#8250df",
	"bgColor-sponsors-muted":     "#ffeff7",
	"bgColor-sponsors-emphasis":  "#bf3989",
	"bgColor-neutral-muted":      "#afb8c133",
	"bgColor-neutral-emphasis":   "#6e7781",

	"borderColor-default":            "#d1d9e0",
	"borderColor-muted":              "#d1d9e0b3",
	"borderColor-emphasis":           "#818b98",
	"borderColor-disabled":           "#1f23281a",
	"borderColor-translucent":        "#1f232826",
	"borderColor-accent-muted":       "#54aeff66",
	"borderColor-accent-emphasis":    "#0969da",
	"borderColor-success-muted":      "#4ac26b66",
	"borderColor-success-emphasis":   "#1a7f37",
	"borderColor-attention-muted":    "#d4a72c66",
	"borderColor-attention-emphasis": "#bf8700",
	"borderColor-severe-muted":       "#fb8f4466",
	"borderColor-severe-emphasis":    "#bc4c00",
	"borderColor-danger-muted":       "#ff818266",
	"borderColor-danger-emphasis":    "#cf222e",
	"borderColor-done-muted":         "#c297ff66",
	"borderColor-done-emphasis":      "#8250df",
	"borderColor-sponsors-muted":     "#ff80c866",
	"borderColor-sponsors-emphasis":  "#bf3989",
	"borderColor-neutral-muted":      "#afb8c133",
	"borderColor-onEmphasis-muted":   "#ffffff4d",

	"control-bgColor-rest":               "#f6f8fa",
	"control-bgColor-hover":              "#eef1f4",
	"control-bgColor-active":             "#e7ebef",
	"control-transparent-bgColor-hover":  "#d0d7de52",
	"control-transparent-bgColor-active": "#d0d7de7a",
	"button-primary-fgColor-rest":        "#ffffff",
	"button-primary-bgColor-rest":        "#1f883d",
	"button-primary-bgColor-hover":       "#1c8139",
	"button-primary-bgColor-active":      "#197935",
	"button-primary-borderColor-rest":    "#1f232826",
	"underlineNav-borderColor-active":    "#fd8c73",
	"underlineNav-borderColor-hover":     "#afb8c133",

	// The label chip recipe. A chip sets --label-r/g/b inline; light themes
	// paint the color solid and flip the text black or white on the
	// perceived-lightness switch the component sheet computes, dark themes
	// tint the canvas with the color and lighten it for the text, the
	// github.com treatment. The var() references resolve on the chip.
	"label-fgColor":     "hsl(0deg 0% calc(var(--label-lightness-switch) * 100%))",
	"label-bgColor":     "rgb(var(--label-r) var(--label-g) var(--label-b))",
	"label-borderColor": "transparent",

	"overlay-bgColor":          "#ffffff",
	"overlay-backdrop-bgColor": "#25292e66",
	"focus-outlineColor":       "#0969da",
	"selection-bgColor":        "#54aeff66",
	"shadow-resting-small":     "0 1px 1px #1f23280a",
	"shadow-resting-medium":    "0 3px 6px #424a531f",
	"shadow-floating-small":    "0 0 0 1px #d1d9e080, 0 8px 24px #8c959f33",
	"shadow-floating-medium":   "0 0 0 1px #d1d9e0, 0 16px 32px #25292e1f",
	"shadow-floating-large":    "0 0 0 1px #d1d9e0, 0 24px 48px #25292e1f",
	"shadow-inset":             "inset 0 1px 0 #1f23280a",

	"diffBlob-additionLine-bgColor": "#dafbe1",
	"diffBlob-additionWord-bgColor": "#aceebb",
	"diffBlob-additionNum-bgColor":  "#aceebb",
	"diffBlob-deletionLine-bgColor": "#ffebe9",
	"diffBlob-deletionWord-bgColor": "#ffcecb",
	"diffBlob-deletionNum-bgColor":  "#ffcecb",
	"diffBlob-hunkLine-bgColor":     "#ddf4ff",
}

// darkBase is the dark theme; the other dark themes override it.
var darkBase = map[string]string{
	"fgColor-default":    "#e6edf3",
	"fgColor-muted":      "#9198a1",
	"fgColor-subtle":     "#6e7681",
	"fgColor-disabled":   "#656c76",
	"fgColor-link":       "#4493f8",
	"fgColor-onEmphasis": "#ffffff",
	"fgColor-accent":     "#4493f8",
	"fgColor-success":    "#3fb950",
	"fgColor-attention":  "#d29922",
	"fgColor-severe":     "#db6d28",
	"fgColor-danger":     "#f85149",
	"fgColor-open":       "#3fb950",
	"fgColor-closed":     "#f85149",
	"fgColor-done":       "#a371f7",
	"fgColor-draft":      "#9198a1",
	"fgColor-sponsors":   "#db61a2",

	"bgColor-default":            "#0d1117",
	"bgColor-muted":              "#151b23",
	"bgColor-subtle":             "#151b23",
	"bgColor-inset":              "#010409",
	"bgColor-emphasis":           "#3d444d",
	"bgColor-disabled":           "#212830",
	"bgColor-transparent":        "transparent",
	"bgColor-accent-muted":       "#388bfd1a",
	"bgColor-accent-emphasis":    "#1f6feb",
	"bgColor-success-muted":      "#2ea04326",
	"bgColor-success-emphasis":   "#238636",
	"bgColor-attention-muted":    "#3d2e00",
	"bgColor-attention-emphasis": "#9e6a03",
	"bgColor-severe-muted":       "#db6d281a",
	"bgColor-severe-emphasis":    "#bd561d",
	"bgColor-danger-muted":       "#f851491a",
	"bgColor-danger-emphasis":    "#da3633",
	"bgColor-open-muted":         "#2ea04326",
	"bgColor-open-emphasis":      "#238636",
	"bgColor-closed-muted":       "#f851491a",
	"bgColor-closed-emphasis":    "#da3633",
	"bgColor-done-muted":         "#ab7df81f",
	"bgColor-done-emphasis":      "#8957e5",
	"bgColor-sponsors-muted":     "#db61a21a",
	"bgColor-sponsors-emphasis":  "#bf4b8a",
	"bgColor-neutral-muted":      "#8b949e1a",
	"bgColor-neutral-emphasis":   "#6e7681",

	"borderColor-default":            "#3d444d",
	"borderColor-muted":              "#3d444db3",
	"borderColor-emphasis":           "#656c76",
	"borderColor-disabled":           "#f0f6fc1a",
	"borderColor-translucent":        "#f0f6fc1a",
	"borderColor-accent-muted":       "#4493f866",
	"borderColor-accent-emphasis":    "#1f6feb",
	"borderColor-success-muted":      "#2ea04366",
	"borderColor-success-emphasis":   "#238636",
	"borderColor-attention-muted":    "#9e6a0066",
	"borderColor-attention-emphasis": "#9e6a03",
	"borderColor-severe-muted":       "#db6d2866",
	"borderColor-severe-emphasis":    "#bd561d",
	"borderColor-danger-muted":       "#f8514966",
	"borderColor-danger-emphasis":    "#da3633",
	"borderColor-done-muted":         "#ab7df866",
	"borderColor-done-emphasis":      "#8957e5",
	"borderColor-sponsors-muted":     "#db61a266",
	"borderColor-sponsors-emphasis":  "#bf4b8a",
	"borderColor-neutral-muted":      "#8b949e33",
	"borderColor-onEmphasis-muted":   "#ffffff33",

	"control-bgColor-rest":               "#212830",
	"control-bgColor-hover":              "#262c36",
	"control-bgColor-active":             "#2a313c",
	"control-transparent-bgColor-hover":  "#656c7633",
	"control-transparent-bgColor-active": "#656c764d",
	"button-primary-fgColor-rest":        "#ffffff",
	"button-primary-bgColor-rest":        "#238636",
	"button-primary-bgColor-hover":       "#29903b",
	"button-primary-bgColor-active":      "#2ea043",
	"button-primary-borderColor-rest":    "#f0f6fc1a",
	"underlineNav-borderColor-active":    "#f78166",
	"underlineNav-borderColor-hover":     "#8b949e33",

	"label-fgColor":     "color-mix(in oklab, rgb(var(--label-r) var(--label-g) var(--label-b)) 60%, white)",
	"label-bgColor":     "color-mix(in srgb, rgb(var(--label-r) var(--label-g) var(--label-b)) 18%, var(--bgColor-default))",
	"label-borderColor": "color-mix(in srgb, rgb(var(--label-r) var(--label-g) var(--label-b)) 30%, var(--bgColor-default))",

	"overlay-bgColor":          "#151b23",
	"overlay-backdrop-bgColor": "#01040980",
	"focus-outlineColor":       "#1f6feb",
	"selection-bgColor":        "#388bfd66",
	"shadow-resting-small":     "0 1px 1px #01040966",
	"shadow-resting-medium":    "0 3px 6px #010409cc",
	"shadow-floating-small":    "0 0 0 1px #3d444d, 0 8px 24px #010409",
	"shadow-floating-medium":   "0 0 0 1px #3d444d, 0 16px 32px #010409",
	"shadow-floating-large":    "0 0 0 1px #3d444d, 0 24px 48px #010409",
	"shadow-inset":             "inset 0 1px 0 #0104093d",

	"diffBlob-additionLine-bgColor": "#2ea04326",
	"diffBlob-additionWord-bgColor": "#2ea0434d",
	"diffBlob-additionNum-bgColor":  "#3fb9504d",
	"diffBlob-deletionLine-bgColor": "#f851491a",
	"diffBlob-deletionWord-bgColor": "#f851494d",
	"diffBlob-deletionNum-bgColor":  "#f851494d",
	"diffBlob-hunkLine-bgColor":     "#388bfd1a",
}

// themeDef is one theme: the selector name, its mode, and the overrides it
// applies on top of the mode base palette.
type themeDef struct {
	name     string
	mode     string // light | dark
	override map[string]string
}

// themes lists the nine themes in the order they are emitted. The high
// contrast themes flatten the surface ramps and harden borders; the
// colorblind and tritanopia themes remap the success/open ramp away from
// green and the danger/closed ramp away from red so state never rides on a
// hue the viewer cannot separate.
var themes = []themeDef{
	{name: "light", mode: "light", override: nil},
	{name: "light_high_contrast", mode: "light", override: map[string]string{
		"fgColor-default":               "#0e1116",
		"fgColor-muted":                 "#0e1116",
		"fgColor-subtle":                "#0e1116",
		"fgColor-accent":                "#0349b4",
		"fgColor-link":                  "#0349b4",
		"bgColor-subtle":                "#ffffff",
		"bgColor-muted":                 "#ffffff",
		"bgColor-emphasis":              "#0e1116",
		"bgColor-success-emphasis":      "#055d20",
		"bgColor-open-emphasis":         "#055d20",
		"borderColor-default":           "#20252c",
		"borderColor-muted":             "#88929d",
		"borderColor-emphasis":          "#20252c",
		"button-primary-bgColor-rest":   "#055d20",
		"button-primary-bgColor-hover":  "#024b1b",
		"button-primary-bgColor-active": "#024b1b",
		"focus-outlineColor":            "#0349b4",
	}},
	{name: "light_colorblind", mode: "light", override: map[string]string{
		// Success and open ride on blue, danger and closed on orange, so the
		// open/closed pair never meets a red/green axis.
		"fgColor-success":               "#0969da",
		"fgColor-open":                  "#0969da",
		"bgColor-success-muted":         "#ddf4ff",
		"bgColor-success-emphasis":      "#0969da",
		"bgColor-open-muted":            "#ddf4ff",
		"bgColor-open-emphasis":         "#0969da",
		"borderColor-success-muted":     "#54aeff66",
		"borderColor-success-emphasis":  "#0969da",
		"fgColor-danger":                "#b35900",
		"fgColor-closed":                "#b35900",
		"bgColor-danger-muted":          "#fff5e8",
		"bgColor-danger-emphasis":       "#b35900",
		"bgColor-closed-muted":          "#fff5e8",
		"bgColor-closed-emphasis":       "#b35900",
		"borderColor-danger-muted":      "#f0883e66",
		"borderColor-danger-emphasis":   "#b35900",
		"button-primary-bgColor-rest":   "#0969da",
		"button-primary-bgColor-hover":  "#0860ca",
		"button-primary-bgColor-active": "#0757ba",
	}},
	{name: "light_tritanopia", mode: "light", override: map[string]string{
		// Tritanopia keeps the success/open swap and moves danger and closed
		// to the same orange the colorblind theme uses, but onto teal instead
		// of blue for success since blue is the confusable hue here.
		"fgColor-success":               "#1b7c83",
		"fgColor-open":                  "#1b7c83",
		"bgColor-success-muted":         "#ddf4ff",
		"bgColor-success-emphasis":      "#1b7c83",
		"bgColor-open-muted":            "#ddf4ff",
		"bgColor-open-emphasis":         "#1b7c83",
		"borderColor-success-muted":     "#54aeff66",
		"borderColor-success-emphasis":  "#1b7c83",
		"fgColor-danger":                "#b35900",
		"fgColor-closed":                "#b35900",
		"bgColor-danger-muted":          "#ffece5",
		"bgColor-danger-emphasis":       "#b35900",
		"bgColor-closed-muted":          "#ffece5",
		"bgColor-closed-emphasis":       "#b35900",
		"borderColor-danger-muted":      "#f0883e66",
		"borderColor-danger-emphasis":   "#b35900",
		"button-primary-bgColor-rest":   "#1b7c83",
		"button-primary-bgColor-hover":  "#166e74",
		"button-primary-bgColor-active": "#115b60",
	}},
	{name: "dark", mode: "dark", override: nil},
	{name: "dark_dimmed", mode: "dark", override: map[string]string{
		"fgColor-default":         "#d1d7e0",
		"fgColor-muted":           "#9ea7b3",
		"fgColor-subtle":          "#768390",
		"fgColor-accent":          "#478be6",
		"fgColor-link":            "#478be6",
		"bgColor-default":         "#212830",
		"bgColor-subtle":          "#2a313c",
		"bgColor-muted":           "#2a313c",
		"bgColor-inset":           "#1c2128",
		"bgColor-emphasis":        "#444c56",
		"bgColor-disabled":        "#2a313c",
		"bgColor-accent-emphasis": "#316dca",
		"borderColor-default":     "#444c56",
		"borderColor-muted":       "#444c56b3",
		"borderColor-emphasis":    "#636e7b",
		"control-bgColor-rest":    "#2a313c",
		"control-bgColor-hover":   "#313a45",
		"control-bgColor-active":  "#3a4350",
		"overlay-bgColor":         "#2a313c",
		"focus-outlineColor":      "#316dca",
		"selection-bgColor":       "#316dca66",
		"shadow-floating-small":   "0 0 0 1px #444c56, 0 8px 24px #1c2128",
		"shadow-floating-medium":  "0 0 0 1px #444c56, 0 16px 32px #1c2128",
		"shadow-floating-large":   "0 0 0 1px #444c56, 0 24px 48px #1c2128",
	}},
	{name: "dark_high_contrast", mode: "dark", override: map[string]string{
		"fgColor-default":                 "#f0f3f6",
		"fgColor-muted":                   "#f0f3f6",
		"fgColor-subtle":                  "#f0f3f6",
		"fgColor-accent":                  "#71b7ff",
		"fgColor-link":                    "#71b7ff",
		"bgColor-default":                 "#0a0c10",
		"bgColor-subtle":                  "#0a0c10",
		"bgColor-muted":                   "#0a0c10",
		"bgColor-emphasis":                "#f0f3f6",
		"bgColor-success-emphasis":        "#26cd4d",
		"bgColor-open-emphasis":           "#26cd4d",
		"borderColor-default":             "#7a828e",
		"borderColor-muted":               "#7a828e",
		"borderColor-emphasis":            "#7a828e",
		"control-bgColor-rest":            "#272b33",
		"control-bgColor-hover":           "#32383f",
		"control-bgColor-active":          "#3d444d",
		"button-primary-fgColor-rest":     "#0a0c10",
		"button-primary-bgColor-rest":     "#26cd4d",
		"button-primary-bgColor-hover":    "#4ae168",
		"button-primary-bgColor-active":   "#4ae168",
		"underlineNav-borderColor-active": "#ff967d",
		"focus-outlineColor":              "#71b7ff",
	}},
	{name: "dark_colorblind", mode: "dark", override: map[string]string{
		"fgColor-success":               "#4493f8",
		"fgColor-open":                  "#4493f8",
		"bgColor-success-muted":         "#388bfd1a",
		"bgColor-success-emphasis":      "#1f6feb",
		"bgColor-open-muted":            "#388bfd1a",
		"bgColor-open-emphasis":         "#1f6feb",
		"borderColor-success-muted":     "#4493f866",
		"borderColor-success-emphasis":  "#1f6feb",
		"fgColor-danger":                "#d47616",
		"fgColor-closed":                "#d47616",
		"bgColor-danger-muted":          "#d476161a",
		"bgColor-danger-emphasis":       "#d47616",
		"bgColor-closed-muted":          "#d476161a",
		"bgColor-closed-emphasis":       "#d47616",
		"borderColor-danger-muted":      "#d4761666",
		"borderColor-danger-emphasis":   "#d47616",
		"button-primary-bgColor-rest":   "#1f6feb",
		"button-primary-bgColor-hover":  "#388bfd",
		"button-primary-bgColor-active": "#4493f8",
	}},
	{name: "dark_tritanopia", mode: "dark", override: map[string]string{
		"fgColor-success":               "#39c5cf",
		"fgColor-open":                  "#39c5cf",
		"bgColor-success-muted":         "#388bfd1a",
		"bgColor-success-emphasis":      "#1b7c83",
		"bgColor-open-muted":            "#388bfd1a",
		"bgColor-open-emphasis":         "#1b7c83",
		"borderColor-success-muted":     "#4493f866",
		"borderColor-success-emphasis":  "#1b7c83",
		"fgColor-danger":                "#d47616",
		"fgColor-closed":                "#d47616",
		"bgColor-danger-muted":          "#d476161a",
		"bgColor-danger-emphasis":       "#d47616",
		"bgColor-closed-muted":          "#d476161a",
		"bgColor-closed-emphasis":       "#d47616",
		"borderColor-danger-muted":      "#d4761666",
		"borderColor-danger-emphasis":   "#d47616",
		"button-primary-bgColor-rest":   "#1b7c83",
		"button-primary-bgColor-hover":  "#208d96",
		"button-primary-bgColor-active": "#24a0aa",
	}},
}

// baseScale is the mode-neutral primitive color scale, emitted once at :root.
// Component CSS never reads these; they exist so functional and component
// tier values have named stops to map onto. Nine hues indexed 0 (lightest)
// to 9 (darkest), plus black, white, and the two alpha ramps.
var baseScaleHues = []string{"gray", "blue", "green", "red", "yellow", "orange", "purple", "pink", "coral"}

var baseScale = map[string][10]string{
	"gray":   {"#f6f8fa", "#eaeef2", "#d0d7de", "#afb8c1", "#8c959f", "#6e7781", "#57606a", "#424a53", "#32383f", "#24292f"},
	"blue":   {"#ddf4ff", "#b6e3ff", "#80ccff", "#54aeff", "#218bff", "#0969da", "#0550ae", "#033d8b", "#0a3069", "#002155"},
	"green":  {"#dafbe1", "#aceebb", "#6fdd8b", "#4ac26b", "#2da44e", "#1a7f37", "#116329", "#0a5223", "#044f1e", "#003d16"},
	"red":    {"#ffebe9", "#ffcecb", "#ffaba8", "#ff8182", "#fa4549", "#cf222e", "#a40e26", "#82071e", "#660018", "#4c0014"},
	"yellow": {"#fff8c5", "#fae17d", "#eac54f", "#d4a72c", "#bf8700", "#9a6700", "#7d4e00", "#633c01", "#4d2d00", "#3b2300"},
	"orange": {"#fff1e5", "#ffd8b5", "#ffb77c", "#fb8f44", "#e16f24", "#bc4c00", "#953800", "#762c00", "#5c1800", "#431100"},
	"purple": {"#fbefff", "#ecd8ff", "#d8b9ff", "#c297ff", "#a475f9", "#8250df", "#6639ba", "#512a97", "#3e1f79", "#2e1461"},
	"pink":   {"#ffeff7", "#ffd3eb", "#ffadda", "#ff80c8", "#e85aad", "#bf3989", "#99286e", "#7d2055", "#641d42", "#4d0336"},
	"coral":  {"#fff0eb", "#ffd6cc", "#ffb4a1", "#fd8c73", "#ec6547", "#cf222e", "#a40e26", "#82071e", "#660018", "#4c0014"},
}

// aliases maps each legacy --color-* name onto its canonical token. The alias
// layer exists so Primer-shaped CSS written against the old names still
// resolves; new rules always read the canonical names.
var aliases = [][2]string{
	{"color-fg-default", "fgColor-default"},
	{"color-fg-muted", "fgColor-muted"},
	{"color-fg-subtle", "fgColor-subtle"},
	{"color-canvas-default", "bgColor-default"},
	{"color-canvas-subtle", "bgColor-muted"},
	{"color-canvas-inset", "bgColor-inset"},
	{"color-border-default", "borderColor-default"},
	{"color-border-muted", "borderColor-muted"},
	{"color-accent-fg", "fgColor-accent"},
	{"color-accent-emphasis", "bgColor-accent-emphasis"},
	{"color-success-fg", "fgColor-success"},
	{"color-danger-fg", "fgColor-danger"},
	{"color-attention-fg", "fgColor-attention"},
	{"color-neutral-muted", "bgColor-neutral-muted"},
}

// resolve builds the full token map for one theme: the mode base palette,
// the mode prettylights palette, then the theme overrides.
func resolve(def themeDef) map[string]string {
	out := make(map[string]string, len(tokenOrder))
	base, pretty := lightBase, lightPrettylights
	if def.mode == "dark" {
		base, pretty = darkBase, darkPrettylights
	}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range pretty {
		out[k] = v
	}
	for k, v := range def.override {
		out[k] = v
	}
	return out
}

// genThemes renders themes.gen.css and aliases.gen.css under srcDir/css.
func genThemes(srcDir string) error {
	var b strings.Builder
	b.WriteString("/* Generated by fe/assets/build from palettes.go. Do not edit by hand. */\n")

	// The base scale, once, mode-neutral.
	b.WriteString(":root {\n")
	for _, hue := range baseScaleHues {
		steps := baseScale[hue]
		for i, v := range steps {
			fmt.Fprintf(&b, "  --base-color-%s-%d: %s;\n", hue, i, v)
		}
	}
	b.WriteString("  --base-color-black: #1f2328;\n")
	b.WriteString("  --base-color-white: #ffffff;\n")
	for i := 0; i <= 10; i++ {
		fmt.Fprintf(&b, "  --base-color-blackAlpha-%d: rgba(31, 35, 40, %s);\n", i, alphaStep(i))
	}
	for i := 0; i <= 10; i++ {
		fmt.Fprintf(&b, "  --base-color-whiteAlpha-%d: rgba(255, 255, 255, %s);\n", i, alphaStep(i))
	}
	b.WriteString("}\n")

	// Each theme is emitted twice: once for the explicit mode and once inside
	// a prefers-color-scheme block for data-color-mode="auto".
	for _, def := range themes {
		tokens := resolve(def)
		missing := len(tokens) != len(tokenOrder)
		if missing {
			return fmt.Errorf("theme %s resolves %d tokens, catalog has %d", def.name, len(tokens), len(tokenOrder))
		}
		sel := fmt.Sprintf("[data-color-mode=%q][data-%s-theme=%q]", def.mode, def.mode, def.name)
		fmt.Fprintf(&b, "%s {\n  color-scheme: %s;\n", sel, def.mode)
		for _, name := range tokenOrder {
			v, ok := tokens[name]
			if !ok {
				return fmt.Errorf("theme %s missing token %s", def.name, name)
			}
			fmt.Fprintf(&b, "  --%s: %s;\n", name, v)
		}
		b.WriteString("}\n")

		autoSel := fmt.Sprintf("[data-color-mode=\"auto\"][data-%s-theme=%q]", def.mode, def.name)
		fmt.Fprintf(&b, "@media (prefers-color-scheme: %s) {\n  %s {\n    color-scheme: %s;\n", def.mode, autoSel, def.mode)
		for _, name := range tokenOrder {
			fmt.Fprintf(&b, "    --%s: %s;\n", name, tokens[name])
		}
		b.WriteString("  }\n}\n")
	}
	if err := os.WriteFile(filepath.Join(srcDir, "css", "themes.gen.css"), []byte(b.String()), 0o644); err != nil {
		return err
	}

	var a strings.Builder
	a.WriteString("/* Generated by fe/assets/build from palettes.go. Do not edit by hand. */\n")
	a.WriteString("/* Legacy --color-* names aliased onto the canonical tokens. */\n")
	a.WriteString(":root {\n")
	for _, pair := range aliases {
		fmt.Fprintf(&a, "  --%s: var(--%s);\n", pair[0], pair[1])
	}
	a.WriteString("}\n")
	return os.WriteFile(filepath.Join(srcDir, "css", "aliases.gen.css"), []byte(a.String()), 0o644)
}

// alphaStep is the alpha for one step of the blackAlpha/whiteAlpha ramps:
// tenths from 0 to 0.9, with the last step at 0.95.
func alphaStep(i int) string {
	if i == 10 {
		return "0.95"
	}
	if i == 0 {
		return "0"
	}
	return fmt.Sprintf("0.%d", i)
}

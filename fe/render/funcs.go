package render

import (
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tamnd/githome/fe/assets"
)

// dictError is returned when dict is called with an odd number of arguments.
const dictError = "dict: want an even count of key/value arguments"

// funcMap is the template FuncMap, bound to a Set so asset and octicon can reach
// the manifest and icon registry. It is installed before parsing, so a template
// that names an absent helper fails the build, not a request. The helpers here
// are the shell-and-format subset F0 needs; later milestones add their own
// against the same Set. See implementation/03 section 4.
func (s *Set) funcMap() template.FuncMap {
	return template.FuncMap{
		"octicon":        octicon,
		"asset":          s.asset,
		"colorModeAttrs": colorModeAttrs,
		"relativeTime":   relativeTime,
		"pluralize":      pluralize,
		"hasPrefix":      strings.HasPrefix,
		"dict":           dict,
	}
}

// dict builds a map from an alternating key/value argument list so a template can
// pass more than one value to a partial that takes a single data argument (for
// example the repo header partial, which needs both the header model and the nav
// model). Keys must be strings. It returns an error on an odd argument count so a
// malformed call fails the render rather than dropping a value silently.
func dict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("%s", dictError)
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key %d is not a string", i)
		}
		m[key] = pairs[i+1]
	}
	return m, nil
}

// octicon renders the named icon as an inline <svg>. The optional arguments
// are a pixel size (defaults to 16) and a label, in either order: an unlabeled
// icon is decorative and marked aria-hidden, a labeled one renders role="img"
// with an aria-label and a <title> so it reads as an image (spec 2005 doc 16
// section 8.4). At 24px and above the helper prefers the 24-grid drawing when
// the set has one instead of upscaling the 16-grid glyph, and the viewBox
// always comes from the icon's own grid so the few non-square glyphs keep
// their aspect ratio. An unknown name renders a visible dashed-box placeholder
// rather than the raw name or an empty string, so a typo surfaces in review
// instead of shipping silently; the coverage test in icons_coverage_test.go
// turns that into a failing build for template references.
// octiconCache memoizes the rendered markup for the decorative (unlabeled) icon
// path, which is almost every call: the SVG body, grid, and attributes are fully
// determined by (name, size), so the result never changes. A labeled icon varies
// by its label and skips the cache. The map only ever grows to the small set of
// (name, size) pairs the templates reference, so it needs no eviction.
var octiconCache sync.Map // string key -> template.HTML

func octicon(name string, args ...any) (template.HTML, error) {
	size := 16
	label := ""
	for _, arg := range args {
		switch v := arg.(type) {
		case int:
			size = v
		case string:
			label = v
		default:
			return "", fmt.Errorf("octicon %q: argument %v is neither a size nor a label", name, arg)
		}
	}
	if size <= 0 {
		size = 16
	}
	// The decorative path is cacheable; a labeled icon is not, so it falls
	// through to render every time.
	var key string
	if label == "" {
		key = name + "\x00" + strconv.Itoa(size)
		if cached, ok := octiconCache.Load(key); ok {
			return cached.(template.HTML), nil
		}
	}
	icon, ok := assets.Icons[name]
	if size >= 24 {
		if icon24, ok24 := assets.Icons24[name]; ok24 {
			icon, ok = icon24, true
		}
	}
	if !ok {
		missing := template.HTML(fmt.Sprintf(
			`<svg class="octicon octicon-missing" width="%d" height="%d" viewBox="0 0 16 16" `+
				`aria-hidden="true" style="outline:1px dashed currentColor"><title>missing icon: %s</title></svg>`,
			size, size, template.HTMLEscapeString(name)))
		if key != "" {
			octiconCache.Store(key, missing)
		}
		return missing, nil
	}
	width := size * icon.Width / icon.Height
	aria := `aria-hidden="true"`
	title := ""
	if label != "" {
		aria = fmt.Sprintf(`role="img" aria-label="%s"`, template.HTMLEscapeString(label))
		title = "<title>" + template.HTMLEscapeString(label) + "</title>"
	}
	out := template.HTML(fmt.Sprintf(
		`<svg class="octicon octicon-%s" width="%d" height="%d" viewBox="0 0 %d %d" `+
			`fill="currentColor" %s>%s%s</svg>`,
		template.HTMLEscapeString(name), width, size, icon.Width, icon.Height, aria, title, icon.Body))
	if key != "" {
		octiconCache.Store(key, out)
	}
	return out, nil
}

// colorModeAttrs renders the three attributes the html element carries so CSS
// alone picks the active theme with no flash and no JavaScript: data-color-mode
// (auto, light or dark), data-light-theme and data-dark-theme. The values come
// from the viewer's preference, already validated by the view layer, so an
// unknown value never reaches here.
func colorModeAttrs(mode, light, dark string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(
		`data-color-mode="%s" data-light-theme="%s" data-dark-theme="%s"`,
		template.HTMLEscapeString(mode),
		template.HTMLEscapeString(light),
		template.HTMLEscapeString(dark)))
}

// relativeTime renders a time as a <relative-time> element carrying the machine
// datetime plus a static human fallback in the body, so a no-JS reader sees an
// absolute timestamp and the web component upgrades it to "3 days ago" when
// scripting is on. A zero time renders nothing.
func relativeTime(t time.Time) template.HTML {
	if t.IsZero() {
		return ""
	}
	iso := t.UTC().Format(time.RFC3339)
	human := t.UTC().Format("Jan 2, 2006")
	return template.HTML(fmt.Sprintf(
		`<relative-time datetime="%s">%s</relative-time>`,
		iso, template.HTMLEscapeString(human)))
}

// pluralize returns singular when n is 1 and plural otherwise. It does not format
// the number; the caller prints n next to the word.
func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

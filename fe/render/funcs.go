package render

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/tamnd/githome/fe/assets"
)

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
	}
}

// octicon renders the named icon as an inline <svg>. An unknown name renders a
// visible dashed-box placeholder rather than the raw name or an empty string, so
// a typo surfaces in review instead of shipping silently. size defaults to 16
// when zero or negative. The svg is marked aria-hidden; a meaningful icon must
// carry its own label at the call site (a visually-hidden span or aria-label).
func octicon(name string, size int) template.HTML {
	if size <= 0 {
		size = 16
	}
	body, ok := assets.Icons[name]
	if !ok {
		return template.HTML(fmt.Sprintf(
			`<svg class="octicon octicon-missing" width="%d" height="%d" viewBox="0 0 16 16" `+
				`aria-hidden="true" style="outline:1px dashed currentColor"><title>missing icon: %s</title></svg>`,
			size, size, template.HTMLEscapeString(name)))
	}
	return template.HTML(fmt.Sprintf(
		`<svg class="octicon octicon-%s" width="%d" height="%d" viewBox="0 0 16 16" `+
			`fill="currentColor" aria-hidden="true">%s</svg>`,
		template.HTMLEscapeString(name), size, size, body))
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

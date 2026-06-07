package markup

import (
	"regexp"

	"github.com/microcosm-cc/bluemonday"
)

// sanitize.go is stage 4, the security core. The Policy is a bluemonday allowlist
// (anything not explicitly permitted is removed) built once and reused. It runs on
// every render path, after render and before post-process, so it guards the web UI
// and the REST text/html media type from one set of rules. This is why
// template.HTML(userInput) is forbidden everywhere else: there is exactly one place
// user content becomes trusted HTML, and it always runs this policy. See
// implementation/10 section 5.

// Policy wraps the bluemonday allowlist so the trust boundary is one named,
// testable artifact.
type Policy struct {
	p *bluemonday.Policy
}

// classToken is one class name our own render and post-process stages emit: the
// fixed generated set plus the language-*, highlight-source-*, and pl-* code
// classes. classAllow matches a whole space-separated class attribute made only of
// these tokens, so a source-authored class is dropped and a comment cannot forge
// Primer chrome or pose as a generated control. See implementation/10 section 5.5.
const classToken = `(?:` +
	`anchor|markdown-alert(?:-[a-z]+)?|task-list-item|contains-task-list|` +
	`math|math-inline|math-display|render-diagram|user-mention|issue-link|commit-link|` +
	`highlight|highlight-source-[\w.+-]+|footnotes|footnote-ref|data-footnote-backref|emoji|` +
	`language-[\w.+#-]+|pl-[a-z0-9]+` +
	`)`

var (
	classAllow   = regexp.MustCompile(`^` + classToken + `(?:\s+` + classToken + `)*$`)
	tableAlign   = regexp.MustCompile(`^(?:left|center|right)$`)
	checkboxType = regexp.MustCompile(`^checkbox$`)
	octiconName  = regexp.MustCompile(`^[a-z][a-z-]*$`)
)

// NewPolicy builds the allowlist. It is deterministic and depends on no config, so
// the policy is identical on both surfaces. It is the GitHub-equivalent allowlist
// of implementation/10 section 5.
func NewPolicy() *Policy {
	p := bluemonday.NewPolicy()

	// Block and inline text flow.
	p.AllowElements(
		"h1", "h2", "h3", "h4", "h5", "h6",
		"p", "blockquote", "pre", "hr", "br",
		"ul", "ol", "li", "dl", "dt", "dd",
		"div", "span", "section",
		"em", "strong", "b", "i", "del", "ins", "sub", "sup", "mark", "small",
		"tt", "code", "kbd", "samp", "var", "abbr", "cite", "q",
		"details", "summary",
		"g-emoji", "figure", "figcaption",
	)

	// Headings and generated containers may carry the generated id and class set.
	p.AllowAttrs("id").Matching(bluemonday.SpaceSeparatedTokens).OnElements(
		"h1", "h2", "h3", "h4", "h5", "h6", "li", "div", "section", "a",
	)
	p.AllowAttrs("class").Matching(classAllow).OnElements(
		"div", "span", "section", "p", "code", "pre", "a", "img", "li", "ul",
	)
	p.AllowAttrs("data-octicon", "data-diagram-type").Matching(octiconName).Globally()

	// Links: href plus our injected rel/class. Schemes are gated below.
	p.AllowAttrs("href", "title").OnElements("a")
	p.AllowAttrs("rel").Matching(bluemonday.SpaceSeparatedTokens).OnElements("a")
	p.AllowStandardURLs()
	p.AllowURLSchemes("http", "https", "mailto")

	// Images: src/alt/title/dimensions only, plus data:image for inline icons.
	p.AllowAttrs("src", "alt", "title", "width", "height").OnElements("img")
	p.AllowImages()        // permits http(s) image URLs
	p.AllowDataURIImages() // permits data:image/* on img src

	// Tables (the GFM set) with the alignment attribute constrained to the three
	// real values.
	p.AllowElements("table", "thead", "tbody", "tfoot", "tr", "th", "td", "caption")
	p.AllowAttrs("align").Matching(tableAlign).OnElements("th", "td")
	p.AllowAttrs("colspan", "rowspan").Matching(bluemonday.Integer).OnElements("th", "td")

	// The generated task-list checkbox is the only permitted input.
	p.AllowAttrs("type").Matching(checkboxType).OnElements("input")
	p.AllowAttrs("checked", "disabled").OnElements("input")
	p.AllowElements("input")

	// nofollow is owned by post-process, which knows on-host from off-host. This must
	// be the last word: AllowStandardURLs (reached through AllowImages above) turns
	// bluemonday's blanket nofollow back on, so reset it here at the end so a link
	// back to our own host is not needlessly penalized.
	p.RequireNoFollowOnLinks(false)

	return &Policy{p: p}
}

// Sanitize strips every tag, attribute, and URL scheme not on the allowlist;
// neutralizes javascript:/vbscript:; drops script/style/iframe/form and every on*
// handler and inline style. It is the only call that turns rendered HTML into
// sanitized HTML.
func (pol *Policy) Sanitize(rendered []byte) []byte {
	return pol.p.SanitizeBytes(rendered)
}

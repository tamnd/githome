package markup

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// extensions.go holds the authored goldmark nodes, transformers, and renderers for
// the GitHub-beyond-spec constructs (implementation/10 section 4): alert
// blockquotes, @mention / #ref / SHA autolinks resolved through the caller's
// closure, inline and display math placeholders, and the fenced-code router that
// sends a block to the highlighter, a math island, or a diagram island. Each
// resolves against the per-call RenderContext stored on the parser context.

// ---- Alerts: > [!NOTE] blockquotes -------------------------------------------

var alertMarker = regexp.MustCompile(`^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*$`)

// alertOcticon maps an alert type to the octicon the template renders. The web
// layer's octicon helper resolves the name; markup only emits the class and a
// data attribute so the surface stays presentation-only.
var alertOcticon = map[string]string{
	"note":      "info",
	"tip":       "light-bulb",
	"important": "report",
	"warning":   "alert",
	"caution":   "stop",
}

var kindAlert = ast.NewNodeKind("Alert")

// alertNode is a typed callout converted from a marked blockquote. Its children
// are the blockquote's content with the marker line removed.
type alertNode struct {
	ast.BaseBlock
	alertType string // lowercase: note|tip|important|warning|caution
}

func (n *alertNode) Kind() ast.NodeKind { return kindAlert }
func (n *alertNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"type": n.alertType}, nil)
}

// alertTransformer rewrites a blockquote whose first line is [!TYPE] into an
// alertNode, stripping the marker.
type alertTransformer struct{}

func (t *alertTransformer) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	var quotes []*ast.Blockquote
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if bq, ok := n.(*ast.Blockquote); ok {
				quotes = append(quotes, bq)
			}
		}
		return ast.WalkContinue, nil
	})
	for _, bq := range quotes {
		para, ok := bq.FirstChild().(*ast.Paragraph)
		if !ok {
			continue
		}
		// The marker is the first line of the paragraph. goldmark's inline parser
		// splits "[!NOTE]" into several text nodes (the unmatched link-open "[" is its
		// own node), so reconstruct the line from the leading text nodes up to the
		// soft break rather than reading a single first child.
		line, lineNodes := firstLineText(para, source)
		m := alertMarker.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		// Drop the whole marker line (text nodes plus the break they carry) so the
		// callout body starts cleanly at the next line.
		for _, n := range lineNodes {
			para.RemoveChild(para, n)
		}
		if para.FirstChild() == nil {
			bq.RemoveChild(bq, para)
		}
		alert := &alertNode{alertType: strings.ToLower(m[1])}
		for c := bq.FirstChild(); c != nil; {
			next := c.NextSibling()
			bq.RemoveChild(bq, c)
			alert.AppendChild(alert, c)
			c = next
		}
		if parent := bq.Parent(); parent != nil {
			parent.ReplaceChild(parent, bq, alert)
		}
	}
}

// firstLineText reconstructs the first source line of a paragraph from its leading
// inline text nodes and returns that text plus the nodes that compose it (including
// the one carrying the soft break that ends the line). It stops at the first line
// break or the first non-text inline node, since an alert marker is plain text alone
// on its line.
func firstLineText(para ast.Node, source []byte) (string, []ast.Node) {
	var (
		b     strings.Builder
		nodes []ast.Node
	)
	for c := para.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(source))
			nodes = append(nodes, c)
			if t.SoftLineBreak() || t.HardLineBreak() {
				return b.String(), nodes
			}
		case *ast.String:
			b.Write(t.Value)
			nodes = append(nodes, c)
		default:
			return b.String(), nodes
		}
	}
	return b.String(), nodes
}

// alertRenderer renders an alertNode as a markdown-alert block.
type alertRenderer struct{}

func (r *alertRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindAlert, r.render)
}

func (r *alertRenderer) render(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*alertNode)
	if entering {
		title := strings.ToUpper(n.alertType[:1]) + n.alertType[1:]
		_, _ = fmt.Fprintf(w, `<div class="markdown-alert markdown-alert-%s" data-octicon=%q>`, n.alertType, alertOcticon[n.alertType])
		_, _ = fmt.Fprintf(w, `<p class="markdown-alert-title">%s</p>`, html.EscapeString(title))
	} else {
		_, _ = w.WriteString("</div>\n")
	}
	return ast.WalkContinue, nil
}

// ---- Math: $inline$ and $$display$$ placeholders -----------------------------

var kindMath = ast.NewNodeKind("Math")

// mathNode carries escaped LaTeX for the KaTeX client island to upgrade. markup
// runs no server-side JS; it only emits the placeholder.
type mathNode struct {
	ast.BaseInline
	latex   string
	display bool
}

func (n *mathNode) Kind() ast.NodeKind { return kindMath }
func (n *mathNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"display": fmt.Sprint(n.display)}, nil)
}

type mathRenderer struct{}

func (r *mathRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindMath, r.render)
}

func (r *mathRenderer) render(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*mathNode)
	if n.display {
		_, _ = fmt.Fprintf(w, `<div class="math math-display">%s</div>`, html.EscapeString(n.latex))
	} else {
		_, _ = fmt.Fprintf(w, `<span class="math math-inline">%s</span>`, html.EscapeString(n.latex))
	}
	return ast.WalkSkipChildren, nil
}

// mathParser is the inline parser for $inline$ and $$display$$. It runs during
// inline parsing, ahead of the backslash-escape parser, so a LaTeX body full of
// backslashes (\int, \frac) is consumed whole instead of being chopped at each
// backslash, which a post-parse text scan could not avoid. It matches only on a
// single source line; multi-line display math is written as a ```math fence.
type mathParser struct{}

func (p *mathParser) Trigger() []byte { return []byte{'$'} }

func (p *mathParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()
	if len(line) < 2 || line[0] != '$' {
		return nil
	}
	// Display: $$...$$ on one line.
	if line[1] == '$' {
		rest := line[2:]
		idx := indexBytes(rest, '$', '$')
		if idx < 0 {
			return nil
		}
		latex := string(rest[:idx])
		block.Advance(2 + idx + 2)
		return &mathNode{latex: latex, display: true}
	}
	// Inline: $...$ with no space just inside either delimiter and a non-empty body
	// (so a lone "$5 and $10" pair of prices is left as text).
	rest := line[1:]
	idx := indexByte(rest, '$')
	if idx <= 0 {
		return nil
	}
	body := rest[:idx]
	if body[0] == ' ' || body[len(body)-1] == ' ' {
		return nil
	}
	block.Advance(1 + idx + 1)
	return &mathNode{latex: string(body), display: false}
}

// indexByte is bytes.IndexByte spelled out to keep the parser free of a bytes import
// churn; it returns the first index of c in b or -1.
func indexByte(b []byte, c byte) int {
	for i := 0; i < len(b); i++ {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// indexBytes returns the first index in b where the two-byte sequence c1 c2 begins,
// or -1. Used to find the closing $$ of display math.
func indexBytes(b []byte, c1, c2 byte) int {
	for i := 0; i+1 < len(b); i++ {
		if b[i] == c1 && b[i+1] == c2 {
			return i
		}
	}
	return -1
}

// ---- Mentions, refs, SHAs (and inline math) ----------------------------------

var kindRef = ast.NewNodeKind("Ref")

// refNode is a resolved mention/issue/commit reference. href empty means the
// pattern did not resolve and the node renders as literal text.
type refNode struct {
	ast.BaseInline
	text  string
	href  string
	class string
}

func (n *refNode) Kind() ast.NodeKind { return kindRef }
func (n *refNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"href": n.href, "text": n.text}, nil)
}

type refRenderer struct{}

func (r *refRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindRef, r.render)
}

func (r *refRenderer) render(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*refNode)
	if n.href == "" {
		_, _ = w.WriteString(html.EscapeString(n.text))
		return ast.WalkSkipChildren, nil
	}
	_, _ = fmt.Fprintf(w, `<a href=%q class=%q>%s</a>`, n.href, n.class, html.EscapeString(n.text))
	return ast.WalkSkipChildren, nil
}

// inline pattern set, scanned per text node (each text node is a single source
// line, so none of these straddle a newline). Math is handled earlier by mathParser
// during inline parsing, so it is not in this set.
var (
	reCrossRef = regexp.MustCompile(`\b([A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9._-]+)#(\d+)\b`)
	reGHRef    = regexp.MustCompile(`\bGH-(\d+)\b`)
	reIssueRef = regexp.MustCompile(`(^|[^&\w])#(\d+)\b`)
	reMention  = regexp.MustCompile(`(^|[^\w/])@([A-Za-z0-9](?:[A-Za-z0-9-]{0,38})(?:/[A-Za-z0-9._-]+)?)`)
	reSHA      = regexp.MustCompile(`\b([0-9a-f]{7,40})\b`)
)

// autolinkTransformer splits text nodes on the inline patterns, replacing a match
// with a math placeholder or a resolved reference link. Mentions, issue refs, and
// SHAs link only when RenderContext.Resolve confirms the entity exists and is
// visible; otherwise they stay literal text. It skips text inside links, code
// spans, and raw HTML so it never rewrites a URL or a code identifier.
type autolinkTransformer struct{}

func (t *autolinkTransformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	rc, ctx := contextFrom(pc)
	source := reader.Source()
	var targets []*ast.Text
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.Kind() {
		case ast.KindLink, ast.KindAutoLink, ast.KindCodeSpan, ast.KindRawHTML:
			return ast.WalkSkipChildren, nil
		case ast.KindText:
			targets = append(targets, n.(*ast.Text))
		}
		return ast.WalkContinue, nil
	})
	for _, tn := range targets {
		t.splitText(ctx, tn, source, rc)
	}
}

// match is one located inline token inside a text node's value.
type match struct {
	start, end int
	node       ast.Node
}

func (t *autolinkTransformer) splitText(goCtx context.Context, tn *ast.Text, source []byte, rc RenderContext) {
	val := tn.Segment.Value(source)
	s := string(val)
	matches := t.scan(goCtx, s, rc)
	if len(matches) == 0 {
		return
	}
	parent := tn.Parent()
	if parent == nil {
		return
	}
	// Build the replacement sequence (literal runs interleaved with token nodes),
	// then splice it in front of the original text node and remove it.
	var nodes []ast.Node
	last := 0
	for _, m := range matches {
		if m.start > last {
			nodes = append(nodes, ast.NewString([]byte(s[last:m.start])))
		}
		nodes = append(nodes, m.node)
		last = m.end
	}
	if last < len(s) {
		nodes = append(nodes, ast.NewString([]byte(s[last:])))
	}
	for _, nn := range nodes {
		parent.InsertBefore(parent, tn, nn)
	}
	parent.RemoveChild(parent, tn)
}

// scan returns the non-overlapping leftmost token matches in priority order.
func (t *autolinkTransformer) scan(goCtx context.Context, s string, rc RenderContext) []match {
	type cand struct {
		loc  []int
		make func(loc []int) ast.Node
	}
	resolve := func(kind RefKind, raw, display, class string) func([]int) ast.Node {
		return func(_ []int) ast.Node {
			if rc.Resolve == nil {
				return &refNode{text: display}
			}
			target, ok := rc.Resolve(goCtx, kind, raw)
			if !ok {
				return &refNode{text: display}
			}
			return &refNode{text: display, href: target, class: class}
		}
	}
	var out []match
	pos := 0
	for pos < len(s) {
		best := -1
		var chosen cand
		try := func(re *regexp.Regexp, mk func(loc []int) ast.Node, group int) {
			loc := re.FindStringSubmatchIndex(s[pos:])
			if loc == nil {
				return
			}
			start := pos + loc[2*group]
			if best == -1 || start < best {
				best = start
				chosen = cand{loc: shift(loc, pos), make: mk}
			}
		}
		try(reCrossRef, func(loc []int) ast.Node {
			raw := s[loc[0]:loc[1]]
			return resolve(RefIssue, raw, raw, "issue-link")(loc)
		}, 0)
		try(reGHRef, func(loc []int) ast.Node {
			raw := s[loc[0]:loc[1]]
			return resolve(RefIssue, raw, raw, "issue-link")(loc)
		}, 0)
		try(reIssueRef, func(loc []int) ast.Node {
			num := s[loc[4]:loc[5]] // the digits group, without the leading '#'
			// Resolve keys on the bare number; the link text keeps the '#'.
			return resolve(RefIssue, num, "#"+num, "issue-link")(loc)
		}, 2)
		try(reMention, func(loc []int) ast.Node {
			raw := s[loc[4]:loc[5]] // the login group, without the leading @
			return resolve(RefMention, raw, "@"+raw, "user-mention")(loc)
		}, 2)
		try(reSHA, func(loc []int) ast.Node {
			raw := s[loc[2]:loc[3]]
			return resolve(RefCommit, raw, shortSHADisplay(raw), "commit-link")(loc)
		}, 0)
		if best == -1 {
			break
		}
		node := chosen.make(chosen.loc)
		out = append(out, match{start: matchStart(chosen.loc), end: chosen.loc[1], node: node})
		pos = chosen.loc[1]
	}
	return out
}

// matchStart is the start of the matched token, which for the issue/mention rules
// is the capture group (skipping the boundary char), and otherwise the whole
// match.
func matchStart(loc []int) int {
	// loc[0] is the full match start; for boundary-prefixed patterns the token we
	// want to replace begins at the first non-boundary group when present.
	if len(loc) >= 6 && loc[4] >= 0 && loc[4] > loc[0] {
		// reIssueRef: token is "#digits"; back up one to include the '#'.
		// reMention: token is "login"; back up one to include the '@'.
		return loc[4] - 1
	}
	return loc[0]
}

func shift(loc []int, by int) []int {
	out := make([]int, len(loc))
	for i, v := range loc {
		if v < 0 {
			out[i] = v
			continue
		}
		out[i] = v + by
	}
	return out
}

func shortSHADisplay(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// ---- Fenced-code router ------------------------------------------------------

// diagramLangs are the fenced info strings routed to a sandboxed diagram island
// rather than the highlighter.
var diagramLangs = map[string]bool{
	"mermaid": true, "geojson": true, "topojson": true, "stl": true,
}

// codeBlockRenderer renders fenced and indented code blocks. A fenced block whose
// info string is "math" becomes a display-math island, a diagram info string
// becomes a diagram island, and anything else is highlighted through the
// Renderer's highlighter. An indented block is highlighted as plain text.
type codeBlockRenderer struct {
	r *Renderer
}

func (cr *codeBlockRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, cr.renderFenced)
	reg.Register(ast.KindCodeBlock, cr.renderIndented)
}

func (cr *codeBlockRenderer) renderFenced(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.FencedCodeBlock)
	lang := ""
	if n.Info != nil {
		lang = strings.ToLower(strings.Fields(string(n.Info.Segment.Value(source)))[0])
	}
	code := codeText(node, source)
	switch {
	case lang == "math":
		_, _ = fmt.Fprintf(w, `<div class="math math-display">%s</div>`+"\n", html.EscapeString(string(code)))
	case diagramLangs[lang]:
		_, _ = fmt.Fprintf(w, `<div class="render-diagram" data-diagram-type=%q><pre>%s</pre></div>`+"\n", lang, html.EscapeString(string(code)))
	default:
		cr.writeHighlighted(w, code, lang)
	}
	return ast.WalkSkipChildren, nil
}

func (cr *codeBlockRenderer) renderIndented(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	cr.writeHighlighted(w, codeText(node, source), "")
	return ast.WalkSkipChildren, nil
}

// writeHighlighted emits the highlighted block. The highlighter returns escaped
// text wrapped in pl-* spans; an unknown language yields escaped text with no
// spans, so the block is always safe and always readable.
func (cr *codeBlockRenderer) writeHighlighted(w util.BufWriter, code []byte, lang string) {
	class := "highlight"
	if lang != "" {
		class += " highlight-source-" + lang
	}
	_, _ = fmt.Fprintf(w, `<div class=%q><pre>`, class)
	lines, _ := cr.r.highlightLines(code, lang)
	for i, ln := range lines {
		if i > 0 {
			_, _ = w.WriteString("\n")
		}
		_, _ = w.WriteString(string(ln))
	}
	_, _ = w.WriteString("</pre></div>\n")
}

// codeText concatenates a code block's source lines.
func codeText(n ast.Node, source []byte) []byte {
	var b bytes.Buffer
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		b.Write(seg.Value(source))
	}
	return b.Bytes()
}

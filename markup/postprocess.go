package markup

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"net/url"
	"path"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// postprocess.go is stage 5, the last stage, and it runs on already-sanitized HTML.
// That ordering is deliberate: the bluemonday wall (stage 4) has already removed
// every tag and attribute not on the allowlist, so this stage only decorates a tree
// of trusted nodes. It adds heading anchors, marks off-host links nofollow, proxies
// external images through camo, and wires the GFM task-list classes (implementation
// /10 section 7). It parses the sanitized fragment into a DOM rather than rewriting
// with regexps so a decoration never reopens an injection the sanitizer just closed.

// postProcess decorates sanitized HTML and returns the final trusted bytes. It
// never adds an element or scheme the policy would have rejected; it only sets ids,
// rel, src, and class on nodes the policy already permits. On a parse error it
// returns the input unchanged (still sanitized, just undecorated), never an error.
func (r *Renderer) postProcess(sanitized []byte, rc RenderContext) []byte {
	ctx := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(bytes.NewReader(sanitized), ctx)
	if err != nil {
		r.log.Warn("post-process parse failed, leaving sanitized html undecorated", "err", err)
		return sanitized
	}
	pp := &postProcessor{
		host:  hostOf(r.baseURL),
		camo:  r.camo,
		slugs: map[string]int{},
		mode:  rc.Mode,
		repo:  rc.Repo,
		ref:   rc.Ref,
		path:  rc.Path,
	}
	for _, n := range nodes {
		pp.walk(n)
	}
	var buf bytes.Buffer
	for _, n := range nodes {
		if err := html.Render(&buf, n); err != nil {
			r.log.Warn("post-process render failed, leaving sanitized html undecorated", "err", err)
			return sanitized
		}
	}
	return buf.Bytes()
}

// postProcessor carries the per-render state the walk threads through: the slug
// counter that makes duplicate heading ids stable, the host the page is served
// from (so a link or image to that host is treated as local), and the camo signer.
type postProcessor struct {
	host  string
	camo  camoSigner
	slugs map[string]int
	mode  RenderMode
	repo  *RepoRef
	ref   string
	path  string
}

func (pp *postProcessor) walk(n *html.Node) {
	if n.Type == html.ElementNode {
		switch n.DataAtom {
		case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
			pp.headingAnchor(n)
		case atom.A:
			pp.linkRel(n)
		case atom.Img:
			pp.camoImage(n)
		case atom.Li:
			pp.taskListItem(n)
		case atom.Div:
			pp.alertIcon(n)
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		pp.walk(c)
	}
}

// linkIconPath is the 16-grid octicon-link path data the heading anchors carry,
// copied from the same @primer/octicons v19.28.1 data (MIT, (c) GitHub Inc.) as
// the alert icons above.
const linkIconPath = "m7.775 3.275 1.25-1.25a3.5 3.5 0 1 1 4.95 4.95l-2.5 2.5a3.5 3.5 0 0 1-4.95 0 .751.751 0 0 1 .018-1.042.751.751 0 0 1 1.042-.018 1.998 1.998 0 0 0 2.83 0l2.5-2.5a2.002 2.002 0 0 0-2.83-2.83l-1.25 1.25a.751.751 0 0 1-1.042-.018.751.751 0 0 1-.018-1.042Zm-4.69 9.64a1.998 1.998 0 0 0 2.83 0l1.25-1.25a.751.751 0 0 1 1.042.018.751.751 0 0 1 .018 1.042l-1.25 1.25a3.5 3.5 0 1 1-4.95-4.95l2.5-2.5a3.5 3.5 0 0 1 4.95 0 .751.751 0 0 1-.018 1.042.751.751 0 0 1-1.042.018 1.998 1.998 0 0 0-2.83 0l-2.5 2.5a1.998 1.998 0 0 0 0 2.83Z"

// headingAnchor gives a heading a stable id and prepends the clickable anchor
// GitHub renders, with the octicon-link glyph as its visible body. The id is
// prefixed user-content- so a heading slug can never collide with an
// application-owned element id on the page (implementation/10 section 7.1).
// Duplicate slugs in one document get a -1, -2 suffix.
func (pp *postProcessor) headingAnchor(h *html.Node) {
	slug := pp.uniqueSlug(slugify(textContent(h)))
	id := "user-content-" + slug
	setAttr(h, "id", id)
	anchor := &html.Node{
		Type:     html.ElementNode,
		Data:     "a",
		DataAtom: atom.A,
		Attr: []html.Attribute{
			{Key: "id", Val: id},
			{Key: "class", Val: "anchor"},
			{Key: "href", Val: "#" + id},
			{Key: "aria-hidden", Val: "true"},
		},
	}
	svg := &html.Node{
		Type:     html.ElementNode,
		Data:     "svg",
		DataAtom: atom.Svg,
		Attr: []html.Attribute{
			{Key: "class", Val: "octicon octicon-link"},
			{Key: "viewBox", Val: "0 0 16 16"},
			{Key: "width", Val: "16"},
			{Key: "height", Val: "16"},
			{Key: "fill", Val: "currentColor"},
			{Key: "aria-hidden", Val: "true"},
		},
	}
	svg.AppendChild(&html.Node{
		Type: html.ElementNode,
		Data: "path",
		Attr: []html.Attribute{{Key: "d", Val: linkIconPath}},
	})
	anchor.AppendChild(svg)
	// This anchor is emitted after sanitize, so its aria-hidden survives: post-process
	// is the last stage and decorates trusted nodes, it does not run the allowlist again.
	if first := h.FirstChild; first != nil {
		h.InsertBefore(anchor, first)
	} else {
		h.AppendChild(anchor)
	}
}

// linkRel marks an off-host link rel="nofollow". Links back to our own host keep
// their natural rel so internal navigation is not penalized. A relative or fragment
// href has no host and is treated as local.
func (pp *postProcessor) linkRel(a *html.Node) {
	href := attr(a, "href")
	if href == "" {
		return
	}
	// In a rendered file, a relative link points into the repo, so resolve it to the
	// blob URL for the file's ref before the external check (which then sees a local
	// link and leaves it alone).
	if rewritten, ok := pp.resolveRelative(href, "blob"); ok {
		setAttr(a, "href", rewritten)
		return
	}
	if pp.isExternal(href) {
		rel := attr(a, "rel")
		setAttr(a, "rel", mergeRel(rel, "nofollow"))
	}
}

// camoImage rewrites an external image src to a signed camo URL so the page never
// leaks a reader's IP to a third-party host and never serves mixed content. A
// data: image, or an image already on our host, is left as is. When camo is not
// configured the src is left unchanged (the link still works, it just is not
// proxied), which is logged once at startup, not per image.
func (pp *postProcessor) camoImage(img *html.Node) {
	src := attr(img, "src")
	if src == "" || strings.HasPrefix(src, "data:") {
		return
	}
	// A relative image in a rendered file points at a blob served raw, not at the
	// blob page; resolve to the raw URL, which is on-host and so skips camo.
	if rewritten, ok := pp.resolveRelative(src, "raw"); ok {
		setAttr(img, "src", rewritten)
		return
	}
	if !pp.isExternal(src) {
		return
	}
	if proxied, ok := pp.camo.sign(src); ok {
		setAttr(img, "src", proxied)
	}
}

// alertIconPaths holds the path data for the five octicons the alert callouts
// carry, keyed by the data-octicon name the render stage emits. markup is a
// leaf package, so it cannot reach the fe icon registry; these five 16-grid
// paths are copied from the same @primer/octicons v19.28.1 data (MIT, (c)
// GitHub Inc.) the registry carries.
var alertIconPaths = map[string]string{
	"info":       "M0 8a8 8 0 1 1 16 0A8 8 0 0 1 0 8Zm8-6.5a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13ZM6.5 7.75A.75.75 0 0 1 7.25 7h1a.75.75 0 0 1 .75.75v2.75h.25a.75.75 0 0 1 0 1.5h-2a.75.75 0 0 1 0-1.5h.25v-2h-.25a.75.75 0 0 1-.75-.75ZM8 6a1 1 0 1 1 0-2 1 1 0 0 1 0 2Z",
	"light-bulb": "M8 1.5c-2.363 0-4 1.69-4 3.75 0 .984.424 1.625.984 2.304l.214.253c.223.264.47.556.673.848.284.411.537.896.621 1.49a.75.75 0 0 1-1.484.211c-.04-.282-.163-.547-.37-.847a8.456 8.456 0 0 0-.542-.68c-.084-.1-.173-.205-.268-.32C3.201 7.75 2.5 6.766 2.5 5.25 2.5 2.31 4.863 0 8 0s5.5 2.31 5.5 5.25c0 1.516-.701 2.5-1.328 3.259-.095.115-.184.22-.268.319-.207.245-.383.453-.541.681-.208.3-.33.565-.37.847a.751.751 0 0 1-1.485-.212c.084-.593.337-1.078.621-1.489.203-.292.45-.584.673-.848.075-.088.147-.173.213-.253.561-.679.985-1.32.985-2.304 0-2.06-1.637-3.75-4-3.75ZM5.75 12h4.5a.75.75 0 0 1 0 1.5h-4.5a.75.75 0 0 1 0-1.5ZM6 15.25a.75.75 0 0 1 .75-.75h2.5a.75.75 0 0 1 0 1.5h-2.5a.75.75 0 0 1-.75-.75Z",
	"report":     "M0 1.75C0 .784.784 0 1.75 0h12.5C15.216 0 16 .784 16 1.75v9.5A1.75 1.75 0 0 1 14.25 13H8.06l-2.573 2.573A1.458 1.458 0 0 1 3 14.543V13H1.75A1.75 1.75 0 0 1 0 11.25Zm1.75-.25a.25.25 0 0 0-.25.25v9.5c0 .138.112.25.25.25h2a.75.75 0 0 1 .75.75v2.19l2.72-2.72a.749.749 0 0 1 .53-.22h6.5a.25.25 0 0 0 .25-.25v-9.5a.25.25 0 0 0-.25-.25Zm7 2.25v2.5a.75.75 0 0 1-1.5 0v-2.5a.75.75 0 0 1 1.5 0ZM9 9a1 1 0 1 1-2 0 1 1 0 0 1 2 0Z",
	"alert":      "M6.457 1.047c.659-1.234 2.427-1.234 3.086 0l6.082 11.378A1.75 1.75 0 0 1 14.082 15H1.918a1.75 1.75 0 0 1-1.543-2.575Zm1.763.707a.25.25 0 0 0-.44 0L1.698 13.132a.25.25 0 0 0 .22.368h12.164a.25.25 0 0 0 .22-.368Zm.53 3.996v2.5a.75.75 0 0 1-1.5 0v-2.5a.75.75 0 0 1 1.5 0ZM9 11a1 1 0 1 1-2 0 1 1 0 0 1 2 0Z",
	"stop":       "M4.47.22A.749.749 0 0 1 5 0h6c.199 0 .389.079.53.22l4.25 4.25c.141.14.22.331.22.53v6a.749.749 0 0 1-.22.53l-4.25 4.25A.749.749 0 0 1 11 16H5a.749.749 0 0 1-.53-.22L.22 11.53A.749.749 0 0 1 0 11V5c0-.199.079-.389.22-.53Zm.84 1.28L1.5 5.31v5.38l3.81 3.81h5.38l3.81-3.81V5.31L10.69 1.5ZM8 4a.75.75 0 0 1 .75.75v3.5a.75.75 0 0 1-1.5 0v-3.5A.75.75 0 0 1 8 4Zm0 8a1 1 0 1 1 0-2 1 1 0 0 1 0 2Z",
}

// alertIcon prepends the kind's octicon to an alert callout title. The render
// stage emits the data-octicon name on the markdown-alert div and the sanitizer
// allowlists it; this stage turns it into the inline <svg> the title shows, the
// same after-sanitize decoration the heading anchor uses. A div without the
// attribute, an unknown name, or a callout with no title is left alone.
func (pp *postProcessor) alertIcon(div *html.Node) {
	if !hasClass(div, "markdown-alert") {
		return
	}
	d, ok := alertIconPaths[attr(div, "data-octicon")]
	if !ok {
		return
	}
	var title *html.Node
	for c := div.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == atom.P && hasClass(c, "markdown-alert-title") {
			title = c
			break
		}
	}
	if title == nil {
		return
	}
	svg := &html.Node{
		Type:     html.ElementNode,
		Data:     "svg",
		DataAtom: atom.Svg,
		Attr: []html.Attribute{
			{Key: "class", Val: "octicon"},
			{Key: "viewBox", Val: "0 0 16 16"},
			{Key: "width", Val: "16"},
			{Key: "height", Val: "16"},
			{Key: "fill", Val: "currentColor"},
			{Key: "aria-hidden", Val: "true"},
		},
	}
	svg.AppendChild(&html.Node{
		Type: html.ElementNode,
		Data: "path",
		Attr: []html.Attribute{{Key: "d", Val: d}},
	})
	if first := title.FirstChild; first != nil {
		title.InsertBefore(svg, first)
	} else {
		title.AppendChild(svg)
	}
}

// taskListItem adds the GFM task-list classes when a list item leads with a
// disabled checkbox. GFM already renders the <input type="checkbox" disabled>; this
// adds the task-list-item class to the <li> and contains-task-list to the parent
// <ul> so the themed CSS can hang the marker (implementation/10 section 7.4).
func (pp *postProcessor) taskListItem(li *html.Node) {
	box := leadingCheckbox(li)
	if box == nil {
		return
	}
	addClass(li, "task-list-item")
	if p := li.Parent; p != nil && (p.DataAtom == atom.Ul || p.DataAtom == atom.Ol) {
		addClass(p, "contains-task-list")
	}
}

// resolveRelative turns a repo-relative href in a rendered file into an absolute
// app path under the given route segment ("blob" for links, "raw" for images),
// matching GitHub's README link rewriting. It returns ok=false (leaving the href
// untouched) for anything that is not a plain repo-relative path: other render
// modes, an absolute URL, a root-relative or protocol-relative href, a bare
// fragment, or a render with no repo/ref context. The query and fragment ride
// along unchanged.
func (pp *postProcessor) resolveRelative(raw, segment string) (string, bool) {
	if pp.mode != ModeFile || pp.repo == nil || pp.ref == "" {
		return "", false
	}
	if raw == "" || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "/") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" {
		return "", false
	}
	if u.Path == "" { // a pure ?query or #fragment with no path component
		return "", false
	}
	dir := path.Dir(pp.path)
	if dir == "." {
		dir = ""
	}
	target := path.Clean(path.Join(dir, u.Path))
	target = strings.TrimPrefix(target, "/")
	out := "/" + pp.repo.Owner + "/" + pp.repo.Name + "/" + segment + "/" + pp.ref + "/" + target
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		out += "#" + u.Fragment
	}
	return out, true
}

func (pp *postProcessor) isExternal(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Host == "" { // relative or fragment: local
		return false
	}
	if pp.host == "" {
		return true
	}
	return !strings.EqualFold(u.Host, pp.host)
}

// uniqueSlug returns slug the first time and slug-N on the Nth repeat, matching
// GitHub's duplicate-heading behavior so a table of contents stays linkable.
func (pp *postProcessor) uniqueSlug(slug string) string {
	n := pp.slugs[slug]
	pp.slugs[slug] = n + 1
	if n == 0 {
		return slug
	}
	return slug + "-" + strconv.Itoa(n)
}

// ---- camo image proxy --------------------------------------------------------

// camoSigner signs an external image URL into a camo proxy path. The zero signer
// (no secret or no base) is disabled and sign returns ok=false, so an unconfigured
// deployment serves images directly rather than through a dead proxy.
type camoSigner struct {
	secret  []byte
	baseURL string
}

func newCamoSigner(secret []byte, base string) camoSigner {
	if len(secret) == 0 || base == "" {
		return camoSigner{}
	}
	return camoSigner{secret: secret, baseURL: strings.TrimRight(base, "/")}
}

// sign returns the camo URL for an external image: base/<hmac-sha1-hex>/<url-hex>,
// the go-camo / github-camo URL shape. ok=false when the signer is disabled.
func (c camoSigner) sign(target string) (string, bool) {
	if len(c.secret) == 0 || c.baseURL == "" {
		return "", false
	}
	mac := hmac.New(sha1.New, c.secret)
	_, _ = mac.Write([]byte(target))
	digest := hex.EncodeToString(mac.Sum(nil))
	encoded := hex.EncodeToString([]byte(target))
	return c.baseURL + "/" + digest + "/" + encoded, true
}

// ---- slug and DOM helpers ----------------------------------------------------

// slugify lowercases, drops characters that are not word characters, spaces, or
// hyphens, then turns runs of spaces into single hyphens, the GitHub heading-slug
// rule.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-':
			b.WriteRune('-')
		case r == '_':
			b.WriteRune(r)
		}
		// Everything else (punctuation, symbols) is dropped.
	}
	out := b.String()
	// Collapse repeated hyphens that came from spaces around dropped punctuation.
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return strings.Trim(out, "-")
}

// textContent returns the concatenated text of a node's subtree, skipping the
// anchor we may have already inserted.
func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
			return
		}
		if x.Type == html.ElementNode && x.DataAtom == atom.A && hasClass(x, "anchor") {
			return
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// leadingCheckbox returns the disabled checkbox input that makes a list item a task
// item, or nil. GFM emits the input as the first inline content of the item.
func leadingCheckbox(li *html.Node) *html.Node {
	for c := li.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			if strings.TrimSpace(c.Data) == "" {
				continue
			}
			return nil
		case html.ElementNode:
			if c.DataAtom == atom.Input && strings.EqualFold(attr(c, "type"), "checkbox") {
				return c
			}
			// A paragraph-wrapped task item keeps the checkbox one level in.
			if c.DataAtom == atom.P {
				if box := leadingCheckbox(c); box != nil {
					return box
				}
			}
			return nil
		}
	}
	return nil
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func setAttr(n *html.Node, key, val string) {
	for i, a := range n.Attr {
		if a.Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

func addClass(n *html.Node, class string) {
	if hasClass(n, class) {
		return
	}
	existing := attr(n, "class")
	if existing == "" {
		setAttr(n, "class", class)
		return
	}
	setAttr(n, "class", existing+" "+class)
}

// mergeRel adds a rel token if it is not already present, preserving any rel the
// renderer set.
func mergeRel(existing, add string) string {
	for _, t := range strings.Fields(existing) {
		if t == add {
			return existing
		}
	}
	if existing == "" {
		return add
	}
	return existing + " " + add
}

func hostOf(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

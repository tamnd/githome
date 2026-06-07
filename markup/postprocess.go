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
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		pp.walk(c)
	}
}

// headingAnchor gives a heading a stable id and prepends the clickable anchor
// GitHub renders. The id is prefixed user-content- so a heading slug can never
// collide with an application-owned element id on the page (implementation/10
// section 7.1). Duplicate slugs in one document get a -1, -2 suffix.
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

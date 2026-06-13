package rest

import (
	"context"
	"io"
	"net/http"
	"sort"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/markup"
)

// This file holds the small static-data and markup-render families GitHub serves
// outside any repository: /zen, /octocat, /licenses, /emojis,
// /gitignore/templates, /feeds, and POST /markdown(/raw). gh repo create
// --license, renovate's emoji handling, and markdown previews all read these.
// The data is intentionally a representative built-in set rather than the full
// github.com catalog; the shapes match so clients parse them.

// writeText writes a text/plain body, the content type /zen and /octocat use.
func writeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// writeHTML writes a text/html body, the content type the markdown endpoints
// return. The HTML has already passed the markup sanitizer.
func writeHTML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// zenLine is the design philosophy GET /zen returns. github.com rotates through
// a set at random; a self-hosted instance returns a fixed line, which keeps the
// response deterministic and is all any client (gh, octokit smoke tests) checks.
const zenLine = "Keep it logically awesome."

// handleZen serves GET /zen: a single line of plain text, no trailing newline,
// matching github.com.
func handleZen(c *mizu.Ctx) error {
	writeText(c.Writer(), http.StatusOK, zenLine)
	return nil
}

// handleOctocat serves GET /octocat: ASCII art of the octocat with a speech
// bubble. The optional s query parameter sets the bubble text; with none, the
// zen line is used, as github.com does.
func handleOctocat(c *mizu.Ctx) error {
	say := c.Query("s")
	if say == "" {
		say = zenLine
	}
	writeText(c.Writer(), http.StatusOK, octocat(say))
	return nil
}

// octocat lays the speech bubble above the cat. The bubble width tracks the
// message so a long s parameter still frames cleanly.
func octocat(say string) string {
	top := " "
	bottom := " "
	for range say {
		top += "_"
		bottom += "-"
	}
	return "" +
		"           MMM.           .MMM\n" +
		"           MMMMMMMMMMMMMMMMMMM\n" +
		"           MMMMMMMMMMMMMMMMMMM      " + top + "\n" +
		"          MMMMMMMMMMMMMMMMMMMMM    | " + say + " |\n" +
		"         MMMMMMMMMMMMMMMMMMMMMMM   " + bottom + "\n" +
		"        MMMMMMMMMMMMMMMMMMMMMMMM /\n" +
		"        MMMM::- -:::::::- -::MMMM\n" +
		"         MM~:~ 00~:::::~ 00~:~MM\n" +
		"    .~~~~88x88~:::::::::~88x88~~~~.\n" +
		"        :~MMMMMMMMMMMMMMMMMM~:\n" +
		"   .~~~~MM:~MM:~MMMM~::~MMMM:~MM~~~~.\n" +
		"~~~~~~MM~~MM~~M~:~MMMM~::~MMMM~~M~~MM~~~~~~\n"
}

// license is one entry of the /licenses family. The list shape omits the body
// and a few descriptive fields; the single-license shape carries the full set.
type license struct {
	Key            string   `json:"key"`
	Name           string   `json:"name"`
	SPDXID         string   `json:"spdx_id"`
	URL            string   `json:"url"`
	NodeID         string   `json:"node_id"`
	HTMLURL        string   `json:"html_url,omitempty"`
	Description    string   `json:"description,omitempty"`
	Implementation string   `json:"implementation,omitempty"`
	Permissions    []string `json:"permissions,omitempty"`
	Conditions     []string `json:"conditions,omitempty"`
	Limitations    []string `json:"limitations,omitempty"`
	Body           string   `json:"body,omitempty"`
	Featured       bool     `json:"featured"`
}

// builtinLicenses is the common set gh repo create --license and octokit
// licenses.getAllCommonlyUsed walk. Bodies are omitted here (a self-hosted host
// is not a license-text registry); the metadata is enough for those callers.
var builtinLicenses = []license{
	{Key: "mit", Name: "MIT License", SPDXID: "MIT", Featured: true},
	{Key: "apache-2.0", Name: "Apache License 2.0", SPDXID: "Apache-2.0", Featured: true},
	{Key: "gpl-3.0", Name: "GNU General Public License v3.0", SPDXID: "GPL-3.0", Featured: true},
	{Key: "gpl-2.0", Name: "GNU General Public License v2.0", SPDXID: "GPL-2.0", Featured: true},
	{Key: "bsd-2-clause", Name: "BSD 2-Clause \"Simplified\" License", SPDXID: "BSD-2-Clause"},
	{Key: "bsd-3-clause", Name: "BSD 3-Clause \"New\" or \"Revised\" License", SPDXID: "BSD-3-Clause"},
	{Key: "mpl-2.0", Name: "Mozilla Public License 2.0", SPDXID: "MPL-2.0"},
	{Key: "lgpl-2.1", Name: "GNU Lesser General Public License v2.1", SPDXID: "LGPL-2.1"},
	{Key: "agpl-3.0", Name: "GNU Affero General Public License v3.0", SPDXID: "AGPL-3.0"},
	{Key: "unlicense", Name: "The Unlicense", SPDXID: "Unlicense"},
}

// handleLicensesList serves GET /licenses, the common-license index. The
// featured query parameter narrows to the featured subset, as github.com does.
func handleLicensesList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		onlyFeatured := c.Query("featured") == "true"
		out := make([]license, 0, len(builtinLicenses))
		for _, l := range builtinLicenses {
			if onlyFeatured && !l.Featured {
				continue
			}
			out = append(out, licenseListEntry(d, l))
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleLicenseGet serves GET /licenses/{key}: the single license, 404 for an
// unknown key.
func handleLicenseGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		key := c.Param("key")
		for _, l := range builtinLicenses {
			if l.Key == key {
				writeJSON(c.Writer(), http.StatusOK, licenseListEntry(d, l))
				return nil
			}
		}
		writeError(c.Writer(), errNotFound())
		return nil
	}
}

// licenseListEntry fills the URL and node_id fields against the configured API
// base. node_id mirrors github.com's MDc6TGljZW5zZTA= form loosely: a stable
// per-key token rather than a real graph id, since licenses are not graph nodes
// here.
func licenseListEntry(d Deps, l license) license {
	l.NodeID = "LI_" + l.Key
	if d.URLs != nil {
		l.URL = d.URLs.API("licenses", l.Key)
	} else {
		l.URL = "/licenses/" + l.Key
	}
	return l
}

// builtinEmojis is a representative slice of the github.com emoji map. renovate
// only needs the shape (name to image URL) to function; the full ~1800-entry
// catalog is not reproduced.
var builtinEmojis = []string{
	"+1", "-1", "100", "tada", "rocket", "eyes", "heart", "fire",
	"smile", "laughing", "wink", "thumbsup", "thumbsdown", "white_check_mark",
	"x", "warning", "bug", "sparkles", "boom", "construction",
}

// handleEmojis serves GET /emojis: a name to image-URL map. The URLs point at
// github.com's emoji asset host, the same target github.com returns, so a client
// that follows them still resolves an image.
func handleEmojis(c *mizu.Ctx) error {
	out := make(map[string]string, len(builtinEmojis))
	for _, name := range builtinEmojis {
		out[name] = "https://github.githubassets.com/images/icons/emoji/" + name + ".png"
	}
	writeJSON(c.Writer(), http.StatusOK, out)
	return nil
}

// builtinGitignore is the common set gh repo create --gitignore and octokit
// gitignore.getAllTemplates walk. The names match github/gitignore filenames.
var builtinGitignore = []string{
	"Actionscript", "Android", "C", "C++", "CMake", "Dart", "Elixir", "Erlang",
	"Go", "Gradle", "Haskell", "Java", "Maven", "Node", "Objective-C", "Perl",
	"Python", "Ruby", "Rust", "Scala", "Swift", "TeX", "Unity", "VisualStudio",
}

// handleGitignoreTemplates serves GET /gitignore/templates: the sorted list of
// template names.
func handleGitignoreTemplates(c *mizu.Ctx) error {
	out := append([]string(nil), builtinGitignore...)
	sort.Strings(out)
	writeJSON(c.Writer(), http.StatusOK, out)
	return nil
}

// handleGitignoreTemplate serves GET /gitignore/templates/{name}: the named
// template as {name, source}. The source is left empty (a self-hosted host is
// not a gitignore-body registry); the name confirms availability, which is what
// gh repo create checks. An unknown name is a 404.
func handleGitignoreTemplate(c *mizu.Ctx) error {
	name := c.Param("name")
	for _, t := range builtinGitignore {
		if t == name {
			writeJSON(c.Writer(), http.StatusOK, map[string]any{
				"name":   t,
				"source": "",
			})
			return nil
		}
	}
	writeError(c.Writer(), errNotFound())
	return nil
}

// handleFeeds serves GET /feeds: the Atom feed URL map. The public timeline and
// the security-advisories feed are the ones that need no authenticated token;
// the per-user feeds carry no private feed token here (a self-hosted instance
// does not mint the github.com-style feed tokens), so their URLs are templates
// without a secret, which is still the correct shape.
func handleFeeds(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		base := ""
		if d.URLs != nil {
			base = d.URLs.HTMLBase()
		}
		timeline := base + "/timeline"
		userTmpl := base + "/{user}"
		out := map[string]any{
			"timeline_url":            timeline,
			"user_url":                userTmpl,
			"security_advisories_url": base + "/security-advisories",
			"_links": map[string]any{
				"timeline": map[string]string{
					"href": timeline,
					"type": "application/atom+xml",
				},
				"user": map[string]string{
					"href": userTmpl,
					"type": "application/atom+xml",
				},
			},
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// markdownRequest is the POST /markdown body. mode is "markdown" (default) or
// "gfm"; context is an owner/repo the gfm mode resolves references against.
type markdownRequest struct {
	Text    string `json:"text"`
	Mode    string `json:"mode"`
	Context string `json:"context"`
}

// handleMarkdownRender serves POST /markdown: render the JSON body's text to
// HTML. The gfm mode runs the full comment pipeline; the default markdown mode
// renders in plain mode. The body is text/html, matching github.com.
func handleMarkdownRender(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		var req markdownRequest
		if !decodeJSONOpt(c, &req) {
			return nil
		}
		mode := markup.ModePlain
		if req.Mode == "gfm" {
			mode = markup.ModeComment
		}
		html := renderMarkdown(c.Context(), d.Markup, []byte(req.Text), mode)
		writeHTML(c.Writer(), http.StatusOK, html)
		return nil
	}
}

// handleMarkdownRaw serves POST /markdown/raw: the request body is the raw
// markdown (Content-Type text/plain or text/x-markdown), rendered in plain mode.
func handleMarkdownRaw(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		src, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return err
		}
		html := renderMarkdown(c.Context(), d.Markup, src, markup.ModePlain)
		writeHTML(c.Writer(), http.StatusOK, html)
		return nil
	}
}

// renderMarkdown runs the shared renderer with no Resolve closure: references do
// not link (the API caller supplies no viewer), but the GFM extensions, emoji,
// and sanitizer all run, so the HTML matches what a comment body would produce
// minus the live links.
func renderMarkdown(ctx context.Context, r *markup.Renderer, src []byte, mode markup.RenderMode) string {
	out, err := r.Render(ctx, src, markup.RenderContext{Mode: mode})
	if err != nil {
		return ""
	}
	return string(out)
}

// mountMiscData registers the static-data and markdown families. The static
// endpoints need no dependency and always mount; the markdown endpoints need the
// shared renderer and mount only when it is present.
func mountMiscData(r *mizu.Router, d Deps) {
	r.Get("/zen", handleZen)
	r.Get("/octocat", handleOctocat)
	r.Get("/licenses", handleLicensesList(d))
	r.Get("/licenses/{key}", handleLicenseGet(d))
	r.Get("/emojis", handleEmojis)
	r.Get("/gitignore/templates", handleGitignoreTemplates)
	r.Get("/gitignore/templates/{name}", handleGitignoreTemplate)
	r.Get("/feeds", handleFeeds(d))
	if d.Markup != nil {
		r.Post("/markdown", handleMarkdownRender(d))
		r.Post("/markdown/raw", handleMarkdownRaw(d))
	}
}

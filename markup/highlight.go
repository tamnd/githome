package markup

import (
	"html"
	"html/template"
	"strings"
)

// highlight.go is the second sanctioned producer of trusted HTML: it emits source
// text HTML-escaped, wrapped in pl-* token spans, per line. Only our spans are
// raw; the token text is escaped, so the output is controlled markup even though
// it is not run through the bluemonday allowlist (implementation/10 section 6.1).
//
// F2 ships the pure-Go chroma backend (highlight_chroma.go), which is the
// CGO_ENABLED=0 static binary Githome builds by default. The Tree-sitter backend
// and the tags-driven code navigation it powers are deferred behind the same
// interface (see codenav.go and the as-built note in implementation/10): the
// interface and the pl-* vocabulary are in place so the cgo backend slots in
// without touching callers.

// highlighter is the build-agnostic backend behind Highlight. ok=false means the
// language is unknown or unsupported and the caller should fall back to escaped
// text with no spans.
type highlighter interface {
	highlight(code []byte, lang string) ([]template.HTML, bool)
	name() string
}

// Highlight highlights code in the given language and returns its HTML: the source
// text HTML-escaped, with highlighted ranges wrapped in <span class="pl-..">. An
// unknown or unsupported language, or a blob over Config.MaxHighlightBytes,
// returns the escaped text with no spans (logged), never an error and never a
// failed page. The error return is reserved for a future backend that can fail
// internally; the current path never populates it.
func (r *Renderer) Highlight(code []byte, lang string) (template.HTML, error) {
	lines, _ := r.highlightLines(code, lang)
	return template.HTML(strings.Join(htmlStrings(lines), "\n")), nil // nolint:gosec // token text is escaped, only our pl-* spans are raw
}

// HighlightLines returns the same content split per line, for the blob and diff
// table builders that pair each line with a gutter cell and a stable id.
func (r *Renderer) HighlightLines(code []byte, lang string) ([]template.HTML, error) {
	lines, _ := r.highlightLines(code, lang)
	return lines, nil
}

// highlightLines is the shared worker. It enforces the size cap (logged, not
// silent), then delegates to the backend, falling back to escaped per-line text
// when the backend cannot handle the language.
func (r *Renderer) highlightLines(code []byte, lang string) ([]template.HTML, bool) {
	if r.maxHL > 0 && len(code) > r.maxHL {
		r.log.Info("blob too large to highlight", "bytes", len(code), "lang", lang)
		return escapeLines(code), false
	}
	if r.hl != nil {
		if lines, ok := r.hl.highlight(code, lang); ok {
			return lines, true
		}
	}
	return escapeLines(code), false
}

// escapeLines splits code into HTML-escaped per-line fragments with no spans, the
// monochrome-but-readable fallback.
func escapeLines(code []byte) []template.HTML {
	raw := strings.Split(string(code), "\n")
	// A trailing newline yields a final empty element; drop it so the blob's line
	// count matches the file's, the way github.com counts lines.
	if n := len(raw); n > 1 && raw[n-1] == "" {
		raw = raw[:n-1]
	}
	out := make([]template.HTML, len(raw))
	for i, ln := range raw {
		out[i] = template.HTML(html.EscapeString(ln)) // nolint:gosec // fully escaped
	}
	return out
}

func htmlStrings(in []template.HTML) []string {
	out := make([]string, len(in))
	for i, h := range in {
		out[i] = string(h)
	}
	return out
}

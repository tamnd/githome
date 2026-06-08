package repo

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/git"
)

// Blob renders a single file at a ref: GET /{owner}/{repo}/blob/{rest}. A tail
// that resolves to a directory 302-redirects to /tree (the inverse auto-correct).
// The blob is classified by content and extension into a kind that selects the
// renderer: text shows highlighted (escaped in F1) lines with stable anchors, an
// image or PDF embeds, a binary shows a blankslate. A blob past the size ceiling
// renders the too-large notice and the handler logs it. See implementation/07
// section 5.
func (h *Handlers) Blob(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	ref, path, ok := h.resolveRef(repo, c.Param("rest"))
	if !ok {
		return h.notFound(c)
	}

	res, err := h.repos.Contents(repo, path, ref)
	switch {
	case errors.Is(err, domain.ErrBlobTooLarge):
		return h.renderTooLarge(c, repo, ref, path)
	case errors.Is(err, domain.ErrGitNotFound), errors.Is(err, domain.ErrEmptyRepo):
		return h.notFound(c)
	case err != nil:
		return err
	}
	if res.IsDir {
		return c.Redirect(http.StatusFound, route.Tree(ownerLogin(repo), repo.Name, ref, path))
	}

	vm := h.buildBlob(ctx, repo, ref, path, res.Entry, res.File, c.Query("plain") == "1")
	vm.Chrome = h.chrome(c, blobTitle(repo, path))
	return h.render.Page(c, "repo/blob", vm)
}

// renderTooLarge renders the too-large blob notice. The content was never read,
// so the view carries only the raw URL and the View raw path; the handler logs
// the event so an operator sees the cap was hit rather than it failing silently
// (implementation/07 section 5.3).
func (h *Handlers) renderTooLarge(c *mizu.Ctx, repo *domain.Repo, ref, path string) error {
	h.log.WarnContext(c.Context(), "blob too large to render in the web view",
		"repo", repo.FullName(), "ref", ref, "path", path)
	vm := view.BlobVM{
		Header:    h.header(repo, "code"),
		Nav:       h.nav(repo, ref),
		Repo:      repoRef(repo),
		Ref:       view.Ref{Name: ref},
		Path:      path,
		Crumbs:    breadcrumbs(repo, ref, path, true),
		RefPicker: h.refPicker(repo, ref, route.KindBlob, parentDir(path)),
		Name:      baseName(path),
		Kind:      "toolarge",
		Truncated: true,
		RawURL:    route.Raw(ownerLogin(repo), repo.Name, ref, path),
		Chrome:    h.chrome(c, blobTitle(repo, path)),
	}
	return h.render.Page(c, "repo/blob", vm)
}

// buildBlob classifies a blob and builds its view model. The classification reads
// the extension first (the unambiguous kinds: image, pdf, svg) and falls back to
// a content sniff (a NUL byte or invalid UTF-8 marks a binary). A markdown blob
// viewed without ?plain=1 renders as GFM; a text or svg blob is highlighted per
// line; the other kinds carry just the size and the raw URL.
func (h *Handlers) buildBlob(ctx context.Context, repo *domain.Repo, ref, path string, entry git.PathEntry, blob *git.Blob, plain bool) view.BlobVM {
	content := blob.Content
	size := entry.Size
	if size == 0 {
		size = int64(len(content))
	}
	grammar := languageForName(baseName(path))
	vm := view.BlobVM{
		Header:    h.header(repo, "code"),
		Nav:       h.nav(repo, ref),
		Repo:      repoRef(repo),
		Ref:       view.Ref{Name: ref, IsDefault: ref == repo.DefaultBranch},
		Path:      path,
		Crumbs:    breadcrumbs(repo, ref, path, true),
		RefPicker: h.refPicker(repo, ref, route.KindBlob, parentDir(path)),
		Name:      baseName(path),
		Size:      size,
		SizeLabel: humanizeBytes(size),
		RawURL:    route.Raw(ownerLogin(repo), repo.Name, ref, path),
		Plain:     plain,
		Lang:      grammar,
	}
	vm.Kind = classifyBlob(path, content)
	switch {
	case vm.Kind == "text" && !plain && h.markup != nil && isMarkdownName(baseName(path)):
		// A markdown file renders to GFM by default; the Raw text toggle (?plain=1)
		// drops back to the highlighted source path below.
		vm.Kind = "markdown"
		vm.RawText = string(content)
		vm.Body = h.markup.RenderFile(ctx, h.markupRepo(repo), ref, path, string(content))
	case vm.Kind == "text" || vm.Kind == "svg":
		vm.RawText = string(content)
		vm.Lines = h.highlightLines(content, grammar)
		vm.LineCount = len(vm.Lines)
	}
	return vm
}

// highlightLines turns blob content into the per-line highlighted HTML the blob
// table renders, pairing each line with its 1-based number. It delegates to the
// shared markup highlighter (the source text is escaped, only the pl-* spans are
// raw); with markup unconfigured, or for an unknown grammar, it falls back to the
// escaped line with no spans so the bytes are always shown, never interpreted.
func (h *Handlers) highlightLines(content []byte, grammar string) []view.BlobLine {
	if len(content) == 0 {
		return nil
	}
	var html []template.HTML
	if h.markup != nil {
		html, _ = h.markup.HighlightLines(content, grammar)
	} else {
		html = escapeLines(content)
	}
	lines := make([]view.BlobLine, len(html))
	for i, line := range html {
		lines[i] = view.BlobLine{Number: i + 1, Text: line}
	}
	return lines
}

// escapeLines is the no-markup fallback: it splits content into HTML-escaped
// per-line fragments with no spans, dropping a single trailing newline so the
// line count matches the file. It mirrors the markup package's own fallback so
// the blob view renders identically whether or not markup is configured.
func escapeLines(content []byte) []template.HTML {
	text := strings.TrimSuffix(string(content), "\n")
	raw := strings.Split(text, "\n")
	out := make([]template.HTML, len(raw))
	for i, l := range raw {
		out[i] = template.HTML(template.HTMLEscapeString(strings.TrimSuffix(l, "\r"))) // nolint:gosec // fully escaped
	}
	return out
}

// classifyBlob returns the blob kind: image, pdf, and svg are decided by
// extension; everything else is text unless the bytes look binary.
func classifyBlob(path string, content []byte) string {
	switch ext(path) {
	case "png", "jpg", "jpeg", "gif", "webp", "ico", "bmp", "avif":
		return "image"
	case "pdf":
		return "pdf"
	case "svg":
		return "svg"
	}
	if isBinary(content) {
		return "binary"
	}
	return "text"
}

// isBinary reports whether content looks like a binary file: an embedded NUL in
// the first sniff window, or bytes that are not valid UTF-8. An empty file is
// text.
func isBinary(content []byte) bool {
	const window = 8000
	head := content
	if len(head) > window {
		head = head[:window]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return true
	}
	return len(head) > 0 && !utf8.Valid(head)
}

// ext returns the lowercased file extension without the dot, or "" when none.
func ext(path string) string {
	base := baseName(path)
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		return strings.ToLower(base[i+1:])
	}
	return ""
}

// baseName returns the final path segment.
func baseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// parentDir returns the directory holding a path, or "" at the root. The ref
// picker uses it so switching refs from a blob lands on the same directory.
func parentDir(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[:i]
	}
	return ""
}

// blobTitle is the browser title for a blob page.
func blobTitle(repo *domain.Repo, path string) string {
	return baseName(path) + " · " + repo.FullName()
}

// languageForName maps a file name to a highlighter grammar label. F1 records the
// label so the markup milestone can highlight without re-deriving it; the blob
// view renders escaped text until then.
func languageForName(name string) string {
	switch ext(name) {
	case "go":
		return "go"
	case "js", "mjs", "cjs":
		return "javascript"
	case "ts":
		return "typescript"
	case "py":
		return "python"
	case "rb":
		return "ruby"
	case "rs":
		return "rust"
	case "c", "h":
		return "c"
	case "cc", "cpp", "cxx", "hpp":
		return "cpp"
	case "java":
		return "java"
	case "json":
		return "json"
	case "yml", "yaml":
		return "yaml"
	case "md", "markdown":
		return "markdown"
	case "sh", "bash":
		return "shell"
	case "html":
		return "html"
	case "css":
		return "css"
	default:
		return ""
	}
}

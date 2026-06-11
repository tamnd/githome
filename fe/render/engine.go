// Package render is the Githome web front's html/template engine. It parses the
// template set, exposes Page and Fragment with the htmx fragment contract, owns
// the asset manifest and the icon-backed FuncMap, and renders the error pages. It
// holds no domain logic and imports no domain package: it renders a view model
// handed to it. Its only non-stdlib imports are fe/assets (the icon registry and
// the built asset tree) and, from later milestones, the module-root markup
// renderer. See implementation/03 and the import boundary in implementation/01.
package render

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/go-mizu/mizu"
)

//go:embed templates
var templatesFS embed.FS

// Set is the parsed template registry plus the asset manifest. One Set is built
// at boot and shared across requests. In the fedev build it re-parses the
// templates and re-reads the manifest per render so an edit shows on reload.
type Set struct {
	mu    sync.RWMutex
	pages map[string]*template.Template // logical page name -> cloned set with its content

	assetFS fs.FS // the built asset tree (assets.FS()): manifest + bytes
	dev     bool

	manMu sync.Mutex
	man   map[string]string // logical asset name -> hashed file name
}

// New builds the Set. assetFS is the asset tree (assets.FS()); dev toggles the
// re-parse-per-request path. It returns an error rather than panicking so the
// caller decides how a malformed template at boot is surfaced; cmd/githome treats
// it as fatal.
func New(assetFS fs.FS, dev bool) (*Set, error) {
	s := &Set{assetFS: assetFS, dev: dev}
	if err := s.loadManifest(); err != nil {
		return nil, err
	}
	if err := s.parse(); err != nil {
		return nil, err
	}
	return s, nil
}

// templatesDir returns the FS the templates parse from: the embedded tree in the
// production build, the on-disk tree in the fedev build (WEB_DEV_TEMPLATES_DIR).
func (s *Set) templatesDir() (fs.FS, string) {
	if s.dev {
		dir := os.Getenv("WEB_DEV_TEMPLATES_DIR")
		if dir == "" {
			dir = "fe/render/templates"
		}
		return os.DirFS(dir), "."
	}
	return templatesFS, "templates"
}

// parse builds the base set (layouts + partials) and then clones it once per page
// file, parsing that page into the clone so each page owns its own "content"
// template while sharing every layout and partial. A parse error fails the build.
func (s *Set) parse() error {
	fsys, root := s.templatesDir()

	base := template.New("root").Funcs(s.funcMap())
	shared, err := globUnder(fsys, root, "layouts", "partials")
	if err != nil {
		return err
	}
	if len(shared) > 0 {
		if base, err = base.ParseFS(fsys, shared...); err != nil {
			return fmt.Errorf("parse shared templates: %w", err)
		}
	}

	pageFiles, err := pagePaths(fsys, root)
	if err != nil {
		return err
	}
	pages := make(map[string]*template.Template, len(pageFiles))
	for _, pf := range pageFiles {
		clone, err := base.Clone()
		if err != nil {
			return err
		}
		if _, err := clone.ParseFS(fsys, pf.path); err != nil {
			return fmt.Errorf("parse page %s: %w", pf.name, err)
		}
		pages[pf.name] = clone
	}

	s.mu.Lock()
	s.pages = pages
	s.mu.Unlock()
	return nil
}

type pageFile struct {
	name string // logical name, e.g. "home/index"
	path string // path within the templates FS
}

// pagePaths lists every *.html under root that is not a layout or partial.
func pagePaths(fsys fs.FS, root string) ([]pageFile, error) {
	var out []pageFile
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".html") {
			return nil
		}
		rel := strings.TrimPrefix(p, root+"/")
		if strings.HasPrefix(rel, "layouts/") || strings.HasPrefix(rel, "partials/") {
			return nil
		}
		out = append(out, pageFile{name: strings.TrimSuffix(rel, ".html"), path: p})
		return nil
	})
	return out, err
}

// globUnder returns the file paths under each of dirs (relative to root).
func globUnder(fsys fs.FS, root string, dirs ...string) ([]string, error) {
	var out []string
	for _, dir := range dirs {
		matches, err := fs.Glob(fsys, path.Join(root, dir, "*.html"))
		if err != nil {
			return nil, err
		}
		out = append(out, matches...)
	}
	return out, nil
}

// Page renders a full page, or a fragment if the request is an htmx swap. name is
// the logical page name ("home/index"). It buffers before writing the status so a
// template error becomes a clean 500 rather than a half-written 200.
//
// Every full page carries a strong ETag over the rendered bytes (spec 03
// section 6: the page is rendered for one viewer and color mode, both of which
// land in the bytes, so the hash subsumes them) and Cache-Control: private,
// no-cache, the revalidate-every-time policy. A back-button or repeat visit
// whose If-None-Match still matches gets a 304 and ships no body. The render
// happens either way; what the ETag saves is the transfer, not the template.
func (s *Set) Page(c *mizu.Ctx, name string, vm any) error {
	if IsFragment(c) {
		return s.Fragment(c, name, vm)
	}
	var buf bytes.Buffer
	if err := s.execTemplate(&buf, name, "base", vm); err != nil {
		return s.ServerError(c, err)
	}
	h := c.Header()
	h.Add("Vary", "Cookie")
	h.Add("Vary", "HX-Request")
	etag := etagFor(buf.Bytes())
	h.Set("ETag", etag)
	h.Set("Cache-Control", "private, no-cache")
	if r := c.Request(); (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		etagMatches(r.Header.Get("If-None-Match"), etag) {
		// Header only: a 304 forbids a body, and net/http rejects any Write
		// after it, so this cannot go through Bytes.
		c.Writer().WriteHeader(http.StatusNotModified)
		return nil
	}
	return c.Bytes(http.StatusOK, buf.Bytes(), "text/html; charset=utf-8")
}

// etagFor is the strong validator for a rendered page: a quoted hash of the
// exact bytes, so any change to the page (content, viewer, theme) changes it.
func etagFor(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// etagMatches implements the If-None-Match check: a comma-separated list of
// entity tags, each possibly weak-prefixed, or the * wildcard. Comparison is
// weak (the W/ prefix is ignored) per RFC 9110 section 13.1.2, which is the
// right strength for deciding whether to resend a body.
func etagMatches(header, etag string) bool {
	if header == "" {
		return false
	}
	for part := range strings.SplitSeq(header, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "W/")
		if part == "*" || part == etag {
			return true
		}
	}
	return false
}

// Fragment renders one page's content template standalone (no layout) for an htmx
// swap. The same content template backs both Page and Fragment, so a fragment is
// never a second template.
func (s *Set) Fragment(c *mizu.Ctx, name string, vm any) error {
	var buf bytes.Buffer
	if err := s.execTemplate(&buf, name, "content", vm); err != nil {
		return s.ServerError(c, err)
	}
	c.Header().Add("Vary", "HX-Request")
	return c.Bytes(http.StatusOK, buf.Bytes(), "text/html; charset=utf-8")
}

// execTemplate looks up the page set and executes one of its named templates
// ("base" for a full page, "content" for a fragment). In dev it re-parses first.
func (s *Set) execTemplate(w io.Writer, page, entry string, vm any) error {
	if s.dev {
		if err := s.parse(); err != nil {
			return err
		}
	}
	s.mu.RLock()
	t, ok := s.pages[page]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("render: unknown page %q", page)
	}
	return t.ExecuteTemplate(w, entry, vm)
}

// IsFragment reports whether to skip the layout, in the precedence htmx header,
// then an explicit ?_fragment=1. A request lacking both renders the full page, so
// every fragment endpoint also serves a sensible full page on direct navigation
// (the no-JS fallback). See implementation/03 section 3.
func IsFragment(c *mizu.Ctx) bool {
	if c.Request().Header.Get("HX-Request") == "true" {
		return true
	}
	return c.Query("_fragment") == "1"
}

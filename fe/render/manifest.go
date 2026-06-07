package render

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/go-mizu/mizu"
)

// manifest.json maps a logical asset name (app.css) to its content-hashed file
// name (app.<hash>.css). It is produced by the asset build and read from the same
// asset tree the bytes live in, so the name a template resolves and the file the
// handler serves always agree. See implementation/01 section 4 and
// implementation/04 section 9.
const manifestName = "manifest.json"

// loadManifest reads and caches the manifest. In the production build it is read
// once at boot; in the fedev build asset returns to disk per call, so a rebuild
// shows up without a restart.
func (s *Set) loadManifest() error {
	b, err := fs.ReadFile(s.assetFS, manifestName)
	if err != nil {
		return fmt.Errorf("render: read asset manifest: %w", err)
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("render: parse asset manifest: %w", err)
	}
	s.manMu.Lock()
	s.man = m
	s.manMu.Unlock()
	return nil
}

// AssetURLPrefix is the public path the hashed asset tree is served under.
// "assets" is a reserved top-level name (2005/02 section 2.3), so the static
// prefix wins over the dynamic "/{owner}" namespace by ServeMux specificity and
// no owner login can shadow it. The route builder and the mount both read this
// one constant so the served path and the generated URLs cannot drift.
const AssetURLPrefix = "/assets/"

// asset resolves a logical asset name to its public URL under AssetURLPrefix. An
// unknown name returns a path that 404s rather than a silent empty string, so a
// missing bundle is loud. In dev it re-reads the manifest first.
func (s *Set) asset(logical string) string {
	if s.dev {
		if err := s.loadManifest(); err != nil {
			return AssetURLPrefix + logical + "?manifest-error"
		}
	}
	s.manMu.Lock()
	hashed, ok := s.man[logical]
	s.manMu.Unlock()
	if !ok {
		return AssetURLPrefix + logical + "?missing"
	}
	return AssetURLPrefix + hashed
}

// AssetHandler serves files from the asset tree under AssetURLPrefix. Hashed
// files are immutable, so the production build sends a far-future cache header;
// the dev build sends no-cache so an edit is always picked up. The handler never
// serves the manifest itself or escapes the asset root.
func (s *Set) AssetHandler() mizu.Handler {
	return func(c *mizu.Ctx) error {
		name := c.Param("file")
		name = strings.TrimPrefix(name, "/")
		if name == "" || name == manifestName || strings.Contains(name, "..") {
			return s.NotFound(c)
		}
		clean := path.Clean(name)
		b, err := fs.ReadFile(s.assetFS, clean)
		if err != nil {
			return s.NotFound(c)
		}
		if s.dev {
			c.Header().Set("Cache-Control", "no-cache")
		} else {
			c.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		return c.Bytes(http.StatusOK, b, contentTypeFor(clean))
	}
}

// contentTypeFor returns the MIME type for the few asset extensions the front
// ships. Anything else falls back to octet-stream rather than guessing.
func contentTypeFor(name string) string {
	switch {
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".woff2"):
		return "font/woff2"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".ico"):
		return "image/x-icon"
	default:
		return "application/octet-stream"
	}
}

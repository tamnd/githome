// The fedev build reads assets from disk instead of the embedded tree, so a
// rebuild (make assets) is reflected on the next request with no recompile. The
// directory is WEB_DEV_ASSETS_DIR, defaulting to the in-tree dist path. Build
// with `-tags fedev`. See implementation/01 section 4.
//
//go:build fedev

package assets

import (
	"io/fs"
	"os"
)

// FS returns the on-disk dist tree. WEB_DEV_ASSETS_DIR overrides the location;
// the default assumes the server runs from the module root.
func FS() fs.FS {
	dir := os.Getenv("WEB_DEV_ASSETS_DIR")
	if dir == "" {
		dir = "fe/assets/dist"
	}
	return os.DirFS(dir)
}

// Dev reports that assets are read from disk, so the render layer re-reads the
// manifest per request and does not mark responses immutable.
func Dev() bool { return true }

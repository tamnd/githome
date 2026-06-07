// Package assets owns the Githome web front's built static assets: the
// content-hashed CSS and JS bundles, the manifest that maps a logical name to
// its hashed file, and the Octicon-equivalent icon registry the render layer
// inlines. The bundles are produced by the build command in ./build and embedded
// here; nothing in this package imports the web front or the persistence layer.
// See implementation/01 and implementation/04.
//
//go:build !fedev

package assets

import (
	"embed"
	"io/fs"
)

//go:embed dist
var distFS embed.FS

// FS returns the built asset tree rooted at dist, so a path like
// "app.<hash>.css" resolves directly. In the production build the tree is the
// embedded one; the fedev build reads from disk instead.
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// dist is a compile-time embed, so this cannot fail in a built binary.
		panic(err)
	}
	return sub
}

// Dev reports whether assets are read from disk (the fedev build). In the
// production build it is always false, so the render layer caches the manifest
// once and serves immutable, far-future-cached asset responses.
func Dev() bool { return false }

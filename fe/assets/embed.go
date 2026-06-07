//go:build !fedev

// This file holds the production asset access: the built tree is embedded into
// the binary and served immutable. The fedev build (embed_dev.go) reads the same
// tree from disk instead. The package doc lives in icons.go, the only
// non-build-tagged file. See implementation/01 and implementation/04.

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

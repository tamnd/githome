// Package route holds the Githome web front's URL-space rules: the reserved
// top-level names that a user or organization login may not take, and the
// ref-versus-path split that the tree and blob URLs need. These are pure
// functions with no dependency on the router or the domain, so the route
// fidelity oracle tests them directly. See implementation/02 and implementation/07.
package route

import "strings"

// reservedTop is the set of first path segments the web front owns, so they can
// never be mistaken for a "/{owner}" profile. A registration that would collide
// with one of these is a bug; the dispatcher checks membership before treating a
// segment as a login. The list is the union of the front's own pages and the
// well-known files a browser or crawler requests at the root.
var reservedTop = map[string]bool{
	// Authentication and account.
	"login":    true,
	"logout":   true,
	"join":     true,
	"sessions": true,
	"settings": true,
	"account":  true,

	// Global pages.
	"notifications": true,
	"search":        true,
	"new":           true,
	"explore":       true,
	"dashboard":     true,
	"organizations": true,
	"orgs":          true,
	"about":         true,

	// The site-administration surface. Reserved now so an instance admin panel
	// can mount under them later without a login ever shadowing the route;
	// Githome does not back them yet, so nothing is mounted there.
	"stafftools": true,
	"admin":      true,

	// Cross-cutting resource roots that are not a single owner.
	"issues": true,
	"pulls":  true,
	"gist":   true,
	"gists":  true,
	"apps":   true,

	// The front's own static surface.
	"assets": true,
	"static": true,

	// Well-known files requested at the site root.
	"favicon.ico":          true,
	"robots.txt":           true,
	"sitemap.xml":          true,
	"manifest.json":        true,
	"sw.js":                true,
	"apple-touch-icon.png": true,
	"health":               true,
	"healthz":              true,
}

// IsReservedTop reports whether name is a reserved first path segment. The check
// is case-insensitive because logins are compared case-insensitively, so a
// reserved name cannot be taken by changing case.
func IsReservedTop(name string) bool {
	return reservedTop[strings.ToLower(name)]
}

// ReservedTopNames returns the reserved names in no guaranteed order, for tests
// and for an admin tool that validates a requested login.
func ReservedTopNames() []string {
	out := make([]string, 0, len(reservedTop))
	for name := range reservedTop {
		out = append(out, name)
	}
	return out
}

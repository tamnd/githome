package graphql

// This file carries the per-request node-ID format plumbing. GitHub lets a
// client pick between the legacy and the new global ID encoding per request
// with the X-Github-Next-Global-ID header; the middleware here reads the
// header once and stores the choice on the request context, and the resolvers
// read it back through Resolver.format.

import (
	"context"
	"net/http"

	"github.com/tamnd/githome/nodeid"
)

// formatKey is the context key the per-request node-ID format is stored under.
type formatKey struct{}

// idFormatMiddleware reads the X-Github-Next-Global-ID header and stores the
// requested node-ID format on the request context. "0" asks for the legacy
// base64 encoding, "1" for the new prefixed encoding; any other value,
// including an absent header, keeps the server default.
func idFormatMiddleware(def nodeid.Format, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		format := def
		switch r.Header.Get("X-Github-Next-Global-ID") {
		case "0":
			format = nodeid.FormatLegacy
		case "1":
			format = nodeid.FormatNew
		}
		ctx := context.WithValue(r.Context(), formatKey{}, format)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// idFormat returns the node-ID format stored on ctx, or fallback when the
// middleware was not installed (handlers built directly in tests).
func idFormat(ctx context.Context, fallback nodeid.Format) nodeid.Format {
	if f, ok := ctx.Value(formatKey{}).(nodeid.Format); ok {
		return f
	}
	return fallback
}

// format is the node-ID format for this request: the header-selected format
// when the middleware saw one, the resolver's configured default otherwise.
func (r *Resolver) format(ctx context.Context) nodeid.Format {
	return idFormat(ctx, r.NodeFormat)
}

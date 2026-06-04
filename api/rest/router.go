// Package rest implements Githome's REST API v3. It mounts onto a mizu router
// and is the only place HTTP request handling for the REST surface lives.
// Handlers call the domain layer for data and the presenter layer for
// rendering; they never touch the store or git directly.
package rest

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/config"
)

// Deps are the dependencies the REST surface needs to mount. Later milestones
// add the domain services, presenter, and authenticator; for now the surface
// needs config, a logger, and an optional readiness pinger.
type Deps struct {
	Config config.Config
	Logger *slog.Logger
	Ready  Pinger
}

// Mount wires the REST routes onto root. The API is served both at the
// GHES-style /api/v3 prefix and at the bare github.com-style root, sharing one
// set of handlers and the version/media-type middleware. Health probes sit
// outside that chain, and any unmatched path returns the GitHub-shaped 404.
func Mount(root *mizu.Router, d Deps) {
	// Drop mizu's default stderr request logger; Githome logs through its own
	// configured slog handler and error handler.
	root.ClearMiddleware()
	root.Use(requestID)
	root.ErrorHandler(errorHandler(d.Logger))

	root.Get("/healthz", handleHealthz)
	root.Get("/readyz", handleReadyz(d.Ready))

	api := root.With(apiVersion, mediaType)
	mountAPI(api.Prefix("/api/v3"), d)
	mountAPI(api, d)

	root.Compat.Handle("/", http.HandlerFunc(notFoundHandler))
}

// mountAPI registers the versioned API endpoints on r, which already carries the
// API middleware chain.
func mountAPI(r *mizu.Router, d Deps) {
	r.Get("/meta", handleMeta(d.Config))
	r.Get("/rate_limit", handleRateLimit(d.Config))
}

// errorHandler turns a handler-returned error or a recovered panic into the
// GitHub-shaped 500 envelope, logging it with the request id. Handlers write
// their own success and error responses and return nil; this fires only for the
// unexpected.
func errorHandler(log *slog.Logger) func(*mizu.Ctx, error) {
	return func(c *mizu.Ctx, err error) {
		if log != nil {
			attrs := []any{
				"request_id", c.Header().Get("X-GitHub-Request-Id"),
				"path", c.Request().URL.Path,
			}
			if panicErr, ok := errors.AsType[*mizu.PanicError](err); ok {
				log.ErrorContext(c.Context(), "panic recovered",
					append(attrs, "err", panicErr.Value, "stack", string(panicErr.Stack))...)
			} else {
				log.ErrorContext(c.Context(), "handler error", append(attrs, "err", err)...)
			}
		}
		writeError(c.Writer(), &apiError{Status: http.StatusInternalServerError, Message: "Server Error", DocURL: docRoot})
	}
}

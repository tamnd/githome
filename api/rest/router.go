// Package rest implements Githome's REST API v3. It mounts onto a chi router and
// is the only place HTTP request handling for the REST surface lives. Handlers
// call the domain layer for data and the presenter layer for rendering; they
// never touch the store or git directly.
package rest

import (
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/tamnd/githome/config"
)

// Deps are the dependencies the REST surface needs to mount. Later milestones
// add the domain services, presenter, and authenticator; M0 needs only config,
// a logger, and an optional readiness pinger.
type Deps struct {
	Config config.Config
	Logger *slog.Logger
	Ready  Pinger
}

// Mount wires the REST routes onto root. The API is served both at the GHES-style
// /api/v3 prefix and at the bare github.com-style root, sharing one set of
// handlers and middleware. Health probes sit outside the versioned chain.
func Mount(root chi.Router, d Deps) {
	root.Use(RequestID)
	root.Use(Recover(d.Logger))

	root.Get("/healthz", handleHealthz)
	root.Get("/readyz", handleReadyz(d.Ready))

	root.Route("/api/v3", func(r chi.Router) { mountAPI(r, d) })
	root.Group(func(r chi.Router) { mountAPI(r, d) })

	root.NotFound(notFoundHandler)
	root.MethodNotAllowed(methodNotAllowedHandler)
}

// mountAPI registers the versioned API endpoints and their middleware on r.
func mountAPI(r chi.Router, d Deps) {
	r.Use(APIVersion)
	r.Use(MediaType)
	r.Get("/meta", handleMeta(d.Config))
	r.Get("/rate_limit", handleRateLimit(d.Config))
}

package rest

import (
	"context"
	"net/http"
	"time"

	"github.com/tamnd/githome/config"
	"github.com/tamnd/githome/presenter/restmodel"
)

// Pinger is the readiness dependency: anything that can verify its backing
// store is reachable. *store.Store satisfies it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// handleMeta serves GET /meta. A self-hosted instance reports its own network
// ranges, which are empty by default; the arrays are always present (never null)
// to match GitHub's shape.
func handleMeta(_ config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		meta := restmodel.Meta{
			VerifiablePasswordAuthentication: true,
			SSHKeyFingerprints:               map[string]string{},
			SSHKeys:                          []string{},
			Hooks:                            []string{},
			Web:                              []string{},
			API:                              []string{},
			Git:                              []string{},
			Packages:                         []string{},
			Pages:                            []string{},
			Importer:                         []string{},
			Actions:                          []string{},
			Dependabot:                       []string{},
		}
		writeJSON(w, http.StatusOK, meta)
	}
}

// handleRateLimit serves GET /rate_limit. Querying it never consumes the core
// budget. With auth landing in M1, M0 reports the anonymous-equivalent full
// budget for every bucket.
func handleRateLimit(cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		reset := time.Now().Add(cfg.RateLimit.Window).Unix()
		bucket := func(limit int, resource string) restmodel.RateLimitBucket {
			return restmodel.RateLimitBucket{
				Limit:     limit,
				Remaining: limit,
				Reset:     reset,
				Used:      0,
				Resource:  resource,
			}
		}
		core := bucket(cfg.RateLimit.AuthedPerHour, "core")
		rl := restmodel.RateLimit{
			Resources: restmodel.RateLimitResources{
				Core:                core,
				Search:              bucket(cfg.RateLimit.SearchPerMin, "search"),
				GraphQL:             bucket(cfg.RateLimit.GraphQLPoints, "graphql"),
				IntegrationManifest: bucket(cfg.RateLimit.AuthedPerHour, "integration_manifest"),
				CodeScanningUpload:  bucket(500, "code_scanning_upload"),
				CodeSearch:          bucket(cfg.RateLimit.SearchPerMin, "code_search"),
			},
			Rate: core,
		}
		writeJSON(w, http.StatusOK, rl)
	}
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": config.Version})
}

func handleReadyz(p Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p == nil {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := p.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db_down"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

func notFoundHandler(w http.ResponseWriter, _ *http.Request) {
	writeError(w, errNotFound())
}

func methodNotAllowedHandler(w http.ResponseWriter, _ *http.Request) {
	writeError(w, &apiError{Status: http.StatusMethodNotAllowed, Message: "Method Not Allowed", DocURL: docRoot})
}

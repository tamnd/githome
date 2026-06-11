package rest

import (
	"context"
	"net/http"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
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
// to match GitHub's shape. InstalledVersion mirrors config.Version so gh and
// Renovate can detect the running build.
func handleMeta(_ config.Config) mizu.Handler {
	return func(c *mizu.Ctx) error {
		meta := restmodel.Meta{
			VerifiablePasswordAuthentication: true,
			InstalledVersion:                 config.Version,
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
		writeJSON(c.Writer(), http.StatusOK, meta)
		return nil
	}
}

// handleVersions serves GET /api/v3/versions. gh calls this first to
// distinguish a GHES/Githome host from github.com. An empty JSON array means
// "this is a GHES-compatible host"; github.com returns a non-empty version
// list. Returning [] is the correct signal for all self-hosted deployments.
func handleVersions(c *mizu.Ctx) error {
	writeJSON(c.Writer(), http.StatusOK, []string{})
	return nil
}

// handleRateLimit serves GET /rate_limit. Querying it never consumes the core
// budget. The core and search buckets read the same limiter that stamps the
// X-RateLimit-* headers, so the body and the headers always agree; the buckets
// Githome does not meter report their full configured budget.
func handleRateLimit(cfg config.Config, limiter *rateLimiter) mizu.Handler {
	return func(c *mizu.Ctx) error {
		r := c.Request()
		actor := auth.ActorFrom(r.Context())
		key := rateKeyFor(actor, r)
		authed := actor.IsAuthenticated()
		live := func(resource string) restmodel.RateLimitBucket {
			st := limiter.peek(key, resource, authed)
			return restmodel.RateLimitBucket{
				Limit:     st.limit,
				Remaining: st.remaining,
				Reset:     st.reset.Unix(),
				Used:      st.used,
				Resource:  resource,
			}
		}
		reset := time.Now().Add(cfg.RateLimit.Window).Unix()
		full := func(limit int, resource string) restmodel.RateLimitBucket {
			return restmodel.RateLimitBucket{
				Limit:     limit,
				Remaining: limit,
				Reset:     reset,
				Used:      0,
				Resource:  resource,
			}
		}
		core := live("core")
		rl := restmodel.RateLimit{
			Resources: restmodel.RateLimitResources{
				Core:                core,
				Search:              live("search"),
				GraphQL:             full(cfg.RateLimit.GraphQLPoints, "graphql"),
				IntegrationManifest: full(limiter.cfg.AuthedPerHour, "integration_manifest"),
				CodeScanningUpload:  full(500, "code_scanning_upload"),
				CodeSearch:          full(limiter.cfg.SearchPerMin, "code_search"),
			},
			Rate: core,
		}
		writeJSON(c.Writer(), http.StatusOK, rl)
		return nil
	}
}

func handleHealthz(c *mizu.Ctx) error {
	writeJSON(c.Writer(), http.StatusOK, map[string]string{"status": "ok", "version": config.Version})
	return nil
}

func handleReadyz(p Pinger) mizu.Handler {
	return func(c *mizu.Ctx) error {
		if p == nil {
			writeJSON(c.Writer(), http.StatusOK, map[string]string{"status": "ready"})
			return nil
		}
		ctx, cancel := context.WithTimeout(c.Context(), 2*time.Second)
		defer cancel()
		if err := p.Ping(ctx); err != nil {
			writeJSON(c.Writer(), http.StatusServiceUnavailable, map[string]string{"status": "db_down"})
			return nil
		}
		writeJSON(c.Writer(), http.StatusOK, map[string]string{"status": "ready"})
		return nil
	}
}

func notFoundHandler(w http.ResponseWriter, _ *http.Request) {
	writeError(w, errNotFound())
}

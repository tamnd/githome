package rest

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/config"
)

// anonSearchPerMin is the search budget for unauthenticated requests. GitHub
// gives anonymous callers 10 search requests a minute, a third of the
// authenticated budget, and Githome keeps the same ratio.
const anonSearchPerMin = 10

// rateLimitDocURL is the documentation_url for a rate-limited 403.
const rateLimitDocURL = docRoot + "/overview/rate-limits-for-the-rest-api"

// rateLimiter meters API requests per actor in fixed windows, the accounting
// behind the X-RateLimit-* headers and GET /rate_limit. Buckets live in memory
// and are keyed by the actor's rate key (or client IP for anonymous requests)
// plus the resource family, so an authenticated user, an installation, and an
// anonymous IP each spend their own budget.
type rateLimiter struct {
	cfg config.RateLimit

	mu      sync.Mutex
	buckets map[rateBucketKey]*rateWindow
}

type rateBucketKey struct {
	key      string
	resource string
}

// rateWindow is one fixed window: how much of the budget is spent and when the
// window rolls over. A window past its reset is replaced, never refilled.
type rateWindow struct {
	used  int
	reset time.Time
}

// rateStatus is a point-in-time view of one bucket, the values the headers and
// the /rate_limit body report.
type rateStatus struct {
	limit     int
	remaining int
	used      int
	reset     time.Time
	resource  string
}

func newRateLimiter(cfg config.RateLimit) *rateLimiter {
	// An embedder that assembles a config by hand can leave the budgets zero,
	// and a zero budget would refuse every request. Each missing value takes
	// the shipped default instead, the same numbers config.Load starts from.
	if cfg.AuthedPerHour <= 0 {
		cfg.AuthedPerHour = 5000
	}
	if cfg.AnonPerHour <= 0 {
		cfg.AnonPerHour = 60
	}
	if cfg.SearchPerMin <= 0 {
		cfg.SearchPerMin = 30
	}
	if cfg.Window <= 0 {
		cfg.Window = time.Hour
	}
	return &rateLimiter{cfg: cfg, buckets: map[rateBucketKey]*rateWindow{}}
}

// limitFor returns the budget and window length for one resource bucket.
func (l *rateLimiter) limitFor(resource string, authed bool) (int, time.Duration) {
	if resource == "search" {
		if authed {
			return l.cfg.SearchPerMin, time.Minute
		}
		return anonSearchPerMin, time.Minute
	}
	if authed {
		return l.cfg.AuthedPerHour, l.cfg.Window
	}
	return l.cfg.AnonPerHour, l.cfg.Window
}

// take charges one request against the bucket and reports the state after the
// charge. The boolean is false when the budget is already spent; an exhausted
// bucket is never charged past its limit, so used tops out at limit the way
// GitHub reports it.
func (l *rateLimiter) take(key, resource string, authed bool) (rateStatus, bool) {
	limit, window := l.limitFor(resource, authed)
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.window(key, resource, window)
	if w.used >= limit {
		return l.status(w, limit, resource), false
	}
	w.used++
	return l.status(w, limit, resource), true
}

// peek reports the bucket without charging it, the view GET /rate_limit serves.
func (l *rateLimiter) peek(key, resource string, authed bool) rateStatus {
	limit, window := l.limitFor(resource, authed)
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status(l.window(key, resource, window), limit, resource)
}

// window returns the live window for the bucket, starting a fresh one when none
// exists or the previous one has rolled over. Callers hold l.mu.
func (l *rateLimiter) window(key, resource string, window time.Duration) *rateWindow {
	k := rateBucketKey{key: key, resource: resource}
	w := l.buckets[k]
	if w == nil || !time.Now().Before(w.reset) {
		w = &rateWindow{reset: time.Now().Add(window)}
		l.buckets[k] = w
	}
	return w
}

func (l *rateLimiter) status(w *rateWindow, limit int, resource string) rateStatus {
	remaining := limit - w.used
	if remaining < 0 {
		remaining = 0
	}
	return rateStatus{limit: limit, remaining: remaining, used: w.used, reset: w.reset, resource: resource}
}

// stampRateHeaders writes the X-RateLimit-* response headers from one bucket
// status. Clients like @octokit/rest, go-github, Octokit.rb, and Octokit.NET
// inspect these headers and refuse to proceed without them.
func stampRateHeaders(h http.Header, st rateStatus) {
	h.Set("X-RateLimit-Limit", strconv.Itoa(st.limit))
	h.Set("X-RateLimit-Remaining", strconv.Itoa(st.remaining))
	h.Set("X-RateLimit-Used", strconv.Itoa(st.used))
	h.Set("X-RateLimit-Reset", strconv.FormatInt(st.reset.Unix(), 10))
	h.Set("X-RateLimit-Resource", st.resource)
}

// rateKeyFor names the bucket a request spends from: the actor's RateKey when a
// credential resolved, otherwise the client IP, so anonymous traffic from one
// host shares one budget.
func rateKeyFor(a *auth.Actor, r *http.Request) string {
	if a != nil && a.RateKey != "" {
		return a.RateKey
	}
	return "ip:" + clientIP(r)
}

// clientIP extracts the bare host from RemoteAddr, falling back to the raw
// value when it carries no port.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateResource maps a request path to the bucket family it spends from. Search
// endpoints have their own per-minute budget; everything else is core.
func rateResource(path string) string {
	if strings.HasPrefix(strippedAPIPath(path), "/search/") {
		return "search"
	}
	return "core"
}

// strippedAPIPath removes the GHES /api/v3 mount prefix so path checks see the
// same shape on both the prefixed and the bare-root mounts.
func strippedAPIPath(path string) string {
	if p, ok := strings.CutPrefix(path, "/api/v3"); ok && (p == "" || p[0] == '/') {
		return p
	}
	return path
}

// rateLimit meters every API request against the actor's bucket and stamps the
// X-RateLimit-* headers. It runs after the auth middleware so the charge lands
// on the resolved actor. GET /rate_limit is never charged, matching GitHub:
// the handler and these headers read the same buckets, so the numbers agree.
// An exhausted budget is a 403 with Retry-After, the primary-rate-limit shape
// GitHub sends.
func rateLimit(l *rateLimiter) mizu.Middleware {
	return func(next mizu.Handler) mizu.Handler {
		return func(c *mizu.Ctx) error {
			r := c.Request()
			actor := auth.ActorFrom(r.Context())
			key := rateKeyFor(actor, r)
			authed := actor.IsAuthenticated()
			if strippedAPIPath(r.URL.Path) == "/rate_limit" {
				stampRateHeaders(c.Header(), l.peek(key, "core", authed))
				return next(c)
			}
			st, ok := l.take(key, rateResource(r.URL.Path), authed)
			stampRateHeaders(c.Header(), st)
			if !ok {
				retry := int(time.Until(st.reset).Seconds()) + 1
				if retry < 1 {
					retry = 1
				}
				c.Header().Set("Retry-After", strconv.Itoa(retry))
				writeError(c.Writer(), &apiError{
					Status:  http.StatusForbidden,
					Message: rateLimitMessage(actor, r),
					DocURL:  rateLimitDocURL,
				})
				return nil
			}
			return next(c)
		}
	}
}

// rateLimitMessage builds the primary-rate-limit 403 message. GitHub names the
// spent budget's owner: the user id for a user credential, the client IP for
// anonymous traffic with a nudge toward authenticating.
func rateLimitMessage(a *auth.Actor, r *http.Request) string {
	switch {
	case a.IsUser():
		return fmt.Sprintf("API rate limit exceeded for user ID %d.", a.UserID)
	case a.IsAuthenticated():
		return "API rate limit exceeded."
	default:
		return "API rate limit exceeded for " + clientIP(r) +
			". (But here's the good news: Authenticated requests get a higher rate limit. Check out the documentation for more details.)"
	}
}

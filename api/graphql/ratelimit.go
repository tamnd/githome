package graphql

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/99designs/gqlgen/graphql"
)

// GitHub answers every GraphQL request with the X-RateLimit-* family so a client
// can read its remaining point budget without a separate rateLimit query, and it
// only serves the endpoint over POST. This file adds both: a per-request holder
// the response interceptor fills with the computed cost, an http layer that lifts
// that cost onto the response headers, and a POST-only gate that answers GET with
// 405 the way GitHub's GraphQL endpoint does.

// rateLimitResource is the value GitHub reports in X-RateLimit-Resource for the
// GraphQL endpoint; the REST surface reports "core".
const rateLimitResource = "graphql"

// rateLimitInfo carries the cost computed during execution out to the http layer
// so the X-RateLimit headers can be written before the body is flushed. It is
// stored on the request context by the header middleware and filled by the
// response interceptor.
type rateLimitInfo struct {
	used      int
	nodeCount int
	filled    bool
}

type rateLimitCtxKey struct{}

// withRateLimitInfo returns a context carrying a fresh rateLimitInfo holder and
// the holder itself, so the caller can read it back after execution.
func withRateLimitInfo(ctx context.Context) (context.Context, *rateLimitInfo) {
	info := &rateLimitInfo{}
	return context.WithValue(ctx, rateLimitCtxKey{}, info), info
}

// rateLimitInfoFrom returns the holder on ctx, or nil when none was installed.
func rateLimitInfoFrom(ctx context.Context) *rateLimitInfo {
	info, _ := ctx.Value(rateLimitCtxKey{}).(*rateLimitInfo)
	return info
}

// rateLimitInterceptor is a gqlgen AroundResponses hook that computes the
// operation's node count and point cost and records them on the request's
// rateLimitInfo holder. It runs while the transport still holds the response
// writer, so the http layer can turn the recorded cost into headers before the
// body is written. It never alters the response itself.
func rateLimitInterceptor(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	if info := rateLimitInfoFrom(ctx); info != nil {
		nodes, cost := queryCost(ctx)
		info.nodeCount = nodes
		info.used = cost
		info.filled = true
	}
	return next(ctx)
}

// resetAtTop returns the unix epoch second of the top of the next hour, the
// instant GitHub reports a fresh GraphQL budget. The hourly window is the unit
// GitHub resets on, so a client backing off until X-RateLimit-Reset waits no
// longer than one window.
func resetAtTop(now time.Time) int64 {
	return now.Truncate(time.Hour).Add(time.Hour).Unix()
}

// rateLimitHeaders wraps the GraphQL handler so the cost recorded during
// execution can be written as the X-RateLimit-* response headers GitHub sends.
// It only installs the per-request holder before execution; the headers
// themselves are written by liftErrorTypes (applyRateLimitHeaders), which is the
// layer that buffers the body and so still controls the header map at flush
// time. Splitting it this way keeps a single point that writes the real
// response head.
func rateLimitHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, _ := withRateLimitInfo(r.Context())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// applyRateLimitHeaders writes the X-RateLimit-* family onto h from the cost
// recorded on ctx during execution. A request that never reached execution
// (rejected before an operation context existed, or with no holder installed)
// reports the one-point minimum against the full budget, matching a cost-free
// call. It must be called before the response head is flushed.
func applyRateLimitHeaders(ctx context.Context, h http.Header) {
	used, nodes := 1, 0
	if info := rateLimitInfoFrom(ctx); info != nil && info.filled {
		used = info.used
		nodes = info.nodeCount
	}
	remaining := rateLimitBudget - used
	if remaining < 0 {
		remaining = 0
	}
	h.Set("X-RateLimit-Limit", strconv.Itoa(rateLimitBudget))
	h.Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	h.Set("X-RateLimit-Used", strconv.Itoa(used))
	h.Set("X-RateLimit-Reset", strconv.FormatInt(resetAtTop(time.Now()), 10))
	h.Set("X-RateLimit-Resource", rateLimitResource)
	// The node count a request consumed is not a rate-limit header on its own,
	// but exposing it keeps the headers and the rateLimit query in agreement for
	// a client that reads both.
	h.Set("X-RateLimit-NodeCount", strconv.Itoa(nodes))
}

// postOnly answers any method other than POST with 405 and an Allow: POST
// header, the way GitHub's GraphQL endpoint refuses GET. The route stays
// registered for GET (the web front's greedy GET /{owner}/{repo} pattern needs
// the GraphQL path to win on specificity), so the refusal happens here in the
// handler rather than by leaving the method unrouted.
func postOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte(`{"message":"This endpoint only supports POST requests.","documentation_url":"https://docs.github.com/graphql/guides/forming-calls-with-graphql"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

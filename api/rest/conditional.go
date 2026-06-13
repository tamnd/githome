package rest

import (
	"encoding/json"
	"net/http"

	"github.com/tamnd/githome/etag"
)

// conditionalJSON renders v as a JSON response carrying a weak ETag derived from
// the exact bytes it serves, and short-circuits to 304 Not Modified when the
// request's If-None-Match validator already covers that tag. A 304 carries the
// ETag but no body, the way GitHub answers a conditional GET of an unchanged
// resource. It is the read-path replacement for writeJSON: handlers set any
// Link or other headers first, then hand the value here so the validator check
// runs against the same representation the client would receive.
//
// GitHub does not spend rate-limit quota on a 304, so the 304 path refunds the
// unit the limiter charged this request before writing the response.
func conditionalJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeError(w, &apiError{Status: http.StatusInternalServerError, Message: "Server Error", DocURL: docRoot})
		return
	}
	tag := etag.Weak(body)
	w.Header().Set("ETag", tag)
	if etag.Match(r.Header.Get("If-None-Match"), tag) {
		refundRateConditional(w, r)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// notModified reports whether the request's If-None-Match validator already
// covers tag, writing the 304 (ETag, no body) when it does. List handlers call
// it with a version tag seeded from one cheap aggregate query, before fetching
// the page, assembling the presenter models, or marshaling anything, so the
// polling workload's 304s cost one query instead of the full render.
func notModified(w http.ResponseWriter, r *http.Request, tag string) bool {
	if !etag.Match(r.Header.Get("If-None-Match"), tag) {
		return false
	}
	w.Header().Set("ETag", tag)
	refundRateConditional(w, r)
	w.WriteHeader(http.StatusNotModified)
	return true
}

// conditionalVersioned renders v as a JSON response carrying the pre-computed
// version ETag tag, and short-circuits to 304 Not Modified without marshaling
// the body when the request's If-None-Match validator already covers that tag.
// It is used for resources with stable version markers (id, updated_at) so the
// 304 hot path pays only the cost of deriving the version key, not a full
// marshal. The tag must be derived from all fields that can change the body,
// typically via etag.Version.
func conditionalVersioned(w http.ResponseWriter, r *http.Request, status int, v any, tag string) {
	w.Header().Set("ETag", tag)
	if etag.Match(r.Header.Get("If-None-Match"), tag) {
		refundRateConditional(w, r)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body, err := json.Marshal(v)
	if err != nil {
		writeError(w, &apiError{Status: http.StatusInternalServerError, Message: "Server Error", DocURL: docRoot})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

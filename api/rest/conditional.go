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
// GitHub does not spend rate-limit quota on a 304. Githome does not meter
// per-request quota yet, so there is nothing to refund; when the limiter lands,
// the refund hangs here, before the body is skipped.
func conditionalJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeError(w, &apiError{Status: http.StatusInternalServerError, Message: "Server Error", DocURL: docRoot})
		return
	}
	tag := etag.Weak(body)
	w.Header().Set("ETag", tag)
	if etag.Match(r.Header.Get("If-None-Match"), tag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

package rest

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
)

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyAPIVersion
)

// defaultAPIVersion is the baseline dated REST API version, echoed when the
// client does not pin one. Matches GitHub's current default.
const defaultAPIVersion = "2022-11-28"

// supportedAPIVersions is the set of dated versions Githome serves. Unknown
// values are rejected with 400, matching GitHub.
var supportedAPIVersions = map[string]bool{
	"2022-11-28": true,
	"2026-03-10": true,
}

// RequestID assigns a GitHub-style request id, exposes it on the response as
// X-GitHub-Request-Id, and stores it in the context.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-GitHub-Request-Id", id)
		ctx := context.WithValue(r.Context(), ctxKeyRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext returns the request id assigned by RequestID, if any.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyRequestID).(string)
	return id
}

// Recover turns a panic in a downstream handler into a 500 error envelope and
// logs it with the request id, rather than dropping the connection.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				if log != nil {
					log.ErrorContext(r.Context(), "panic recovered",
						"err", rec,
						"stack", string(debug.Stack()),
						"request_id", RequestIDFromContext(r.Context()),
						"path", r.URL.Path)
				}
				writeError(w, &apiError{Status: http.StatusInternalServerError, Message: "Server Error", DocURL: docRoot})
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// APIVersion validates and echoes the X-GitHub-Api-Version header. An empty
// header takes the default; an unknown value is a 400.
func APIVersion(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := r.Header.Get("X-GitHub-Api-Version")
		if v == "" {
			v = defaultAPIVersion
		} else if !supportedAPIVersions[v] {
			w.Header().Set("X-GitHub-Api-Version", defaultAPIVersion)
			writeError(w, &apiError{
				Status:  http.StatusBadRequest,
				Message: fmt.Sprintf("The version specified for this request is invalid: %q", v),
				DocURL:  "https://docs.github.com/rest/about-the-rest-api/api-versions",
			})
			return
		}
		w.Header().Set("X-GitHub-Api-Version", v)
		ctx := context.WithValue(r.Context(), ctxKeyAPIVersion, v)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// MediaType advertises the served media type on X-GitHub-Media-Type. The full
// Accept negotiation (diff/patch/raw branches) arrives with the endpoints that
// need it; M0 always serves JSON.
func MediaType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-GitHub-Media-Type", "github.v3; format=json")
		next.ServeHTTP(w, r)
	})
}

// newRequestID returns a GitHub-style request id: five uppercase hex groups
// separated by colons.
func newRequestID() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read on a healthy system does not fail; fall back to a zero id.
		return "0000:0000:000000:000000:00000000"
	}
	g1 := binary.BigEndian.Uint16(b[0:2])
	g2 := binary.BigEndian.Uint16(b[2:4])
	g3 := binary.BigEndian.Uint16(b[4:6]) // widened below
	g4 := binary.BigEndian.Uint16(b[6:8])
	g5 := binary.BigEndian.Uint32(b[6:10])
	return fmt.Sprintf("%04X:%04X:%06X:%06X:%08X", g1, g2, g3, g4, g5)
}

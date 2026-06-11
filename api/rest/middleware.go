package rest

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/config"
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

// requestID assigns a GitHub-style request id and exposes it on the response as
// X-GitHub-Request-Id. It runs for every request, including health probes and
// the not-found fallback, so every response is traceable.
func requestID(next mizu.Handler) mizu.Handler {
	return func(c *mizu.Ctx) error {
		c.Header().Set("X-GitHub-Request-Id", newRequestID())
		return next(c)
	}
}

// apiVersion validates and echoes the X-GitHub-Api-Version header. An empty
// header takes the default; an unknown value is a 400.
func apiVersion(next mizu.Handler) mizu.Handler {
	return func(c *mizu.Ctx) error {
		v := c.Request().Header.Get("X-GitHub-Api-Version")
		switch {
		case v == "":
			v = defaultAPIVersion
		case !supportedAPIVersions[v]:
			c.Header().Set("X-GitHub-Api-Version", defaultAPIVersion)
			writeError(c.Writer(), &apiError{
				Status:  http.StatusBadRequest,
				Message: fmt.Sprintf("The version specified for this request is invalid: %q", v),
				DocURL:  "https://docs.github.com/rest/about-the-rest-api/api-versions",
			})
			return nil
		}
		c.Header().Set("X-GitHub-Api-Version", v)
		return next(c)
	}
}

// mediaType advertises the served media type on X-GitHub-Media-Type. The
// middleware stamps the JSON default; a handler that negotiates a raw
// representation from the Accept header overrides it through
// negotiatedMediaType before writing the body.
func mediaType(next mizu.Handler) mizu.Handler {
	return func(c *mizu.Ctx) error {
		c.Header().Set("X-GitHub-Media-Type", "github.v3; format=json")
		return next(c)
	}
}

// negotiatedMediaType replaces the default X-GitHub-Media-Type once a handler
// has negotiated a non-JSON representation, the way GitHub reports format=diff,
// format=patch, or format=raw for the vendor media types.
func negotiatedMediaType(w http.ResponseWriter, format string) {
	w.Header().Set("X-GitHub-Media-Type", "github.v3; format="+format)
}

// enterpriseVersion stamps every /api/v3/ response with the
// x-github-enterprise-version header. Renovate and other GHES-aware clients
// read this header to gate features against the server version.
func enterpriseVersion(next mizu.Handler) mizu.Handler {
	return func(c *mizu.Ctx) error {
		c.Header().Set("x-github-enterprise-version", config.Version)
		return next(c)
	}
}

// maxBody caps the request body of every API request at limit bytes by wrapping
// it in an http.MaxBytesReader. A handler that reads past the cap sees an
// *http.MaxBytesError, which decodeJSON maps to a 413. A zero or negative limit
// disables the cap. This guards the JSON surface only: the git smart-HTTP
// transport mounts on the root router outside this chain, so multi-gigabyte
// clone and push bodies are never wrapped.
func maxBody(limit int64) func(mizu.Handler) mizu.Handler {
	return func(next mizu.Handler) mizu.Handler {
		if limit <= 0 {
			return next
		}
		return func(c *mizu.Ctx) error {
			r := c.Request()
			if r.Body != nil {
				r.Body = http.MaxBytesReader(c.Writer(), r.Body, limit)
			}
			return next(c)
		}
	}
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
	g3 := binary.BigEndian.Uint16(b[4:6])
	g4 := binary.BigEndian.Uint16(b[6:8])
	g5 := binary.BigEndian.Uint32(b[6:10])
	return fmt.Sprintf("%04X:%04X:%06X:%06X:%08X", g1, g2, g3, g4, g5)
}

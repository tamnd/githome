package webmw

import (
	"github.com/go-mizu/mizu"
)

// SecureHeaders sets the security-related response headers the spec requires on
// every HTML page. It does not depend on any other middleware and may sit at any
// position in the chain. The Content-Security-Policy allows inline styles (needed
// by the color-mode data attributes) and disallows all inline scripts; external
// resources are restricted to same-origin and the CDN domain in the asset URL.
// The CSP is deliberately strict but avoids report-only mode since the front has
// no report collector. See implementation/14 section 4.
func SecureHeaders() mizu.Middleware {
	return func(next mizu.Handler) mizu.Handler {
		return func(c *mizu.Ctx) error {
			h := c.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			// CSP: same-origin default; unsafe-inline styles for the theme layer;
			// no inline or eval script; frame-ancestors none matches X-Frame-Options.
			h.Set("Content-Security-Policy",
				"default-src 'self'; "+
					"style-src 'self' 'unsafe-inline'; "+
					"img-src 'self' data:; "+
					"font-src 'self'; "+
					"frame-ancestors 'none'; "+
					"base-uri 'self'; "+
					"form-action 'self'",
			)
			return next(c)
		}
	}
}

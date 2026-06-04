package rest

import (
	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
)

// authMiddleware resolves the Authorization header into an auth.Actor, places it
// in the request context, and sets the scope response headers. It never aborts
// on a missing credential: anonymous flows through and individual handlers
// decide whether to demand authentication. It does abort with 401 when a
// credential is present but invalid, expired, or revoked, matching GitHub.
func authMiddleware(svc *auth.Service) mizu.Middleware {
	return func(next mizu.Handler) mizu.Handler {
		return func(c *mizu.Ctx) error {
			r := c.Request()
			actor, err := svc.Authenticate(r.Context(), r.Header.Get("Authorization"))
			if err != nil {
				setScopeHeaders(c, nil)
				writeError(c.Writer(), errBadCredentials())
				return nil
			}
			setScopeHeaders(c, actor)
			// mizu exposes no request-context setter, so update the request in
			// place; c holds the same *http.Request, so c.Request() downstream
			// sees the actor.
			*r = *r.WithContext(auth.WithActor(r.Context(), actor))
			return next(c)
		}
	}
}

// setScopeHeaders emits X-OAuth-Scopes (the scopes on the credential) and
// X-Accepted-OAuth-Scopes (the scopes the route checks). Both appear on every
// response, including anonymous and error cases, because gh and octokit parse
// them. M1 has no per-route accepted-scope table yet, so the accepted header is
// empty; later milestones populate it from the route metadata.
func setScopeHeaders(c *mizu.Ctx, a *auth.Actor) {
	var have string
	if a != nil && len(a.Scopes) > 0 {
		have = a.Scopes.Header()
	}
	c.Header().Set("X-OAuth-Scopes", have)
	c.Header().Set("X-Accepted-OAuth-Scopes", "")
}

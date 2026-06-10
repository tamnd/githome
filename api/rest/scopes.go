package rest

// scopes.go is the classic-scope gate for the REST surface. requireScope wraps
// a handler with the scopes its endpoint family accepts: the wrapper always
// advertises them in X-Accepted-OAuth-Scopes, and when the request rode in on
// a scoped user token it refuses with 403 unless the token carries at least
// one of them (parents count through the scope lattice, so admin:public_key
// satisfies a read:public_key gate). Anonymous requests and non-token actors
// pass through untouched: the handlers behind the gate still demand
// authentication themselves, and an installation token is bounded by its
// grant, not by classic scopes.

import (
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
)

// requireScope wraps h with the accepted classic scopes of its endpoint
// family. Accepted lists every scope GitHub's X-Accepted-OAuth-Scopes shows
// for the family, broadest first, e.g. ("repo", "public_repo") for repository
// writes.
func requireScope(h mizu.Handler, accepted ...auth.Scope) mizu.Handler {
	header := auth.Scopes(accepted).Header()
	return func(c *mizu.Ctx) error {
		c.Header().Set("X-Accepted-OAuth-Scopes", header)
		actor := auth.ActorFrom(c.Context())
		if actor.Kind != auth.KindUser || actor.TokenID == 0 {
			return h(c)
		}
		for _, sc := range accepted {
			if actor.Scopes.Has(sc) {
				return h(c)
			}
		}
		writeError(c.Writer(), errMissingScope(header))
		return nil
	}
}

// errMissingScope is the 403 a scoped token gets on an endpoint its scopes do
// not cover. The message names the accepted scopes so the fix is obvious from
// the error alone, like GitHub's scope errors do.
func errMissingScope(accepted string) *apiError {
	return &apiError{
		Status:  http.StatusForbidden,
		Message: "Token does not include the required scope(s): " + accepted,
		DocURL:  docRoot,
	}
}

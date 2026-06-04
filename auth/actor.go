package auth

import (
	"context"
	"errors"
	"time"
)

// Kind classifies the credential behind an Actor.
type Kind uint8

// The actor kinds. M1 produces Anonymous and User; the App-related kinds are
// reserved for later milestones.
const (
	KindAnonymous    Kind = iota // no or invalid credential, public-only access
	KindUser                     // classic PAT or OAuth user token
	KindUserToServer             // ghu_: a user bounded by an installation
	KindInstallation             // ghs_: an installation, no user
	KindAppJWT                   // app-level JWT
)

// Actor is the normalized principal placed in the request context. A request
// that reached the auth middleware always carries at least the anonymous actor,
// so handlers never nil-check.
type Actor struct {
	Kind Kind

	UserID    int64 // resolved user pk; 0 for anonymous
	UserLogin string
	SiteAdmin bool
	TokenID   int64 // tokens.pk of the credential used; 0 for anonymous

	Scopes    Scopes
	ExpiresAt *time.Time

	// RateKey identifies the rate-limit bucket this actor is charged against.
	RateKey string
}

// IsAuthenticated reports whether a real credential resolved.
func (a *Actor) IsAuthenticated() bool { return a != nil && a.Kind != KindAnonymous }

// IsUser reports whether the actor acts as a user.
func (a *Actor) IsUser() bool {
	return a != nil && (a.Kind == KindUser || a.Kind == KindUserToServer)
}

type ctxKey struct{}

// WithActor returns a context carrying a.
func WithActor(ctx context.Context, a *Actor) context.Context {
	return context.WithValue(ctx, ctxKey{}, a)
}

// ActorFrom never returns nil: a request without a stored actor is treated as
// anonymous.
func ActorFrom(ctx context.Context) *Actor {
	if a, ok := ctx.Value(ctxKey{}).(*Actor); ok && a != nil {
		return a
	}
	return Anonymous()
}

// Anonymous returns the unauthenticated actor.
func Anonymous() *Actor { return &Actor{Kind: KindAnonymous} }

// ErrBadCredentials is returned when a credential is present but invalid,
// expired, or revoked. It maps to 401 at the HTTP layer.
var ErrBadCredentials = errors.New("auth: bad credentials")

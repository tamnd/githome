package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/tamnd/githome/store"
)

// Service resolves credentials into Actors and serves the OAuth device flow. It
// is framework-agnostic: it speaks contexts, strings, and typed results, never
// http.ResponseWriter, so the REST layer owns all request and response wiring.
// The dependency direction is auth -> store only.
type Service struct {
	store    Store
	last     *lastUsedWriter
	baseURL  string // external base URL, used to build the device verification_uri
	keyCache *publicKeyCache
}

// NewService wires a Service over the store. baseURL is the site root used in
// device-flow responses (for example https://git.example.com).
func NewService(st Store, baseURL string) *Service {
	return &Service{
		store:    st,
		last:     newLastUsedWriter(st),
		baseURL:  strings.TrimRight(baseURL, "/"),
		keyCache: newPublicKeyCache(),
	}
}

// Close releases the background last-used flusher.
func (s *Service) Close() { s.last.Close() }

// Authenticate turns an Authorization header value into an Actor. An empty
// header is the anonymous actor with a nil error: public reads still work, and
// handlers decide whether to demand authentication. A present-but-invalid
// credential returns ErrBadCredentials, which the REST layer maps to 401.
func (s *Service) Authenticate(ctx context.Context, authorization string) (*Actor, error) {
	raw, scheme := extractToken(authorization)
	if raw == "" {
		return Anonymous(), nil
	}
	actor, err := s.resolve(ctx, raw, scheme)
	if err != nil {
		return nil, err
	}
	if actor.TokenID != 0 {
		s.last.touch(actor.TokenID)
	}
	return actor, nil
}

// extractToken pulls the credential out of an Authorization header. GitHub
// accepts "Bearer <token>", the legacy "token <token>", and Basic where the
// password field carries the token (the username is informational, as git over
// HTTPS sends the login or the literal x-access-token).
func extractToken(h string) (raw, scheme string) {
	switch {
	case strings.HasPrefix(h, "Bearer "):
		return strings.TrimSpace(h[len("Bearer "):]), "bearer"
	case strings.HasPrefix(h, "token "):
		return strings.TrimSpace(h[len("token "):]), "token"
	case strings.HasPrefix(h, "Basic "):
		user, pass, ok := decodeBasic(h[len("Basic "):])
		if !ok {
			return "", ""
		}
		if pass != "" {
			return pass, "basic"
		}
		if looksLikeToken(user) {
			return user, "basic"
		}
		return "", ""
	}
	return "", ""
}

func decodeBasic(b64 string) (user, pass string, ok bool) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return "", "", false
	}
	user, pass, ok = strings.Cut(string(decoded), ":")
	return user, pass, ok
}

// looksLikeToken does a cheap prefix test so a real password is never mistaken
// for a token when it arrives in the Basic username field.
func looksLikeToken(s string) bool {
	for _, p := range []string{PrefixClassicPAT, PrefixOAuth, PrefixUserToSrv,
		PrefixInstall, PrefixRefresh, PrefixFineGrained} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// resolve validates the checksum offline, then dispatches on the class prefix.
func (s *Service) resolve(ctx context.Context, raw, _ string) (*Actor, error) {
	switch {
	case strings.HasPrefix(raw, PrefixClassicPAT):
		return s.resolveUserToken(ctx, raw, PrefixClassicPAT, "pat")
	case strings.HasPrefix(raw, PrefixOAuth):
		return s.resolveUserToken(ctx, raw, PrefixOAuth, "oauth")
	case strings.HasPrefix(raw, PrefixInstall):
		return s.resolveInstallation(ctx, raw)
	default:
		// A three-segment dot-separated value without a known prefix may be an
		// app JWT (RS256, no ghs_ prefix). Try JWT parsing last so the cheap
		// prefix checks above short-circuit the common cases.
		if looksLikeJWT(raw) {
			return s.resolveAppJWT(ctx, raw)
		}
		return nil, ErrBadCredentials
	}
}

// lookupOpaque hashes the presented token, validates the checksum offline,
// queries by hash, and applies the lifecycle gates (revoked, expired, prefix
// match). It never reports a database error as bad credentials.
func (s *Service) lookupOpaque(ctx context.Context, raw, prefix string) (*store.TokenRow, error) {
	if !VerifyChecksum(raw) { // offline: malformed tokens never hit the database
		return nil, ErrBadCredentials
	}
	sum := sha256.Sum256([]byte(raw))
	row, err := s.store.TokenByHash(ctx, sum[:])
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrBadCredentials
	}
	if err != nil {
		return nil, err
	}
	// Constant-time compare of the stored hash, guarding the final equality even
	// though the unique index already matched.
	if subtle.ConstantTimeCompare(row.TokenHash, sum[:]) != 1 {
		return nil, ErrBadCredentials
	}
	if row.RevokedAt != nil {
		return nil, ErrBadCredentials
	}
	if row.ExpiresAt != nil && row.ExpiresAt.Before(time.Now()) {
		return nil, ErrBadCredentials
	}
	if row.TokenPrefix != prefix {
		return nil, ErrBadCredentials
	}
	return row, nil
}

// resolveUserToken turns a classic PAT or OAuth user token row into a user
// actor with its flat scopes.
func (s *Service) resolveUserToken(ctx context.Context, raw, prefix, _ string) (*Actor, error) {
	row, err := s.lookupOpaque(ctx, raw, prefix)
	if err != nil {
		return nil, err
	}
	if row.UserPK == nil {
		return nil, ErrBadCredentials
	}
	u, err := s.store.UserByPK(ctx, *row.UserPK)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrBadCredentials
	}
	if err != nil {
		return nil, err
	}
	return &Actor{
		Kind:      KindUser,
		UserID:    u.PK,
		UserLogin: u.Login,
		SiteAdmin: u.SiteAdmin,
		TokenID:   row.PK,
		Scopes:    NormalizeScopes(ParseScopeParam(row.Scopes)),
		ExpiresAt: row.ExpiresAt,
		RateKey:   "user:" + strconv.FormatInt(u.PK, 10),
	}, nil
}

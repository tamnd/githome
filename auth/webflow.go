package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/tamnd/githome/store"
)

// OAuthAppName returns the display name for the OAuth app identified by
// clientID, and whether the app is registered. Used by the FE consent page.
func (s *Service) OAuthAppName(ctx context.Context, clientID string) (string, bool) {
	app, err := s.store.OAuthAppByClientID(ctx, clientID)
	if err != nil {
		return "", false
	}
	return app.Name, true
}

// ErrInvalidCode is returned when an authorization code cannot be exchanged:
// it is unknown, already used, expired, or the redirect_uri does not match.
var ErrInvalidCode = errors.New("auth: invalid authorization code")

// AuthCodeRequest holds the parameters the caller passes to GenerateAuthCode.
type AuthCodeRequest struct {
	ClientID    string
	RedirectURI string
	Scope       string
	UserPK      int64
}

// GenerateAuthCode creates a new single-use authorization code for the OAuth
// web flow. It returns the opaque plaintext code the server embeds in the
// redirect to the client's redirect_uri.
func (s *Service) GenerateAuthCode(ctx context.Context, req AuthCodeRequest) (string, error) {
	return s.GenerateOAuthAuthCode(ctx, req.ClientID, req.RedirectURI, req.Scope, req.UserPK)
}

// GenerateOAuthAuthCode is the flat-parameter form used by the FE handler
// interface to avoid a circular import.
func (s *Service) GenerateOAuthAuthCode(ctx context.Context, clientID, redirectURI, scope string, userPK int64) (string, error) {
	app, err := s.store.OAuthAppByClientID(ctx, clientID)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrUnknownClient
	}
	if err != nil {
		return "", err
	}

	raw := randHex(20)
	h := sha256.Sum256([]byte(raw))
	row := &store.AuthCodeRow{
		CodeHash:    h[:],
		OAuthAppPK:  app.PK,
		UserPK:      userPK,
		RedirectURI: redirectURI,
		Scopes:      scope,
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	}
	if err := s.store.InsertAuthCode(ctx, row); err != nil {
		return "", err
	}
	return raw, nil
}

// ExchangeAuthCode exchanges an authorization code for an OAuth user token.
// clientID must match the code's registered app; redirectURI must equal the
// redirect_uri used when the code was issued.
func (s *Service) ExchangeAuthCode(ctx context.Context, clientID, code, redirectURI string) (*IssuedToken, error) {
	app, err := s.store.OAuthAppByClientID(ctx, clientID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnknownClient
	}
	if err != nil {
		return nil, err
	}

	h := sha256.Sum256([]byte(code))
	row, err := s.store.ConsumeAuthCode(ctx, h[:])
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrInvalidCode
	}
	if err != nil {
		return nil, err
	}

	if row.OAuthAppPK != app.PK {
		return nil, ErrInvalidCode
	}
	if row.RedirectURI != redirectURI {
		return nil, fmt.Errorf("%w: redirect_uri mismatch", ErrInvalidCode)
	}

	appPK := app.PK
	return s.issueOAuthToken(ctx, row.UserPK, &appPK, row.Scopes)
}

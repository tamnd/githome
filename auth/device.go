package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/tamnd/githome/store"
)

// Device-flow lifetimes and the slow_down step, matching GitHub.
const (
	deviceCodeTTL    = 15 * time.Minute
	deviceInterval   = 5
	slowDownStepSecs = 5
)

// Errors returned by the device-flow request step. The REST layer renders them
// as OAuth error bodies.
var (
	ErrUnknownClient      = errors.New("auth: unknown client_id")
	ErrDeviceFlowDisabled = errors.New("auth: device flow not enabled for this app")
)

// DeviceCodeResult is the body of a successful POST /login/device/code.
type DeviceCodeResult struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	ExpiresIn       int
	Interval        int
}

// IssuedToken is a minted user token returned by a completed device exchange.
type IssuedToken struct {
	AccessToken string
	TokenType   string
	Scope       string
}

// DeviceTokenOutcome is the result of one poll of the device token endpoint.
// GitHub answers every poll with HTTP 200 and a JSON body that is either the
// token or an OAuth error, so the protocol-level conditions live here rather
// than in the returned error, which is reserved for genuine server failures.
type DeviceTokenOutcome struct {
	Token            *IssuedToken
	Error            string // authorization_pending | slow_down | expired_token | access_denied | ...
	ErrorDescription string
	Interval         int // set with slow_down
}

// RequestDeviceCode opens a device-flow session for the given client and scopes.
func (s *Service) RequestDeviceCode(ctx context.Context, clientID, scopeParam string) (*DeviceCodeResult, error) {
	app, err := s.store.OAuthAppByClientID(ctx, clientID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnknownClient
	}
	if err != nil {
		return nil, err
	}
	if !app.DeviceFlowEnabled {
		return nil, ErrDeviceFlowDisabled
	}
	scopes := NormalizeScopes(ParseScopeParam(scopeParam))

	deviceCode := randHex(20) // 40 hex chars
	userCode := genUserCode()
	dch := sha256.Sum256([]byte(deviceCode))
	row := &store.DeviceCodeRow{
		DeviceCodeHash: dch[:],
		UserCode:       userCode,
		OAuthAppPK:     &app.PK,
		Scopes:         scopes.Header(),
		State:          "pending",
		IntervalSec:    deviceInterval,
		ExpiresAt:      time.Now().Add(deviceCodeTTL),
	}
	if err := s.store.InsertDeviceCode(ctx, row); err != nil {
		return nil, err
	}
	return &DeviceCodeResult{
		DeviceCode:      deviceCode,
		UserCode:        userCode,
		VerificationURI: s.baseURL + "/login/device",
		ExpiresIn:       int(deviceCodeTTL.Seconds()),
		Interval:        deviceInterval,
	}, nil
}

// ApproveDeviceCode marks the session behind userCode approved by userPK. It
// returns store.ErrNotFound when the code is unknown or already expired.
func (s *Service) ApproveDeviceCode(ctx context.Context, userCode string, userPK int64) error {
	row, err := s.liveDeviceByUserCode(ctx, userCode)
	if err != nil {
		return err
	}
	return s.store.SetDeviceState(ctx, row.PK, "approved", userPK)
}

// DenyDeviceCode marks the session behind userCode denied.
func (s *Service) DenyDeviceCode(ctx context.Context, userCode string) error {
	row, err := s.liveDeviceByUserCode(ctx, userCode)
	if err != nil {
		return err
	}
	return s.store.SetDeviceState(ctx, row.PK, "denied", 0)
}

func (s *Service) liveDeviceByUserCode(ctx context.Context, userCode string) (*store.DeviceCodeRow, error) {
	row, err := s.store.DeviceCodeByUserCode(ctx, normalizeUserCode(userCode))
	if err != nil {
		return nil, err
	}
	if row.ExpiresAt.Before(time.Now()) {
		return nil, store.ErrNotFound
	}
	return row, nil
}

// PollDeviceToken advances the device-flow state machine for one poll. The
// returned error is non-nil only on a genuine failure (a bad client, an unknown
// device code, or a store error); every protocol condition is reported in the
// outcome.
func (s *Service) PollDeviceToken(ctx context.Context, clientID, deviceCode string) (*DeviceTokenOutcome, error) {
	app, err := s.store.OAuthAppByClientID(ctx, clientID)
	if errors.Is(err, store.ErrNotFound) {
		return &DeviceTokenOutcome{Error: "incorrect_client_credentials", ErrorDescription: "The client_id is not registered."}, nil
	}
	if err != nil {
		return nil, err
	}

	dch := sha256.Sum256([]byte(deviceCode))
	row, err := s.store.DeviceCodeByHash(ctx, dch[:])
	if errors.Is(err, store.ErrNotFound) {
		return &DeviceTokenOutcome{Error: "incorrect_device_code", ErrorDescription: "The device code is not valid."}, nil
	}
	if err != nil {
		return nil, err
	}
	if row.OAuthAppPK == nil || *row.OAuthAppPK != app.PK {
		return &DeviceTokenOutcome{Error: "incorrect_device_code", ErrorDescription: "The device code is not valid."}, nil
	}

	now := time.Now()
	if row.ExpiresAt.Before(now) {
		_ = s.store.DeleteDeviceCode(ctx, row.PK)
		return &DeviceTokenOutcome{Error: "expired_token", ErrorDescription: "The device code has expired."}, nil
	}

	// Enforce the minimum poll interval: a client polling too fast gets slow_down
	// and the stored interval is bumped, exactly like GitHub.
	if row.LastPolledAt != nil && now.Sub(*row.LastPolledAt) < time.Duration(row.IntervalSec)*time.Second {
		newInterval := row.IntervalSec + slowDownStepSecs
		_ = s.store.SetDeviceInterval(ctx, row.PK, newInterval)
		return &DeviceTokenOutcome{Error: "slow_down", ErrorDescription: "You are polling too frequently.", Interval: newInterval}, nil
	}
	_ = s.store.SetDevicePolled(ctx, row.PK, now)

	switch row.State {
	case "pending":
		return &DeviceTokenOutcome{Error: "authorization_pending", ErrorDescription: "The authorization request is still pending."}, nil
	case "denied":
		_ = s.store.DeleteDeviceCode(ctx, row.PK)
		return &DeviceTokenOutcome{Error: "access_denied", ErrorDescription: "The user denied the authorization request."}, nil
	case "approved":
		if row.UserPK == nil {
			return &DeviceTokenOutcome{Error: "authorization_pending", ErrorDescription: "The authorization request is still pending."}, nil
		}
		issued, err := s.issueOAuthToken(ctx, *row.UserPK, row.OAuthAppPK, row.Scopes)
		if err != nil {
			return nil, err
		}
		_ = s.store.DeleteDeviceCode(ctx, row.PK) // single use
		return &DeviceTokenOutcome{Token: issued}, nil
	default:
		return &DeviceTokenOutcome{Error: "authorization_pending"}, nil
	}
}

// issueOAuthToken mints a gho_ user token, persists its hash, and returns the
// one-time plaintext to hand back to the client.
func (s *Service) issueOAuthToken(ctx context.Context, userPK int64, appPK *int64, scopes string) (*IssuedToken, error) {
	g, err := GenerateToken(PrefixOAuth)
	if err != nil {
		return nil, err
	}
	hash := g.Hash
	row := &store.TokenRow{
		UserPK:      &userPK,
		OAuthAppPK:  appPK,
		TokenHash:   hash[:],
		TokenPrefix: g.Prefix,
		LastEight:   g.Last8,
		Kind:        "oauth",
		Scopes:      scopes,
	}
	if err := s.store.InsertToken(ctx, row); err != nil {
		return nil, err
	}
	scope := strings.Join(NormalizeScopes(ParseScopeParam(scopes)).Strings(), ",")
	return &IssuedToken{AccessToken: g.Plaintext, TokenType: "bearer", Scope: scope}, nil
}

// randHex returns n random bytes hex-encoded (2n characters).
func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// genUserCode produces an 8-character code from an unambiguous alphabet,
// formatted XXXX-XXXX.
func genUserCode() string {
	const alpha = "BCDFGHJKLMNPQRSTVWXZ" // no vowels, no easily confused glyphs
	r := make([]byte, 8)
	_, _ = rand.Read(r)
	b := make([]byte, 8)
	for i := range b {
		b[i] = alpha[int(r[i])%len(alpha)]
	}
	return string(b[:4]) + "-" + string(b[4:])
}

// normalizeUserCode uppercases and re-inserts the dash so "wdjbmjht" and
// "wdjb-mjht" both match the stored "WDJB-MJHT".
func normalizeUserCode(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			sb.WriteRune(r - 32)
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			sb.WriteRune(r)
		}
	}
	code := sb.String()
	if len(code) == 8 {
		return code[:4] + "-" + code[4:]
	}
	return code
}

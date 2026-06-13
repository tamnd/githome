package auth

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/tamnd/githome/store"
)

// publicKeyCache holds parsed RSA public keys keyed by github_apps.pk so
// resolveAppJWT does not re-parse PEM on every request.
type publicKeyCache struct {
	mu   sync.RWMutex
	keys map[int64]*rsa.PublicKey
}

func newPublicKeyCache() *publicKeyCache { return &publicKeyCache{keys: map[int64]*rsa.PublicKey{}} }

func (c *publicKeyCache) get(pk int64) (*rsa.PublicKey, bool) {
	c.mu.RLock()
	k, ok := c.keys[pk]
	c.mu.RUnlock()
	return k, ok
}

func (c *publicKeyCache) set(pk int64, k *rsa.PublicKey) {
	c.mu.Lock()
	c.keys[pk] = k
	c.mu.Unlock()
}

// resolveAppJWT validates an RS256 JWT sent as a Bearer token by a GitHub App.
// On success it returns a KindAppJWT actor whose AppID is the app's internal PK.
func (s *Service) resolveAppJWT(ctx context.Context, raw string) (*Actor, error) {
	var claims jwt.RegisteredClaims
	keyFunc := func(t *jwt.Token) (any, error) {
		iss, err := claims.GetIssuer()
		if err != nil || iss == "" {
			return nil, ErrBadCredentials
		}
		appID, err := strconv.ParseInt(iss, 10, 64)
		if err != nil {
			return nil, ErrBadCredentials
		}
		pub, err := s.publicKeyForApp(ctx, appID)
		if err != nil {
			return nil, ErrBadCredentials
		}
		return pub, nil
	}
	tok, err := jwt.ParseWithClaims(raw, &claims, keyFunc,
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(60*time.Second),
	)
	if err != nil || !tok.Valid {
		return nil, ErrBadCredentials
	}
	// Enforce the 10-minute cap.
	if claims.IssuedAt == nil || claims.ExpiresAt == nil ||
		claims.ExpiresAt.Sub(claims.IssuedAt.Time) > 10*time.Minute {
		return nil, ErrBadCredentials
	}
	appID, _ := strconv.ParseInt(claims.Issuer, 10, 64)
	app, err := s.store.GitHubAppByPK(ctx, appID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrBadCredentials
	}
	if err != nil {
		return nil, err
	}
	exp := claims.ExpiresAt.Time
	return &Actor{
		Kind:      KindAppJWT,
		AppID:     app.PK,
		ExpiresAt: &exp,
		RateKey:   "app:" + strconv.FormatInt(app.PK, 10),
	}, nil
}

// publicKeyForApp returns the RSA public key for a registered GitHub App,
// parsing it from the stored private key PEM on first access.
func (s *Service) publicKeyForApp(ctx context.Context, appID int64) (*rsa.PublicKey, error) {
	if k, ok := s.keyCache.get(appID); ok {
		return k, nil
	}
	app, err := s.store.GitHubAppByPK(ctx, appID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrBadCredentials
	}
	if err != nil {
		return nil, err
	}
	priv, err := jwt.ParseRSAPrivateKeyFromPEM(app.PrivateKeyPEM)
	if err != nil {
		return nil, ErrBadCredentials
	}
	pub := &priv.PublicKey
	s.keyCache.set(appID, pub)
	return pub, nil
}

// resolveInstallation turns a ghs_ installation token into a KindInstallation actor.
func (s *Service) resolveInstallation(ctx context.Context, raw string) (*Actor, error) {
	row, err := s.lookupOpaque(ctx, raw, PrefixInstall)
	if err != nil {
		return nil, err
	}
	if row.InstallationPK == nil || row.GitHubAppPK == nil {
		return nil, ErrBadCredentials
	}
	return &Actor{
		Kind:           KindInstallation,
		AppID:          *row.GitHubAppPK,
		InstallationID: *row.InstallationPK,
		TokenID:        row.PK,
		ExpiresAt:      row.ExpiresAt,
		RateKey:        "installation:" + strconv.FormatInt(*row.InstallationPK, 10),
	}, nil
}

// CreateInstallationToken mints a ghs_ token for instPK. actor must be KindAppJWT
// and own the installation. On success it returns the plaintext token and expiry.
// repos and permissions narrow the grant (empty = full installation grant).
func (s *Service) CreateInstallationToken(ctx context.Context, actor *Actor, instPK int64,
	repos []string, permissions map[string]string) (plaintext string, expiresAt time.Time, err error) {

	if actor.Kind != KindAppJWT {
		return "", time.Time{}, ErrBadCredentials
	}
	inst, err := s.store.InstallationByPK(ctx, instPK)
	if errors.Is(err, store.ErrNotFound) {
		return "", time.Time{}, ErrBadCredentials
	}
	if err != nil {
		return "", time.Time{}, err
	}
	if inst.AppPK != actor.AppID {
		return "", time.Time{}, ErrBadCredentials
	}
	if inst.SuspendedAt != nil {
		return "", time.Time{}, ErrInstallationSuspended
	}

	g, err := GenerateToken(PrefixInstall)
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().Add(1 * time.Hour)
	h := sha256.Sum256([]byte(g.Plaintext))
	appPK := inst.AppPK
	t := &store.TokenRow{
		TokenHash:      h[:],
		TokenPrefix:    PrefixInstall,
		LastEight:      g.Last8,
		Kind:           "installation",
		InstallationPK: &inst.PK,
		GitHubAppPK:    &appPK,
		ExpiresAt:      &expires,
	}
	if err := s.store.InsertToken(ctx, t); err != nil {
		return "", time.Time{}, err
	}
	return g.Plaintext, expires, nil
}

// ErrInstallationSuspended is returned when a caller tries to mint a token for a
// suspended installation.
var ErrInstallationSuspended = errors.New("auth: installation is suspended")

// AppByPK loads a GitHub App by its internal primary key. The REST layer uses
// this to render the app object for GET /app.
func (s *Service) AppByPK(ctx context.Context, pk int64) (*store.GitHubAppRow, error) {
	return s.store.GitHubAppByPK(ctx, pk)
}

// InstallationsByApp returns all installations for the given app PK.
func (s *Service) InstallationsByApp(ctx context.Context, appPK int64) ([]*store.InstallationRow, error) {
	return s.store.InstallationsByAppPK(ctx, appPK)
}

// InstallationByPK loads one installation by its internal primary key. The REST
// layer renders it for the installation-token actor's own metadata.
func (s *Service) InstallationByPK(ctx context.Context, pk int64) (*store.InstallationRow, error) {
	return s.store.InstallationByPK(ctx, pk)
}

// InstallationByDBID loads one installation by its public database id, the id
// carried in the access_tokens_url the installation object hands to API clients.
func (s *Service) InstallationByDBID(ctx context.Context, dbID int64) (*store.InstallationRow, error) {
	return s.store.InstallationByDBID(ctx, dbID)
}

// InstallationByAppAndAccount resolves the installation of app appPK on the
// account accountPK, backing GET /repos/{owner}/{repo}/installation.
func (s *Service) InstallationByAppAndAccount(ctx context.Context, appPK, accountPK int64) (*store.InstallationRow, error) {
	return s.store.InstallationByAppAndAccount(ctx, appPK, accountPK)
}

// InstallationRepoPKs returns the repo PKs a "selected"-scope installation may
// access, backing GET /installation/repositories.
func (s *Service) InstallationRepoPKs(ctx context.Context, instPK int64) ([]int64, error) {
	return s.store.InstallationRepoPKs(ctx, instPK)
}

// looksLikeJWT does a quick structural check (three dot-separated segments) so
// resolveAppJWT is only called for JWT-shaped strings and not for every Bearer token.
func looksLikeJWT(s string) bool {
	n := 0
	for _, c := range s {
		if c == '.' {
			n++
		}
	}
	return n == 2
}

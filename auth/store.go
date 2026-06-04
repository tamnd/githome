package auth

import (
	"context"
	"time"

	"github.com/tamnd/githome/store"
)

// Store is the narrow slice of the metadata store the auth package depends on.
// *store.Store satisfies it. Keeping the dependency to an interface lets the
// auth tests drive the service with an in-memory fake and documents exactly
// which store methods auth reaches for.
type Store interface {
	// Credential resolution.
	TokenByHash(ctx context.Context, hash []byte) (*store.TokenRow, error)
	UserByPK(ctx context.Context, pk int64) (*store.UserRow, error)
	BumpTokenLastUsed(ctx context.Context, at map[int64]time.Time) error

	// OAuth device flow.
	OAuthAppByClientID(ctx context.Context, clientID string) (*store.OAuthAppRow, error)
	InsertToken(ctx context.Context, t *store.TokenRow) error
	InsertDeviceCode(ctx context.Context, d *store.DeviceCodeRow) error
	DeviceCodeByHash(ctx context.Context, hash []byte) (*store.DeviceCodeRow, error)
	DeviceCodeByUserCode(ctx context.Context, userCode string) (*store.DeviceCodeRow, error)
	SetDeviceState(ctx context.Context, pk int64, state string, userPK int64) error
	SetDeviceInterval(ctx context.Context, pk int64, interval int) error
	SetDevicePolled(ctx context.Context, pk int64, at time.Time) error
	DeleteDeviceCode(ctx context.Context, pk int64) error
}

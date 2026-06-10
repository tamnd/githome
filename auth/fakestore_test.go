package auth

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"sort"
	"time"

	"github.com/tamnd/githome/store"
)

// fakeStore is an in-memory auth.Store for the service and device-flow tests. It
// keeps the dependency on a real database out of the unit tests while exercising
// the exact method surface the auth package reaches for.
type fakeStore struct {
	seq       int64
	users     map[int64]*store.UserRow
	tokens    map[int64]*store.TokenRow
	apps      map[string]*store.OAuthAppRow
	devices   map[int64]*store.DeviceCodeRow
	authCodes map[int64]*store.AuthCodeRow
	lastUsed  map[int64]time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		users:     map[int64]*store.UserRow{},
		tokens:    map[int64]*store.TokenRow{},
		apps:      map[string]*store.OAuthAppRow{},
		devices:   map[int64]*store.DeviceCodeRow{},
		authCodes: map[int64]*store.AuthCodeRow{},
		lastUsed:  map[int64]time.Time{},
	}
}

func (f *fakeStore) nextPK() int64 { f.seq++; return f.seq }

// addUser inserts a user and returns its pk.
func (f *fakeStore) addUser(u *store.UserRow) int64 {
	u.PK = f.nextPK()
	f.users[u.PK] = u
	return u.PK
}

// addToken inserts a pre-built token row and returns its pk.
func (f *fakeStore) addToken(t *store.TokenRow) int64 {
	t.PK = f.nextPK()
	f.tokens[t.PK] = t
	return t.PK
}

// addApp inserts an OAuth app and returns it.
func (f *fakeStore) addApp(a *store.OAuthAppRow) *store.OAuthAppRow {
	a.PK = f.nextPK()
	f.apps[a.ClientID] = a
	return a
}

func (f *fakeStore) TokenByHash(_ context.Context, hash []byte) (*store.TokenRow, error) {
	for _, t := range f.tokens {
		if bytes.Equal(t.TokenHash, hash) {
			return t, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeStore) UserByPK(_ context.Context, pk int64) (*store.UserRow, error) {
	if u, ok := f.users[pk]; ok {
		return u, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeStore) BumpTokenLastUsed(_ context.Context, at map[int64]time.Time) error {
	maps.Copy(f.lastUsed, at)
	return nil
}

func (f *fakeStore) OAuthAppByClientID(_ context.Context, clientID string) (*store.OAuthAppRow, error) {
	if a, ok := f.apps[clientID]; ok {
		return a, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeStore) InsertOAuthApp(_ context.Context, a *store.OAuthAppRow) error {
	if _, ok := f.apps[a.ClientID]; ok {
		return errors.New("UNIQUE constraint failed: oauth_apps.client_id")
	}
	f.addApp(a)
	return nil
}

func (f *fakeStore) InsertToken(_ context.Context, t *store.TokenRow) error {
	t.PK = f.nextPK()
	t.CreatedAt = time.Now()
	f.tokens[t.PK] = t
	return nil
}

func (f *fakeStore) TokensForUser(_ context.Context, userPK int64) ([]*store.TokenRow, error) {
	var out []*store.TokenRow
	for _, t := range f.tokens {
		if t.UserPK != nil && *t.UserPK == userPK && t.Kind == "pat" && t.RevokedAt == nil {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PK > out[j].PK })
	return out, nil
}

func (f *fakeStore) DeleteUserToken(_ context.Context, pk, userPK int64) error {
	t, ok := f.tokens[pk]
	if !ok || t.UserPK == nil || *t.UserPK != userPK || t.Kind != "pat" {
		return store.ErrNotFound
	}
	delete(f.tokens, pk)
	return nil
}

func (f *fakeStore) InsertDeviceCode(_ context.Context, d *store.DeviceCodeRow) error {
	d.PK = f.nextPK()
	d.CreatedAt = time.Now()
	f.devices[d.PK] = d
	return nil
}

func (f *fakeStore) DeviceCodeByHash(_ context.Context, hash []byte) (*store.DeviceCodeRow, error) {
	for _, d := range f.devices {
		if bytes.Equal(d.DeviceCodeHash, hash) {
			return d, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeStore) DeviceCodeByUserCode(_ context.Context, userCode string) (*store.DeviceCodeRow, error) {
	for _, d := range f.devices {
		if d.UserCode == userCode {
			return d, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeStore) SetDeviceState(_ context.Context, pk int64, state string, userPK int64) error {
	d, ok := f.devices[pk]
	if !ok {
		return store.ErrNotFound
	}
	d.State = state
	if userPK == 0 {
		d.UserPK = nil
	} else {
		d.UserPK = &userPK
	}
	return nil
}

func (f *fakeStore) SetDeviceInterval(_ context.Context, pk int64, interval int) error {
	d, ok := f.devices[pk]
	if !ok {
		return store.ErrNotFound
	}
	d.IntervalSec = interval
	return nil
}

func (f *fakeStore) SetDevicePolled(_ context.Context, pk int64, at time.Time) error {
	d, ok := f.devices[pk]
	if !ok {
		return store.ErrNotFound
	}
	d.LastPolledAt = &at
	return nil
}

func (f *fakeStore) DeleteDeviceCode(_ context.Context, pk int64) error {
	delete(f.devices, pk)
	return nil
}

func (f *fakeStore) InsertAuthCode(_ context.Context, a *store.AuthCodeRow) error {
	a.PK = f.nextPK()
	a.CreatedAt = time.Now()
	f.authCodes[a.PK] = a
	return nil
}

func (f *fakeStore) ConsumeAuthCode(_ context.Context, codeHash []byte) (*store.AuthCodeRow, error) {
	for _, a := range f.authCodes {
		if bytes.Equal(a.CodeHash, codeHash) {
			if a.Used || time.Now().After(a.ExpiresAt) {
				return nil, store.ErrNotFound
			}
			a.Used = true
			return a, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeStore) GitHubAppByPK(_ context.Context, _ int64) (*store.GitHubAppRow, error) {
	return nil, store.ErrNotFound
}

func (f *fakeStore) InstallationByPK(_ context.Context, _ int64) (*store.InstallationRow, error) {
	return nil, store.ErrNotFound
}

func (f *fakeStore) InstallationsByAppPK(_ context.Context, _ int64) ([]*store.InstallationRow, error) {
	return nil, nil
}

func (f *fakeStore) InstallationRepoPKs(_ context.Context, _ int64) ([]int64, error) {
	return nil, nil
}

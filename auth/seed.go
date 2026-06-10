package auth

import (
	"context"
	"errors"

	"github.com/tamnd/githome/store"
)

// GHCLIClientID is the OAuth client_id hardcoded into the gh CLI. gh sends it
// to POST /login/device/code on every "gh auth login", so the server must know
// the app before any user can sign in through gh.
const GHCLIClientID = "178c6fc778ccc68e1d6a"

// EnsureFirstPartyApps seeds the OAuth app rows first-party clients expect to
// exist, today just the gh CLI's device-flow app. It is idempotent and runs at
// every startup, so an existing row (including one another process inserted
// concurrently) is left alone. The gh app is a public client: it holds no
// client secret and signs in through the device flow alone.
func (s *Service) EnsureFirstPartyApps(ctx context.Context) error {
	_, err := s.store.OAuthAppByClientID(ctx, GHCLIClientID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	insertErr := s.store.InsertOAuthApp(ctx, &store.OAuthAppRow{
		ClientID:          GHCLIClientID,
		Name:              "GitHub CLI",
		DeviceFlowEnabled: true,
	})
	if insertErr == nil {
		return nil
	}
	// The unique index on client_id makes a concurrent boot lose this race
	// cleanly: if the row is there now, someone else seeded it first.
	if _, err := s.store.OAuthAppByClientID(ctx, GHCLIClientID); err == nil {
		return nil
	}
	return insertErr
}

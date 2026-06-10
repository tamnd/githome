package auth

import (
	"context"
	"testing"

	"github.com/tamnd/githome/store"
)

func TestEnsureFirstPartyAppsSeedsGHCLI(t *testing.T) {
	f := newFakeStore()
	svc := NewService(f, "https://git.test.internal")
	t.Cleanup(svc.Close)

	if err := svc.EnsureFirstPartyApps(context.Background()); err != nil {
		t.Fatalf("EnsureFirstPartyApps: %v", err)
	}
	app, err := f.OAuthAppByClientID(context.Background(), GHCLIClientID)
	if err != nil {
		t.Fatalf("gh app not seeded: %v", err)
	}
	if !app.DeviceFlowEnabled {
		t.Error("gh app must be device-flow enabled")
	}
	if len(app.ClientSecretHash) != 0 {
		t.Error("gh app is a public client and must hold no secret")
	}

	// The seeded app makes gh's first request work: POST /login/device/code
	// with the hardcoded client_id no longer answers unknown-client.
	if _, err := svc.RequestDeviceCode(context.Background(), GHCLIClientID, "repo"); err != nil {
		t.Fatalf("RequestDeviceCode with gh client_id: %v", err)
	}
}

func TestEnsureFirstPartyAppsIsIdempotent(t *testing.T) {
	f := newFakeStore()
	svc := NewService(f, "https://git.test.internal")
	t.Cleanup(svc.Close)

	for i := 0; i < 2; i++ {
		if err := svc.EnsureFirstPartyApps(context.Background()); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	count := 0
	for range f.apps {
		count++
	}
	if count != 1 {
		t.Fatalf("apps seeded = %d, want 1", count)
	}
}

func TestEnsureFirstPartyAppsKeepsExistingRow(t *testing.T) {
	f := newFakeStore()
	existing := f.addApp(&store.OAuthAppRow{ClientID: GHCLIClientID, Name: "Custom gh", DeviceFlowEnabled: true})
	svc := NewService(f, "https://git.test.internal")
	t.Cleanup(svc.Close)

	if err := svc.EnsureFirstPartyApps(context.Background()); err != nil {
		t.Fatalf("EnsureFirstPartyApps: %v", err)
	}
	app, _ := f.OAuthAppByClientID(context.Background(), GHCLIClientID)
	if app.PK != existing.PK || app.Name != "Custom gh" {
		t.Errorf("existing row was replaced: %+v", app)
	}
}

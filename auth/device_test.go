package auth

import (
	"context"
	"testing"
	"time"

	"github.com/tamnd/githome/store"
)

// deviceFixture wires a service over a fake store holding one device-flow app
// and one user, and returns everything a device-flow test needs.
type deviceFixture struct {
	svc    *Service
	store  *fakeStore
	app    *store.OAuthAppRow
	userPK int64
}

func newDeviceFixture(t *testing.T) *deviceFixture {
	t.Helper()
	f := newFakeStore()
	app := f.addApp(&store.OAuthAppRow{ClientID: "Iv1.abcdef0123456789", Name: "CLI", DeviceFlowEnabled: true})
	uid := f.addUser(&store.UserRow{Login: "octocat", Type: "User"})
	svc := NewService(f, "https://git.test.internal")
	t.Cleanup(svc.Close)
	return &deviceFixture{svc: svc, store: f, app: app, userPK: uid}
}

func TestRequestDeviceCode(t *testing.T) {
	fx := newDeviceFixture(t)
	res, err := fx.svc.RequestDeviceCode(context.Background(), fx.app.ClientID, "repo gist")
	if err != nil {
		t.Fatal(err)
	}
	if res.DeviceCode == "" || res.UserCode == "" {
		t.Fatal("device and user codes must be set")
	}
	if res.Interval != deviceInterval {
		t.Errorf("interval = %d, want %d", res.Interval, deviceInterval)
	}
	if res.ExpiresIn != int(deviceCodeTTL.Seconds()) {
		t.Errorf("expires_in = %d, want %d", res.ExpiresIn, int(deviceCodeTTL.Seconds()))
	}
	if res.VerificationURI != "https://git.test.internal/login/device" {
		t.Errorf("verification_uri = %q", res.VerificationURI)
	}
}

func TestRequestDeviceCodeUnknownClient(t *testing.T) {
	fx := newDeviceFixture(t)
	if _, err := fx.svc.RequestDeviceCode(context.Background(), "Iv1.nope", ""); err != ErrUnknownClient {
		t.Errorf("err = %v, want ErrUnknownClient", err)
	}
}

func TestRequestDeviceCodeDisabledApp(t *testing.T) {
	fx := newDeviceFixture(t)
	fx.store.addApp(&store.OAuthAppRow{ClientID: "Iv1.disabled", DeviceFlowEnabled: false})
	if _, err := fx.svc.RequestDeviceCode(context.Background(), "Iv1.disabled", ""); err != ErrDeviceFlowDisabled {
		t.Errorf("err = %v, want ErrDeviceFlowDisabled", err)
	}
}

func TestPollPending(t *testing.T) {
	fx := newDeviceFixture(t)
	res, _ := fx.svc.RequestDeviceCode(context.Background(), fx.app.ClientID, "repo")

	out, err := fx.svc.PollDeviceToken(context.Background(), fx.app.ClientID, res.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if out.Token != nil || out.Error != "authorization_pending" {
		t.Fatalf("outcome = %+v, want authorization_pending", out)
	}
}

func TestPollApprovedIssuesToken(t *testing.T) {
	fx := newDeviceFixture(t)
	res, _ := fx.svc.RequestDeviceCode(context.Background(), fx.app.ClientID, "repo gist")

	// Approve before the first poll so the slow_down interval guard does not fire.
	if err := fx.svc.ApproveDeviceCode(context.Background(), res.UserCode, fx.userPK); err != nil {
		t.Fatal(err)
	}
	out, err := fx.svc.PollDeviceToken(context.Background(), fx.app.ClientID, res.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if out.Token == nil {
		t.Fatalf("outcome = %+v, want a token", out)
	}
	if out.Token.TokenType != "bearer" {
		t.Errorf("token_type = %q, want bearer", out.Token.TokenType)
	}
	if !VerifyChecksum(out.Token.AccessToken) {
		t.Error("issued access token fails its own checksum")
	}
	if out.Token.Scope != "gist,repo" {
		t.Errorf("scope = %q, want %q", out.Token.Scope, "gist,repo")
	}
	// The minted token must authenticate as the approving user.
	a, err := fx.svc.Authenticate(context.Background(), "token "+out.Token.AccessToken)
	if err != nil {
		t.Fatalf("authenticate issued token: %v", err)
	}
	if a.UserID != fx.userPK {
		t.Errorf("issued token resolves to user %d, want %d", a.UserID, fx.userPK)
	}
	// The device code is single-use: a second poll no longer finds it.
	out2, _ := fx.svc.PollDeviceToken(context.Background(), fx.app.ClientID, res.DeviceCode)
	if out2.Error != "incorrect_device_code" {
		t.Errorf("second poll error = %q, want incorrect_device_code", out2.Error)
	}
}

func TestPollSlowDown(t *testing.T) {
	fx := newDeviceFixture(t)
	res, _ := fx.svc.RequestDeviceCode(context.Background(), fx.app.ClientID, "repo")

	// First poll records the timestamp; the immediate second poll is too soon.
	if _, err := fx.svc.PollDeviceToken(context.Background(), fx.app.ClientID, res.DeviceCode); err != nil {
		t.Fatal(err)
	}
	out, err := fx.svc.PollDeviceToken(context.Background(), fx.app.ClientID, res.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if out.Error != "slow_down" {
		t.Fatalf("error = %q, want slow_down", out.Error)
	}
	if out.Interval != deviceInterval+slowDownStepSecs {
		t.Errorf("bumped interval = %d, want %d", out.Interval, deviceInterval+slowDownStepSecs)
	}
}

func TestPollAccessDenied(t *testing.T) {
	fx := newDeviceFixture(t)
	res, _ := fx.svc.RequestDeviceCode(context.Background(), fx.app.ClientID, "repo")

	if err := fx.svc.DenyDeviceCode(context.Background(), res.UserCode); err != nil {
		t.Fatal(err)
	}
	out, err := fx.svc.PollDeviceToken(context.Background(), fx.app.ClientID, res.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if out.Error != "access_denied" {
		t.Errorf("error = %q, want access_denied", out.Error)
	}
}

func TestPollExpiredToken(t *testing.T) {
	fx := newDeviceFixture(t)
	res, _ := fx.svc.RequestDeviceCode(context.Background(), fx.app.ClientID, "repo")

	// Age the stored session past its TTL.
	for _, d := range fx.store.devices {
		d.ExpiresAt = time.Now().Add(-time.Minute)
	}
	out, err := fx.svc.PollDeviceToken(context.Background(), fx.app.ClientID, res.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if out.Error != "expired_token" {
		t.Errorf("error = %q, want expired_token", out.Error)
	}
}

func TestPollUnknownClient(t *testing.T) {
	fx := newDeviceFixture(t)
	res, _ := fx.svc.RequestDeviceCode(context.Background(), fx.app.ClientID, "repo")

	out, err := fx.svc.PollDeviceToken(context.Background(), "Iv1.someoneelse", res.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if out.Error != "incorrect_client_credentials" {
		t.Errorf("error = %q, want incorrect_client_credentials", out.Error)
	}
}

func TestNormalizeUserCode(t *testing.T) {
	cases := map[string]string{
		"wdjb-mjht": "WDJB-MJHT",
		"WDJBMJHT":  "WDJB-MJHT",
		"wdjbmjht":  "WDJB-MJHT",
	}
	for in, want := range cases {
		if got := normalizeUserCode(in); got != want {
			t.Errorf("normalizeUserCode(%q) = %q, want %q", in, got, want)
		}
	}
}

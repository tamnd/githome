package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"

	"github.com/tamnd/githome/store"
)

// webFlowFixture seeds a user and an OAuth app and returns the service. A
// non-empty secret is hashed into the app row, making it a confidential
// client; an empty secret leaves a public client like the seeded gh app.
func webFlowFixture(t *testing.T, secret, callbackURL string) (*Service, *fakeStore, int64) {
	t.Helper()
	fs := newFakeStore()
	userPK := fs.addUser(&store.UserRow{Login: "octocat"})
	app := &store.OAuthAppRow{ClientID: "webapp", Name: "Web App", CallbackURL: callbackURL}
	if secret != "" {
		h := sha256.Sum256([]byte(secret))
		app.ClientSecretHash = h[:]
	}
	fs.addApp(app)
	svc := NewService(fs, "https://example.test")
	t.Cleanup(svc.Close)
	return svc, fs, userPK
}

func TestExchangeAuthCodeVerifiesClientSecret(t *testing.T) {
	svc, _, userPK := webFlowFixture(t, "s3cret", "")
	ctx := context.Background()
	redirect := "https://client.example/cb"

	mintCode := func() string {
		code, err := svc.GenerateOAuthAuthCode(ctx, "webapp", redirect, "repo", userPK)
		if err != nil {
			t.Fatalf("generate code: %v", err)
		}
		return code
	}

	// No secret at all.
	if _, err := svc.ExchangeAuthCode(ctx, "webapp", "", mintCode(), redirect); !errors.Is(err, ErrInvalidClientSecret) {
		t.Fatalf("missing secret: err = %v, want ErrInvalidClientSecret", err)
	}
	// The wrong secret.
	if _, err := svc.ExchangeAuthCode(ctx, "webapp", "wrong", mintCode(), redirect); !errors.Is(err, ErrInvalidClientSecret) {
		t.Fatalf("wrong secret: err = %v, want ErrInvalidClientSecret", err)
	}
	// The right one mints a token.
	tok, err := svc.ExchangeAuthCode(ctx, "webapp", "s3cret", mintCode(), redirect)
	if err != nil {
		t.Fatalf("right secret: %v", err)
	}
	if !strings.HasPrefix(tok.AccessToken, PrefixOAuth) {
		t.Errorf("access token = %q, want %s prefix", tok.AccessToken, PrefixOAuth)
	}
}

func TestExchangeAuthCodePublicClientSkipsSecret(t *testing.T) {
	// An app with no secret on file is a public client, like the seeded gh
	// app: the exchange must keep working without a client_secret.
	svc, _, userPK := webFlowFixture(t, "", "")
	ctx := context.Background()
	redirect := "https://client.example/cb"

	code, err := svc.GenerateOAuthAuthCode(ctx, "webapp", redirect, "repo", userPK)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExchangeAuthCode(ctx, "webapp", "", code, redirect); err != nil {
		t.Fatalf("public client exchange: %v", err)
	}
}

func TestRedirectURIValidatedAgainstCallback(t *testing.T) {
	svc, _, userPK := webFlowFixture(t, "", "https://client.example/cb")
	ctx := context.Background()

	// A redirect outside the registered callback is refused at code issue.
	if _, err := svc.GenerateOAuthAuthCode(ctx, "webapp", "https://evil.example/steal", "repo", userPK); !errors.Is(err, ErrInvalidRedirectURI) {
		t.Fatalf("rogue redirect at authorize: err = %v, want ErrInvalidRedirectURI", err)
	}

	// A redirect under the callback passes, by prefix like GitHub.
	redirect := "https://client.example/cb/landing?next=1"
	code, err := svc.GenerateOAuthAuthCode(ctx, "webapp", redirect, "repo", userPK)
	if err != nil {
		t.Fatalf("prefixed redirect: %v", err)
	}
	if _, err := svc.ExchangeAuthCode(ctx, "webapp", "", code, redirect); err != nil {
		t.Fatalf("exchange with prefixed redirect: %v", err)
	}

	// The exchange refuses a rogue redirect too, even with a valid code.
	code, err = svc.GenerateOAuthAuthCode(ctx, "webapp", redirect, "repo", userPK)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExchangeAuthCode(ctx, "webapp", "", code, "https://evil.example/steal"); !errors.Is(err, ErrInvalidRedirectURI) {
		t.Fatalf("rogue redirect at exchange: err = %v, want ErrInvalidRedirectURI", err)
	}
}

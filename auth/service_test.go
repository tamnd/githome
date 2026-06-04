package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"

	"github.com/tamnd/githome/store"
)

// mintPAT generates a classic PAT, stores its hash against userPK with the given
// scope header, and returns the plaintext to present to Authenticate.
func mintPAT(t *testing.T, f *fakeStore, userPK int64, scopes string) string {
	t.Helper()
	g, err := GenerateToken(PrefixClassicPAT)
	if err != nil {
		t.Fatal(err)
	}
	hash := g.Hash
	f.addToken(&store.TokenRow{
		UserPK:      &userPK,
		TokenHash:   hash[:],
		TokenPrefix: PrefixClassicPAT,
		LastEight:   g.Last8,
		Kind:        "pat",
		Scopes:      scopes,
	})
	return g.Plaintext
}

func TestAuthenticateAnonymous(t *testing.T) {
	svc := NewService(newFakeStore(), "https://git.test.internal")
	defer svc.Close()

	a, err := svc.Authenticate(context.Background(), "")
	if err != nil {
		t.Fatalf("empty header: unexpected error %v", err)
	}
	if a.IsAuthenticated() {
		t.Error("empty header should yield the anonymous actor")
	}
}

func TestAuthenticateValidPAT(t *testing.T) {
	f := newFakeStore()
	uid := f.addUser(&store.UserRow{Login: "octocat", Type: "User", SiteAdmin: true})
	tok := mintPAT(t, f, uid, "gist, repo")

	svc := NewService(f, "https://git.test.internal")
	defer svc.Close()

	for _, h := range []string{"token " + tok, "Bearer " + tok} {
		a, err := svc.Authenticate(context.Background(), h)
		if err != nil {
			t.Fatalf("%q: %v", h, err)
		}
		if !a.IsUser() || a.UserID != uid {
			t.Fatalf("%q: actor = %+v, want user %d", h, a, uid)
		}
		if a.UserLogin != "octocat" || !a.SiteAdmin {
			t.Errorf("%q: login/admin not carried: %+v", h, a)
		}
		if got := a.Scopes.Header(); got != "gist, repo" {
			t.Errorf("%q: scopes = %q, want %q", h, got, "gist, repo")
		}
		if a.RateKey == "" {
			t.Errorf("%q: empty RateKey", h)
		}
	}
}

func TestAuthenticateBasicWithTokenPassword(t *testing.T) {
	f := newFakeStore()
	uid := f.addUser(&store.UserRow{Login: "octocat", Type: "User"})
	tok := mintPAT(t, f, uid, "repo")

	svc := NewService(f, "https://git.test.internal")
	defer svc.Close()

	creds := base64.StdEncoding.EncodeToString([]byte("octocat:" + tok))
	a, err := svc.Authenticate(context.Background(), "Basic "+creds)
	if err != nil {
		t.Fatalf("basic auth with token password: %v", err)
	}
	if !a.IsUser() || a.UserID != uid {
		t.Fatalf("actor = %+v, want user %d", a, uid)
	}
}

func TestAuthenticateBadCredentials(t *testing.T) {
	f := newFakeStore()
	uid := f.addUser(&store.UserRow{Login: "octocat", Type: "User"})
	good := mintPAT(t, f, uid, "repo")

	svc := NewService(f, "https://git.test.internal")
	defer svc.Close()

	// A well-formed token whose final character was flipped, so its checksum
	// fails the offline verify before any database hit.
	bad := flipLast(good)

	cases := []string{
		"token not-a-real-token",
		"Bearer " + bad,
		"token ghp_000000000000000000000000000000000000", // valid shape, no such hash
	}
	for _, h := range cases {
		if _, err := svc.Authenticate(context.Background(), h); err != ErrBadCredentials {
			t.Errorf("%q: err = %v, want ErrBadCredentials", h, err)
		}
	}
}

// flipLast returns tok with its final character changed, breaking the checksum.
func flipLast(tok string) string {
	b := []byte(tok)
	last := len(b) - 1
	if b[last] == 'a' {
		b[last] = 'b'
	} else {
		b[last] = 'a'
	}
	return string(b)
}

func TestAuthenticateRevokedAndExpired(t *testing.T) {
	f := newFakeStore()
	uid := f.addUser(&store.UserRow{Login: "octocat", Type: "User"})
	svc := NewService(f, "https://git.test.internal")
	defer svc.Close()

	revoked := mintTokenWithLifecycle(t, f, uid, func(r *store.TokenRow) {
		now := time.Now()
		r.RevokedAt = &now
	})
	expired := mintTokenWithLifecycle(t, f, uid, func(r *store.TokenRow) {
		past := time.Now().Add(-time.Hour)
		r.ExpiresAt = &past
	})
	for name, tok := range map[string]string{"revoked": revoked, "expired": expired} {
		if _, err := svc.Authenticate(context.Background(), "token "+tok); err != ErrBadCredentials {
			t.Errorf("%s token: err = %v, want ErrBadCredentials", name, err)
		}
	}
}

func mintTokenWithLifecycle(t *testing.T, f *fakeStore, userPK int64, mut func(*store.TokenRow)) string {
	t.Helper()
	g, err := GenerateToken(PrefixClassicPAT)
	if err != nil {
		t.Fatal(err)
	}
	hash := g.Hash
	row := &store.TokenRow{
		UserPK:      &userPK,
		TokenHash:   hash[:],
		TokenPrefix: PrefixClassicPAT,
		Kind:        "pat",
	}
	mut(row)
	f.addToken(row)
	return g.Plaintext
}

func TestTokenHashIsSHA256(t *testing.T) {
	g, err := GenerateToken(PrefixOAuth)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(g.Plaintext))
	if HashToken(g.Plaintext) != want {
		t.Error("HashToken disagrees with sha256(plaintext)")
	}
}

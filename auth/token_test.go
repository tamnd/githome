package auth

import (
	"crypto/sha256"
	"strings"
	"testing"
)

func TestGenerateTokenRoundTrips(t *testing.T) {
	prefixes := []string{
		PrefixClassicPAT, PrefixOAuth, PrefixUserToSrv,
		PrefixInstall, PrefixRefresh, PrefixFineGrained,
	}
	for _, p := range prefixes {
		g, err := GenerateToken(p)
		if err != nil {
			t.Fatalf("GenerateToken(%q): %v", p, err)
		}
		if !strings.HasPrefix(g.Plaintext, p) {
			t.Errorf("%q: plaintext %q missing prefix", p, g.Plaintext)
		}
		if g.Prefix != p {
			t.Errorf("%q: Prefix = %q", p, g.Prefix)
		}
		if !VerifyChecksum(g.Plaintext) {
			t.Errorf("%q: VerifyChecksum rejected a freshly minted token", p)
		}
		if g.Hash != sha256.Sum256([]byte(g.Plaintext)) {
			t.Errorf("%q: Hash is not sha256(plaintext)", p)
		}
		if want := g.Plaintext[len(g.Plaintext)-8:]; g.Last8 != want {
			t.Errorf("%q: Last8 = %q, want %q", p, g.Last8, want)
		}
	}
}

func TestGenerateTokenRejectsUnknownPrefix(t *testing.T) {
	if _, err := GenerateToken("zzz_"); err == nil {
		t.Fatal("expected an error for an unknown prefix")
	}
}

func TestVerifyChecksumRejectsTampered(t *testing.T) {
	g, err := GenerateToken(PrefixClassicPAT)
	if err != nil {
		t.Fatal(err)
	}
	// Flip the final character so the presented checksum no longer matches.
	tok := []byte(g.Plaintext)
	last := len(tok) - 1
	if tok[last] == 'a' {
		tok[last] = 'b'
	} else {
		tok[last] = 'a'
	}
	if VerifyChecksum(string(tok)) {
		t.Error("VerifyChecksum accepted a tampered token")
	}
}

func TestVerifyChecksumRejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"not-a-token",
		"ghp_short",
		"xyz_0000000000000000000000000000000000",
	}
	for _, c := range cases {
		if VerifyChecksum(c) {
			t.Errorf("VerifyChecksum(%q) = true, want false", c)
		}
	}
}

func TestGeneratedTokensAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		g, err := GenerateToken(PrefixClassicPAT)
		if err != nil {
			t.Fatal(err)
		}
		if seen[g.Plaintext] {
			t.Fatalf("duplicate token generated: %q", g.Plaintext)
		}
		seen[g.Plaintext] = true
	}
}

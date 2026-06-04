// Package auth turns a raw HTTP credential into a normalized Actor and decides
// what that actor may do. M1 implements classic personal access tokens, the
// OAuth device flow, and the resolution path that backs GET /user; later
// milestones add fine-grained grants, GitHub Apps, and the repository
// authorizer.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"hash/crc32"
	"strings"

	"crypto/rand"
)

// Token class prefixes. The third letter classes the credential, matching
// GitHub's published token formats so a token minted here is detectable by the
// same secret-scanning rules.
const (
	PrefixClassicPAT  = "ghp_"        // personal access token (classic)
	PrefixFineGrained = "github_pat_" // fine-grained PAT
	PrefixOAuth       = "gho_"        // OAuth app user access token
	PrefixUserToSrv   = "ghu_"        // GitHub App user-to-server token
	PrefixInstall     = "ghs_"        // GitHub App installation token
	PrefixRefresh     = "ghr_"        // GitHub App refresh token
)

// base62Alphabet matches GitHub's so the body is regex-detectable by the same
// secret-scanning rules (gh[posru]_[0-9A-Za-z]{36}).
const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

const (
	bodyLen     = 30
	checksumLen = 6
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// Generated holds everything the store needs plus the one-time plaintext. The
// plaintext is shown to the user exactly once and never persisted.
type Generated struct {
	Plaintext string   // shown once, never stored
	Prefix    string   // "ghp_" etc.
	Hash      [32]byte // sha256(Plaintext); store Hash[:] in tokens.token_hash
	Last8     string   // last eight chars, for the settings UI
}

// GenerateToken mints a token with the given class prefix. It never touches the
// database; the caller persists the hash.
func GenerateToken(prefix string) (Generated, error) {
	switch prefix {
	case PrefixClassicPAT, PrefixFineGrained, PrefixOAuth, PrefixUserToSrv, PrefixInstall, PrefixRefresh:
	default:
		return Generated{}, fmt.Errorf("auth: unknown token prefix %q", prefix)
	}
	body, err := randBody()
	if err != nil {
		return Generated{}, err
	}
	plain := prefix + body + checksum(body)
	return Generated{
		Plaintext: plain,
		Prefix:    prefix,
		Hash:      sha256.Sum256([]byte(plain)),
		Last8:     plain[len(plain)-8:],
	}, nil
}

// VerifyChecksum validates a presented token's class prefix and CRC32 offline,
// so a malformed or mistyped token is rejected without a database hit.
func VerifyChecksum(token string) bool {
	prefix := classPrefix(token)
	if prefix == "" {
		return false
	}
	rest := token[len(prefix):]
	if len(rest) != bodyLen+checksumLen {
		return false
	}
	body, got := rest[:bodyLen], rest[bodyLen:]
	want := checksum(body)
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// HashToken returns the sha256 the store indexes tokens by. Every caller hashes
// through this so they agree byte-for-byte.
func HashToken(token string) [32]byte { return sha256.Sum256([]byte(token)) }

// classPrefix returns the class prefix of token, or "" if none matches. The
// long github_pat_ prefix is tested first so it is not shadowed.
func classPrefix(token string) string {
	for _, p := range []string{PrefixFineGrained, PrefixClassicPAT, PrefixOAuth, PrefixUserToSrv, PrefixInstall, PrefixRefresh} {
		if strings.HasPrefix(token, p) {
			return p
		}
	}
	return ""
}

// randBody returns bodyLen uniformly random base62 characters, rejecting the
// top of each byte's range to avoid modulo bias.
func randBody() (string, error) {
	buf := make([]byte, 0, bodyLen)
	for len(buf) < bodyLen {
		b := make([]byte, bodyLen)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		for _, x := range b {
			if x < 248 { // 248 = 4*62; reject the top 8 values
				buf = append(buf, base62Alphabet[x%62])
				if len(buf) == bodyLen {
					break
				}
			}
		}
	}
	return string(buf), nil
}

// checksum is the CRC32 (Castagnoli) of the body, base62-encoded and left-padded
// to checksumLen. It is integrity-only, not a secret.
func checksum(body string) string {
	sum := crc32.Checksum([]byte(body), castagnoli)
	c := base62Uint(uint64(sum))
	if len(c) > checksumLen {
		c = c[len(c)-checksumLen:]
	}
	return leftPad(c, checksumLen)
}

// base62Uint encodes n in base62, most-significant digit first, no padding.
func base62Uint(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [11]byte // ceil(log62(2^64)) = 11
	i := len(b)
	for n > 0 {
		i--
		b[i] = base62Alphabet[n%62]
		n /= 62
	}
	return string(b[i:])
}

func leftPad(s string, n int) string {
	for len(s) < n {
		s = "0" + s
	}
	return s
}

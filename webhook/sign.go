// Package webhook delivers a repository's recorded events to its registered
// hooks. It is the leaf consumer that turns an event into outgoing HTTP: it
// renders the delivery payload through the presenter, signs it, posts it behind
// an SSRF guard, and records the result. It may import domain, presenter, store,
// and worker because nothing in those packages imports it back.
package webhook

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"hash"
)

// Signatures holds the headers a webhook POST carries to prove the body was sent
// by a hook that knows the shared secret. Both are present when a secret is set;
// both are empty when it is not, and the delivery sends neither header.
type Signatures struct {
	SHA256 string // value of X-Hub-Signature-256, e.g. "sha256=<hex>"
	SHA1   string // value of X-Hub-Signature, e.g. "sha1=<hex>" (legacy)
}

// Sign computes the HMAC signatures over the exact request body using the hook's
// secret. The body must be the literal bytes that go on the wire: for a
// form-encoded delivery that is "payload=<urlencoded json>", not the bare JSON,
// so a receiver recomputing the digest over what it received matches. An empty
// secret yields empty signatures.
func Sign(secret string, body []byte) Signatures {
	if secret == "" {
		return Signatures{}
	}
	return Signatures{
		SHA256: "sha256=" + hexHMAC(sha256.New, secret, body),
		SHA1:   "sha1=" + hexHMAC(sha1.New, secret, body),
	}
}

// Verify reports whether sig is the valid X-Hub-Signature-256 value for body
// under secret. The comparison is constant time so a caller cannot learn the
// secret by timing rejections. An empty secret or a missing signature never
// verifies.
func Verify(secret, sig string, body []byte) bool {
	if secret == "" || sig == "" {
		return false
	}
	want := "sha256=" + hexHMAC(sha256.New, secret, body)
	return hmac.Equal([]byte(sig), []byte(want))
}

// hexHMAC returns the lowercase hex HMAC of body under secret using the given
// hash constructor, the encoding GitHub's signature headers use.
func hexHMAC(h func() hash.Hash, secret string, body []byte) string {
	mac := hmac.New(h, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

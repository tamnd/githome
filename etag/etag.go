// Package etag computes deterministic HTTP entity tags for Githome responses.
//
// GitHub serves weak validators (W/"..."), so Githome does too. An ETag is a
// hash over a resource's version inputs (such as a serialized body, or an
// updated-at plus lock-version pair), never over presentation details that vary
// between equal representations. The same inputs always yield the same tag,
// across processes and restarts.
package etag

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Weak returns a weak ETag over the given bytes, e.g. the serialized response
// body. The tag is the first 16 bytes of the SHA-256 digest, hex-encoded.
func Weak(body []byte) string {
	sum := sha256.Sum256(body)
	return `W/"` + hex.EncodeToString(sum[:16]) + `"`
}

// Version returns a weak ETag over a resource's version inputs. Callers pass a
// stable seed (the resource kind), its public id, and any monotonic version
// markers such as an updated-at timestamp in nanoseconds and a lock version.
func Version(seed string, id int64, markers ...int64) string {
	h := sha256.New()
	// Writes to a hash.Hash never error.
	_, _ = fmt.Fprintf(h, "%s:%d", seed, id)
	for _, m := range markers {
		_, _ = fmt.Fprintf(h, ":%d", m)
	}
	return `W/"` + hex.EncodeToString(h.Sum(nil)[:16]) + `"`
}

// Match reports whether an If-None-Match header value satisfies the given tag.
// It honors the comma-separated list form and the "*" wildcard, and compares
// weakly (ignoring the W/ marker), which is correct for GET and HEAD.
func Match(ifNoneMatch, tag string) bool {
	if ifNoneMatch == "" {
		return false
	}
	want := stripWeak(tag)
	for _, candidate := range splitList(ifNoneMatch) {
		if candidate == "*" || stripWeak(candidate) == want {
			return true
		}
	}
	return false
}

func stripWeak(tag string) string {
	if len(tag) >= 2 && tag[0] == 'W' && tag[1] == '/' {
		return tag[2:]
	}
	return tag
}

func splitList(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			tok := trimSpace(s[start:i])
			if tok != "" {
				out = append(out, tok)
			}
			start = i + 1
		}
	}
	return out
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

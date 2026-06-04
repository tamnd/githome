package nodeid

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
)

// Git-object node IDs differ from the resource IDs Encode handles: a commit,
// blob, or ref is identified by the repository it lives in together with its
// 40-hex object id, not by a single database id. GitHub encodes exactly that
// pair, prefixing with a short type tag ("C_" for a commit, "B_" for a blob,
// "REF_" for a reference). We mirror the shape so the values are opaque,
// reversible, and unique per (repo, oid). Clients treat them as opaque; the
// only contract is that DecodeGitObject inverts EncodeGitObject.
//
// The binary payload is a version byte (1, distinguishing it from the
// resource-id codec's version 0) followed by a varint repository database id
// and the raw object id bytes.

const gitObjectVersion = 1

// gitObjectPrefixes maps a git-object type tag to its node-ID prefix.
var gitObjectPrefixes = map[string]string{
	"commit": "C",
	"blob":   "B",
	"tree":   "T",
	"ref":    "REF",
}

var byGitObjectPrefix = func() map[string]string {
	m := map[string]string{}
	for tag, prefix := range gitObjectPrefixes {
		m[prefix] = tag
	}
	return m
}()

// EncodeGitObject builds the node ID for a git object of the given type tag
// ("commit", "blob", "tree", or "ref") in repository repoDBID with object id
// oid. An unknown tag yields the empty string.
func EncodeGitObject(typeTag string, repoDBID int64, oid string) string {
	prefix, ok := gitObjectPrefixes[typeTag]
	if !ok {
		return ""
	}
	buf := make([]byte, 0, 1+binary.MaxVarintLen64+len(oid))
	buf = append(buf, gitObjectVersion)
	var v [binary.MaxVarintLen64]byte
	n := binary.PutVarint(v[:], repoDBID)
	buf = append(buf, v[:n]...)
	buf = append(buf, oid...)
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(buf)
}

// DecodeGitObject recovers the type tag, repository database id, and object id
// from a git-object node ID.
func DecodeGitObject(nodeID string) (typeTag string, repoDBID int64, oid string, err error) {
	prefix, rest, ok := strings.Cut(nodeID, "_")
	if !ok {
		return "", 0, "", ErrInvalid
	}
	tag, known := byGitObjectPrefix[prefix]
	if !known {
		return "", 0, "", ErrInvalid
	}
	raw, derr := base64.RawURLEncoding.DecodeString(rest)
	if derr != nil || len(raw) < 2 || raw[0] != gitObjectVersion {
		return "", 0, "", ErrInvalid
	}
	id, n := binary.Varint(raw[1:])
	if n <= 0 {
		return "", 0, "", ErrInvalid
	}
	return tag, id, string(raw[1+n:]), nil
}

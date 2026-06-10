// Package nodeid encodes and decodes the opaque identifiers Githome uses for the
// GraphQL `id` field and the REST `node_id` field.
//
// Two formats are supported. The legacy format is base64(standard) of
// "<lenprefix>:<TypeName><dbid>", matching the historical GitHub scheme. The new
// format is "<prefix>_" followed by base64(url, no padding) of a compact binary
// payload. Clients treat node IDs as opaque, so the only hard contract is that
// Decode inverts Encode for both formats:
//
//	Decode(Encode(kind, dbID, format)) == (kind, dbID)
package nodeid

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Kind enumerates the node-bearing resource types.
type Kind int

// The node-bearing resource kinds, in registry order.
const (
	KindUser Kind = iota
	KindOrganization
	KindRepository
	KindIssue
	KindPullRequest
	KindIssueComment
	KindPullRequestReview
	KindPullRequestReviewComment
	KindPullRequestReviewThread
	KindCheckRun
	KindCheckSuite
	KindStatusContext
	KindLabel
	KindMilestone
	KindCommit
	KindReaction
	KindRelease
	KindReleaseAsset
)

// Format selects the encoding.
type Format int

const (
	// FormatNew is Githome's default typename-prefixed encoding.
	FormatNew Format = iota
	// FormatLegacy is the historical base64("len:TypeNameID") encoding.
	FormatLegacy
)

type entry struct {
	typeName  string // GraphQL type name, e.g. "PullRequest"
	legacyLen string // length prefix the legacy scheme placed before the type name
	newPrefix string // new-format prefix without the underscore, e.g. "PR"
}

var registry = map[Kind]entry{
	KindUser:                     {"User", "04", "U"},
	KindOrganization:             {"Organization", "012", "O"},
	KindRepository:               {"Repository", "010", "R"},
	KindIssue:                    {"Issue", "05", "I"},
	KindPullRequest:              {"PullRequest", "011", "PR"},
	KindIssueComment:             {"IssueComment", "012", "IC"},
	KindPullRequestReview:        {"PullRequestReview", "017", "PRR"},
	KindPullRequestReviewComment: {"PullRequestReviewComment", "024", "PRRC"},
	KindPullRequestReviewThread:  {"PullRequestReviewThread", "023", "PRRT"},
	KindCheckRun:                 {"CheckRun", "08", "CR"},
	KindCheckSuite:               {"CheckSuite", "010", "CS"},
	KindStatusContext:            {"StatusContext", "013", "SC"},
	KindLabel:                    {"Label", "05", "LA"},
	KindMilestone:                {"Milestone", "09", "MI"},
	KindCommit:                   {"Commit", "06", "C"},
	KindReaction:                 {"Reaction", "08", "RA"},
	KindRelease:                  {"Release", "07", "RE"},
	KindReleaseAsset:             {"ReleaseAsset", "012", "REA"},
}

// byNewPrefix and byTypeName are reverse lookups built once at init.
var (
	byNewPrefix = map[string]Kind{}
	byTypeName  = map[string]Kind{}
)

func init() {
	for k, e := range registry {
		byNewPrefix[e.newPrefix] = k
		byTypeName[e.typeName] = k
	}
}

// ErrInvalid is returned when a node ID cannot be decoded.
var ErrInvalid = errors.New("nodeid: invalid node id")

// Encode builds the node ID for (kind, dbID) in the requested format.
func Encode(kind Kind, dbID int64, format Format) string {
	e, ok := registry[kind]
	if !ok {
		return ""
	}
	if format == FormatLegacy {
		body := fmt.Sprintf("%s:%s%d", e.legacyLen, e.typeName, dbID)
		return base64.StdEncoding.EncodeToString([]byte(body))
	}
	var buf [1 + binary.MaxVarintLen64]byte
	buf[0] = 0 // payload version
	n := binary.PutVarint(buf[1:], dbID)
	return e.newPrefix + "_" + base64.RawURLEncoding.EncodeToString(buf[:1+n])
}

// Decode recovers (kind, dbID) from a node ID in either format.
func Decode(nodeID string) (Kind, int64, error) {
	if nodeID == "" {
		return 0, 0, ErrInvalid
	}
	if prefix, rest, ok := strings.Cut(nodeID, "_"); ok {
		if kind, known := byNewPrefix[prefix]; known {
			return decodeNew(kind, rest)
		}
	}
	return decodeLegacy(nodeID)
}

func decodeNew(kind Kind, rest string) (Kind, int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil || len(raw) < 2 {
		return 0, 0, ErrInvalid
	}
	if raw[0] != 0 {
		return 0, 0, ErrInvalid
	}
	id, n := binary.Varint(raw[1:])
	if n <= 0 {
		return 0, 0, ErrInvalid
	}
	return kind, id, nil
}

func decodeLegacy(nodeID string) (Kind, int64, error) {
	raw, err := base64.StdEncoding.DecodeString(nodeID)
	if err != nil {
		return 0, 0, ErrInvalid
	}
	_, body, ok := strings.Cut(string(raw), ":")
	if !ok {
		return 0, 0, ErrInvalid
	}
	// body is "<TypeName><dbid>"; find the longest registered type name prefix
	// whose remainder is all digits.
	for typeName, kind := range byTypeName {
		if !strings.HasPrefix(body, typeName) {
			continue
		}
		digits := body[len(typeName):]
		if digits == "" {
			continue
		}
		id, err := strconv.ParseInt(digits, 10, 64)
		if err != nil {
			continue
		}
		return kind, id, nil
	}
	return 0, 0, ErrInvalid
}

// refPrefix is the leading marker that distinguishes ref node IDs from the
// (Kind, int64) node IDs the Encode/Decode pair handles.
const refPrefix = "RF_"

// EncodeRef builds the node ID for a git reference: an RF_ prefix followed by
// a base64url encoding of the 8-byte big-endian repository PK concatenated with
// the full ref name (e.g. "refs/heads/main"). This is intentionally separate
// from Encode because refs do not have a database PK of their own.
func EncodeRef(repoPK int64, qualifiedName string) string {
	buf := make([]byte, 8+len(qualifiedName))
	binary.BigEndian.PutUint64(buf, uint64(repoPK))
	copy(buf[8:], qualifiedName)
	return refPrefix + base64.RawURLEncoding.EncodeToString(buf)
}

// DecodeRef recovers (repoPK, qualifiedName) from a node ID produced by
// EncodeRef. It returns ErrInvalid when the id does not begin with RF_ or
// decodes to fewer than 8 bytes.
func DecodeRef(nodeID string) (repoPK int64, qualifiedName string, err error) {
	s, ok := strings.CutPrefix(nodeID, refPrefix)
	if !ok {
		return 0, "", ErrInvalid
	}
	raw, decErr := base64.RawURLEncoding.DecodeString(s)
	if decErr != nil || len(raw) < 8 {
		return 0, "", ErrInvalid
	}
	return int64(binary.BigEndian.Uint64(raw[:8])), string(raw[8:]), nil
}

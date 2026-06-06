package store

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// IssueCursor is the opaque seek key for keyset-paginated issue lists. It
// encodes the (created_at, number) pair of the last item on the current page
// so the next page can be fetched with a single index seek rather than an
// OFFSET scan.
//
// The cursor is an implementation detail of the store package; callers treat
// the encoded string as opaque and round-trip it unchanged.
type IssueCursor struct {
	CreatedAt time.Time
	Number    int64
}

// EncodeCursor serialises the cursor to a URL-safe base64 string.
func EncodeCursor(c IssueCursor) string {
	raw := fmt.Sprintf("%d:%d", c.CreatedAt.UnixNano(), c.Number)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor parses a cursor returned by EncodeCursor. Any malformed input
// returns an error; callers fall back to OFFSET when decoding fails.
func DecodeCursor(s string) (IssueCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return IssueCursor{}, fmt.Errorf("store: bad cursor encoding: %w", err)
	}
	parts := strings.SplitN(string(b), ":", 2)
	if len(parts) != 2 {
		return IssueCursor{}, fmt.Errorf("store: bad cursor format")
	}
	ns, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return IssueCursor{}, fmt.Errorf("store: bad cursor timestamp: %w", err)
	}
	num, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return IssueCursor{}, fmt.Errorf("store: bad cursor number: %w", err)
	}
	return IssueCursor{
		CreatedAt: time.Unix(0, ns).UTC(),
		Number:    num,
	}, nil
}

package restmodel

import (
	"strconv"
	"time"
)

// Time is the timestamp scalar the REST API emits. GitHub renders timestamps as
// RFC3339 in UTC with a trailing Z and no fractional seconds (for example
// "2024-01-15T10:00:00Z"). A zero Time marshals to null so optional timestamps
// round-trip correctly.
type Time struct {
	time.Time
}

// NewTime wraps t for wire rendering.
func NewTime(t time.Time) Time { return Time{Time: t} }

// MarshalJSON renders the timestamp in GitHub's exact format, or null when zero.
func (t Time) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return []byte(strconv.Quote(t.UTC().Format("2006-01-02T15:04:05Z"))), nil
}

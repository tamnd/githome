package gqlmodel

import (
	"bytes"
	"testing"
	"time"
)

// TestDateTimeAcceptsOffsets confirms the DateTime scalar takes the offset
// forms GitHub accepts on input and normalizes them to UTC, while still
// rendering the Zulu form on output.
func TestDateTimeAcceptsOffsets(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"2011-10-05T14:48:00Z", "2011-10-05T14:48:00Z"},
		{"2011-10-05T14:48:00+07:00", "2011-10-05T07:48:00Z"},
		{"2011-10-05T14:48:00-04:30", "2011-10-05T19:18:00Z"},
		{"2011-10-05T14:48:00.123Z", "2011-10-05T14:48:00Z"},
	}
	for _, tc := range cases {
		var d DateTime
		if err := d.UnmarshalGQL(tc.in); err != nil {
			t.Errorf("UnmarshalGQL(%q): %v", tc.in, err)
			continue
		}
		var buf bytes.Buffer
		d.MarshalGQL(&buf)
		if got := buf.String(); got != `"`+tc.want+`"` {
			t.Errorf("DateTime(%q) renders %s, want %q", tc.in, got, tc.want)
		}
	}

	var d DateTime
	if err := d.UnmarshalGQL("yesterday"); err == nil {
		t.Error("UnmarshalGQL accepted a non-ISO string")
	}
}

// TestGitTimestampKeepsOffset confirms GitTimestamp renders the offset it
// parsed rather than converting to UTC, the way GitHub types git authoring
// dates.
func TestGitTimestampKeepsOffset(t *testing.T) {
	var g GitTimestamp
	if err := g.UnmarshalGQL("2011-10-05T14:48:00+07:00"); err != nil {
		t.Fatalf("UnmarshalGQL: %v", err)
	}
	var buf bytes.Buffer
	g.MarshalGQL(&buf)
	if got, want := buf.String(), `"2011-10-05T14:48:00+07:00"`; got != want {
		t.Errorf("GitTimestamp renders %s, want %s", got, want)
	}
}

// TestDateRoundTrips confirms the Date scalar carries a bare calendar date.
func TestDateRoundTrips(t *testing.T) {
	var d Date
	if err := d.UnmarshalGQL("2011-10-05"); err != nil {
		t.Fatalf("UnmarshalGQL: %v", err)
	}
	if !d.T.Equal(time.Date(2011, 10, 5, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Date parsed to %v", d.T)
	}
	var buf bytes.Buffer
	d.MarshalGQL(&buf)
	if got, want := buf.String(), `"2011-10-05"`; got != want {
		t.Errorf("Date renders %s, want %s", got, want)
	}
	if err := d.UnmarshalGQL("2011-10-05T14:48:00Z"); err == nil {
		t.Error("Date accepted a full timestamp")
	}
}

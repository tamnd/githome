// Package gqlmodel holds the hand-written Go types that back Githome's GraphQL
// object types and scalars. The api/graphql resolvers render into these structs
// so the presenter owns the GraphQL wire shape, mirroring how restmodel owns the
// REST wire shape. gqlgen autobinds object types to these structs and binds the
// custom scalars to the marshalers here.
package gqlmodel

import (
	"fmt"
	"io"
	"strconv"
	"time"
)

// timeLayout is the Zulu RFC 3339 form GitHub's DateTime scalar renders, the
// same layout restmodel.Time uses, so REST and GraphQL timestamps match.
const timeLayout = "2006-01-02T15:04:05Z"

// DateTime is GitHub's DateTime scalar: an ISO 8601 instant in UTC. It marshals
// to a quoted Zulu string and is used for non-null timestamp fields; nullable
// timestamps use *DateTime.
type DateTime struct{ T time.Time }

// NewDateTime wraps t as a DateTime.
func NewDateTime(t time.Time) DateTime { return DateTime{T: t} }

// MarshalGQL writes the timestamp as a quoted Zulu string.
func (d DateTime) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, strconv.Quote(d.T.UTC().Format(timeLayout)))
}

// UnmarshalGQL parses an ISO 8601 string back into a DateTime. GitHub accepts
// offset forms like 2011-10-05T14:48:00+07:00 on input and converts them to
// UTC, so the parse takes the full RFC 3339 grammar, not just the Zulu form
// the scalar renders.
func (d *DateTime) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("DateTime must be a string, got %T", v)
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return err
	}
	d.T = t.UTC()
	return nil
}

// URI is GitHub's URI scalar: an absolute URL rendered as a JSON string.
type URI string

// MarshalGQL writes the URI as a quoted string.
func (u URI) MarshalGQL(w io.Writer) { _, _ = io.WriteString(w, strconv.Quote(string(u))) }

// UnmarshalGQL reads a URI from a JSON string.
func (u *URI) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("URI must be a string, got %T", v)
	}
	*u = URI(s)
	return nil
}

// GitObjectID is GitHub's GitObjectID scalar: a git object's hex SHA.
type GitObjectID string

// MarshalGQL writes the object id as a quoted string.
func (g GitObjectID) MarshalGQL(w io.Writer) { _, _ = io.WriteString(w, strconv.Quote(string(g))) }

// UnmarshalGQL reads a GitObjectID from a JSON string.
func (g *GitObjectID) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("GitObjectID must be a string, got %T", v)
	}
	*g = GitObjectID(s)
	return nil
}

// HTML is GitHub's HTML scalar: a string containing rendered HTML.
type HTML string

// MarshalGQL writes the HTML as a quoted string.
func (h HTML) MarshalGQL(w io.Writer) { _, _ = io.WriteString(w, strconv.Quote(string(h))) }

// UnmarshalGQL reads an HTML value from a JSON string.
func (h *HTML) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("HTML must be a string, got %T", v)
	}
	*h = HTML(s)
	return nil
}

// GitTimestamp is GitHub's GitTimestamp scalar: an ISO 8601 instant that,
// unlike DateTime, keeps the offset the git object recorded instead of
// converting to UTC.
type GitTimestamp struct{ T time.Time }

// MarshalGQL writes the timestamp as a quoted RFC 3339 string in its own zone.
func (g GitTimestamp) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, strconv.Quote(g.T.Format(time.RFC3339)))
}

// UnmarshalGQL parses an ISO 8601 string, offset preserved.
func (g *GitTimestamp) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("GitTimestamp must be a string, got %T", v)
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return err
	}
	g.T = t
	return nil
}

// dateLayout is the calendar-date form GitHub's Date scalar carries.
const dateLayout = "2006-01-02"

// Date is GitHub's Date scalar: an ISO 8601 calendar date with no time part.
type Date struct{ T time.Time }

// MarshalGQL writes the date as a quoted YYYY-MM-DD string.
func (d Date) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, strconv.Quote(d.T.Format(dateLayout)))
}

// UnmarshalGQL parses a YYYY-MM-DD string.
func (d *Date) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("Date must be a string, got %T", v)
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return err
	}
	d.T = t
	return nil
}

// GitSSHRemote is GitHub's GitSSHRemote scalar: a git SSH clone address like
// git@host:owner/name.git.
type GitSSHRemote string

// MarshalGQL writes the remote as a quoted string.
func (g GitSSHRemote) MarshalGQL(w io.Writer) { _, _ = io.WriteString(w, strconv.Quote(string(g))) }

// UnmarshalGQL reads a GitSSHRemote from a JSON string.
func (g *GitSSHRemote) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("GitSSHRemote must be a string, got %T", v)
	}
	*g = GitSSHRemote(s)
	return nil
}

// BigInt is GitHub's BigInt scalar: an integer too wide for Int, carried as a
// JSON string. fullDatabaseId is typed with it.
type BigInt string

// MarshalGQL writes the integer as a quoted string.
func (b BigInt) MarshalGQL(w io.Writer) { _, _ = io.WriteString(w, strconv.Quote(string(b))) }

// UnmarshalGQL reads a BigInt from a JSON string.
func (b *BigInt) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("BigInt must be a string, got %T", v)
	}
	*b = BigInt(s)
	return nil
}

// Base64String is GitHub's Base64String scalar: a base64-encoded string, the
// type blob text and tarball payloads ride in.
type Base64String string

// MarshalGQL writes the payload as a quoted string.
func (b Base64String) MarshalGQL(w io.Writer) { _, _ = io.WriteString(w, strconv.Quote(string(b))) }

// UnmarshalGQL reads a Base64String from a JSON string.
func (b *Base64String) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("Base64String must be a string, got %T", v)
	}
	*b = Base64String(s)
	return nil
}

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

// UnmarshalGQL parses a Zulu string back into a DateTime. DateTime is an output
// scalar in the M2 schema, so this exists only to satisfy gqlgen's scalar
// binding, which requires both directions.
func (d *DateTime) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("DateTime must be a string, got %T", v)
	}
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return err
	}
	d.T = t
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

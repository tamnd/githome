package store

import (
	"database/sql"
	"fmt"
	"time"
)

// The two dialects hand back different Go types for timestamps and booleans:
// pgx returns time.Time and bool, modernc/sqlite returns a TEXT string and an
// INTEGER. The Scanner adapters here absorb that difference so every query path
// is dialect-agnostic, and the arg* helpers bind nullable pointers without each
// call site juggling sql.Null* wrappers.

// timeLayouts are tried in order when parsing a SQLite timestamp string. The
// bare "2006-01-02 15:04:05" form is what CURRENT_TIMESTAMP emits (UTC, no zone);
// the offset forms cover values modernc round-trips from a bound time.Time.
var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	// modernc/sqlite renders a bound time.Time with its String() layout, e.g.
	// "2026-06-04 14:22:32 +0000 UTC", so a value we wrote round-trips back.
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05 -0700 MST",
}

// nullTime scans a timestamp from either dialect and tracks NULL.
type nullTime struct {
	Time  time.Time
	Valid bool
}

func (n *nullTime) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		n.Time, n.Valid = time.Time{}, false
		return nil
	case time.Time:
		n.Time, n.Valid = v.UTC(), true
		return nil
	case []byte:
		return n.parse(string(v))
	case string:
		return n.parse(v)
	default:
		return fmt.Errorf("store: cannot scan %T into time", src)
	}
}

func (n *nullTime) parse(s string) error {
	if s == "" {
		n.Time, n.Valid = time.Time{}, false
		return nil
	}
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			n.Time, n.Valid = t.UTC(), true
			return nil
		}
	}
	return fmt.Errorf("store: unrecognized time %q", s)
}

// ptr returns a *time.Time, nil when the value was NULL.
func (n nullTime) ptr() *time.Time {
	if !n.Valid {
		return nil
	}
	t := n.Time
	return &t
}

// boolVal scans a boolean from either dialect (Postgres bool, SQLite int64).
type boolVal struct {
	Bool  bool
	Valid bool
}

func (b *boolVal) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		b.Bool, b.Valid = false, false
	case bool:
		b.Bool, b.Valid = v, true
	case int64:
		b.Bool, b.Valid = v != 0, true
	case []byte:
		b.Bool, b.Valid = len(v) == 1 && v[0] != '0', true
	case string:
		b.Bool, b.Valid = v == "1" || v == "true" || v == "t", true
	default:
		return fmt.Errorf("store: cannot scan %T into bool", src)
	}
	return nil
}

// ptr returns a *bool, nil when the value was NULL.
func (b boolVal) ptr() *bool {
	if !b.Valid {
		return nil
	}
	v := b.Bool
	return &v
}

// argStr binds a nullable string: a nil pointer becomes SQL NULL.
func argStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// argBool binds a nullable bool.
func argBool(p *bool) any {
	if p == nil {
		return nil
	}
	return *p
}

// argTime binds a nullable timestamp. It normalizes to UTC, which also strips
// any monotonic clock reading a time.Now()-derived value carries: without this
// modernc renders the bound value via time.Time.String() as e.g.
// "2026-06-13 11:12:07 +0700 +07 m=+3600", a form no scan layout parses.
func argTime(p *time.Time) any {
	if p == nil {
		return nil
	}
	return p.UTC()
}

// sqliteTimeFmt is the format CURRENT_TIMESTAMP uses in SQLite: no timezone
// suffix, UTC assumed. Comparing a bound time.Time (which modernc renders with
// a " +0000 UTC" suffix) against a column stored in this format always returns
// less-than for every row because the shorter string is lexicographically
// smaller. Formatting explicitly in this layout makes the strings match.
const sqliteTimeFmt = "2006-01-02 15:04:05"

// timeArg returns t formatted for a bound query parameter. For SQLite it uses
// the bare YYYY-MM-DD HH:MM:SS layout that CURRENT_TIMESTAMP stores; for
// Postgres it passes time.Time directly so pgx handles timezone encoding.
func (s *Store) timeArg(t time.Time) any {
	if s.dialect == DialectSQLite {
		return t.UTC().Format(sqliteTimeFmt)
	}
	return t
}

// affectedOrNotFound maps an UPDATE/DELETE that touched no row to ErrNotFound,
// so a call against a missing primary key reads as a not-found rather than a
// silent success.
func affectedOrNotFound(res sql.Result) error {
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// strPtr converts a scanned sql.NullString to *string.
func strPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

// i64Ptr converts a scanned sql.NullInt64 to *int64.
func i64Ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

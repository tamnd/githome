package search

import "strings"

// Search strategy
//
// Githome runs search directly against the authoritative tables with portable
// LIKE predicates rather than a dedicated index: an issue or repository search
// is a filtered scan, and a code search walks the matching repositories' git
// trees. This keeps the metadata store as the single source of truth and avoids
// a second store to migrate and keep consistent.
//
// The query language and result envelope are the contract; the execution behind
// them is not. A future backend (SQLite FTS5, Postgres tsvector, a bleve or
// Zoekt index) can replace the scan without changing this package's parse
// output or the REST/GraphQL shapes, because such an index is rebuildable from
// the tables and never holds data the tables do not. The normalization helpers
// below give every backend the same notion of which fields a term matches, how
// results sort, and how a relevance score is derived.

// Field is a target a free-text term matches against. The in: qualifier selects
// it; with no in: qualifier a term matches the default fields for the search
// kind (title and body for issues, name and description for repositories).
type Field int

// The fields a term can match: an issue's title or body, or its comment text.
const (
	FieldTitle Field = iota
	FieldBody
	FieldComments
)

// Fields reads the in: qualifiers into the set of fields a term must match,
// falling back to def when none are given. An unrecognized in: value is
// ignored, the way GitHub drops a qualifier value it does not understand.
func Fields(q Query, def ...Field) []Field {
	var out []Field
	for _, v := range q.Values("in") {
		switch strings.ToLower(v) {
		case "title":
			out = append(out, FieldTitle)
		case "body":
			out = append(out, FieldBody)
		case "comments":
			out = append(out, FieldComments)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}

// NormalizeOrder maps a direction query parameter to "asc" or "desc",
// defaulting to descending, which is GitHub's default for every sort.
func NormalizeOrder(order string) string {
	if strings.EqualFold(order, "asc") {
		return "asc"
	}
	return "desc"
}

// Score is the relevance value attached to a result item. The portable-scan
// backend does not rank, so every match scores the same 1.0; the field exists
// so the wire shape carries GitHub's score and a ranking backend can fill it
// without a contract change.
func Score() float64 { return 1.0 }

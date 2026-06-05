// Package search parses GitHub's search query mini-language into the structured
// form the domain resolves and the store filters on. It is pure: it touches no
// store, no git, and no HTTP, so the same raw query string always parses to the
// same Query. The domain layer turns the parsed qualifiers into store filters
// (resolving logins and repo names to ids); this package only tokenizes.
package search

import "strings"

// Query is a parsed search string: the free-text terms a result must contain
// and the qualifier:value pairs that narrow the set. A qualifier may repeat
// (label:bug label:urgent), so its values are a slice. Keys are lowercased; the
// values keep their original case because logins and repo names are matched
// case-insensitively downstream but rendered as written.
type Query struct {
	Terms      []string
	Qualifiers map[string][]string
}

// Parse splits a raw search string into terms and qualifiers. A token shaped
// key:value whose key is a recognized qualifier becomes a qualifier; every
// other token is a free-text term. Double quotes group spaces into one token,
// so `label:"help wanted"` and `"exact phrase"` parse as a single value or
// term. An unterminated quote runs to the end of the string, matching how
// GitHub tolerates a trailing quote.
func Parse(raw string) Query {
	q := Query{Qualifiers: map[string][]string{}}
	for _, tok := range tokenize(raw) {
		key, val, ok := splitQualifier(tok)
		if ok && knownQualifiers[key] {
			q.Qualifiers[key] = append(q.Qualifiers[key], val)
			continue
		}
		q.Terms = append(q.Terms, tok)
	}
	return q
}

// First returns the first value of a qualifier and whether it was present.
func (q Query) First(key string) (string, bool) {
	vs := q.Qualifiers[key]
	if len(vs) == 0 {
		return "", false
	}
	return vs[0], true
}

// Values returns every value given for a qualifier, in query order.
func (q Query) Values(key string) []string { return q.Qualifiers[key] }

// Text joins the free-text terms back into a single space-separated string, the
// form a free-text-only backend wants.
func (q Query) Text() string { return strings.Join(q.Terms, " ") }

// knownQualifiers is the set of qualifier keys Parse recognizes. A key:value
// token whose key is not here stays a free-text term (so a URL or a time of day
// is not mistaken for a qualifier).
var knownQualifiers = map[string]bool{
	"repo": true, "user": true, "org": true, "author": true, "assignee": true,
	"label": true, "state": true, "is": true, "in": true, "milestone": true,
	"type": true, "language": true, "created": true, "updated": true,
	"fork": true, "archived": true,
}

// splitQualifier splits a token at its first colon into a lowercased key and
// its value, reporting whether the token had the key:value shape with a
// non-empty key. A leading colon (":x") or no colon returns ok=false.
func splitQualifier(tok string) (key, val string, ok bool) {
	i := strings.IndexByte(tok, ':')
	if i <= 0 {
		return "", "", false
	}
	return strings.ToLower(tok[:i]), tok[i+1:], true
}

// tokenize splits s on unquoted whitespace, honoring double quotes so a quoted
// run is one token with its surrounding quotes removed. Quotes after a
// qualifier key (label:"help wanted") are handled because the split happens
// after tokenizing, so the value carries through with its quotes already
// stripped from the run.
func tokenize(s string) []string {
	var (
		out    []string
		cur    strings.Builder
		quoted bool
		have   bool // cur holds a started token, even if empty (a "" literal)
	)
	flush := func() {
		if have || cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
			have = false
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			have = true
		case (r == ' ' || r == '\t' || r == '\n' || r == '\r') && !quoted:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

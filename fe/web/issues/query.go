package issues

import (
	"strconv"
	"strings"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/view"
)

// query.go owns the ?q= mini-language for the issues index: the parse, the
// server-side composition the filter chips and tabs use, and the projection into
// the same domain.IssueQuery the REST list endpoint builds, so the page and the
// API never disagree about what a filter means. It is pure: it imports domain for
// the query struct and fe/view for the viewer login (the only viewer-relative
// rewrite), and it touches no store, git, or net/http. See implementation/08
// section 2.
//
// As-built scope. The domain issue query the binary actually exposes
// (domain.IssueQuery) filters on state, labels, creator, assignee, milestone
// number, and sort. The grammar parses the full github.com vocabulary so a
// human's query is never rejected, but Filter only projects the qualifiers the
// domain supports today: is:, author:, assignee:, label:, milestone: (numeric),
// and sort:. Everything else (no:, reason:, in:, date ranges, free text) is kept
// in the parsed Query so the search box round-trips it and the composer
// preserves it, but it does not yet narrow the result set. The deferral is
// recorded in section 2 of the spec rather than silently dropped.

// Query is the parsed and re-composable ?q= filter. It is the single source of
// truth for the issues index filter: the index handler parses it once, the
// templates call its composer methods to build dropdown and chip hrefs
// server-side, and Filter projects it to the domain query the API also uses.
type Query struct {
	Raw   string      // the literal q string, for the search input value
	State string      // "open" | "closed" | "" (unset, treated as open default)
	Type  string      // "issue" (this index) | "pr" (the handler redirects to /pulls)
	Terms []string    // bare full-text words
	Quals []Qualifier // every key:value, in source order, negation preserved
}

// Qualifier is one key:value token, with the leading '-' negation preserved so
// the composer can round-trip a query it does not itself project.
type Qualifier struct {
	Key    string
	Value  string
	Negate bool
}

// defaultSort is the canonical sort the index uses when no sort: qualifier is
// present, and the value SetSort omits so the bare default stays the clean URL.
const defaultSort = "created-desc"

// ParseQuery tokenizes the raw q string into a Query. It never errors: it cannot,
// because a browser can submit anything. A whitespace-only q yields the default.
func ParseQuery(raw string) *Query {
	q := &Query{Raw: strings.TrimSpace(raw), Type: "issue"}
	for _, tok := range tokenize(q.Raw) {
		negate := false
		body := tok
		if strings.HasPrefix(body, "-") && len(body) > 1 {
			negate = true
			body = body[1:]
		}
		key, val, ok := splitQualifier(body)
		if !ok {
			// A bare word (or a lone '-') is a full-text term, kept verbatim.
			q.Terms = append(q.Terms, tok)
			continue
		}
		switch key {
		case "is":
			switch val {
			case "open", "closed":
				q.State = val
			case "issue":
				q.Type = "issue"
			case "pr", "pull-request":
				q.Type = "pr"
			default:
				q.Quals = append(q.Quals, Qualifier{Key: key, Value: val, Negate: negate})
			}
		case "state":
			if val == "open" || val == "closed" {
				q.State = val
			} else {
				q.Quals = append(q.Quals, Qualifier{Key: key, Value: val, Negate: negate})
			}
		default:
			q.Quals = append(q.Quals, Qualifier{Key: key, Value: val, Negate: negate})
		}
	}
	return q
}

// DefaultQuery is what an index visited with no ?q= behaves as, and what the
// "clear filters" link restores (implementation/08 section 1.6, 2.1).
func DefaultQuery() *Query { return ParseQuery("is:issue is:open") }

// tokenize splits a q string on whitespace while keeping a double-quoted run
// (including one that follows a key:) as a single token. An unterminated quote
// runs to the end of the string rather than erroring.
func tokenize(s string) []string {
	var toks []string
	var b strings.Builder
	inQuote := false
	flush := func() {
		if b.Len() > 0 {
			toks = append(toks, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case (r == ' ' || r == '\t' || r == '\n') && !inQuote:
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return toks
}

// splitQualifier splits a token into key and value at the first colon. It reports
// false when there is no colon or the key is empty, so the caller treats the
// token as a free-text term.
func splitQualifier(tok string) (key, val string, ok bool) {
	i := strings.IndexByte(tok, ':')
	if i <= 0 {
		return "", "", false
	}
	return strings.ToLower(tok[:i]), tok[i+1:], true
}

// state returns the effective state for filtering and tab highlighting: an unset
// State defaults to open, matching is:issue is:open.
func (q *Query) state() string {
	if q.State == "" {
		return "open"
	}
	return q.State
}

// firstValue returns the value of the first non-negated qualifier with the given
// key, and whether one was present.
func (q *Query) firstValue(key string) (string, bool) {
	for _, ql := range q.Quals {
		if ql.Key == key && !ql.Negate {
			return ql.Value, true
		}
	}
	return "", false
}

// hasNo reports whether a no:<what> qualifier is present (no:assignee,
// no:milestone, no:label). It is preserved across composition even though the
// domain query does not yet project it.
func (q *Query) hasNo(what string) bool {
	for _, ql := range q.Quals {
		if ql.Key == "no" && ql.Value == what {
			return true
		}
	}
	return false
}

// labels returns the label: values in source order, the AND set the index shows
// as active chips.
func (q *Query) labels() []string {
	var out []string
	for _, ql := range q.Quals {
		if ql.Key == "label" && !ql.Negate {
			out = append(out, ql.Value)
		}
	}
	return out
}

// sortKey returns the effective sort, defaulting to the canonical created-desc.
func (q *Query) sortKey() string {
	if v, ok := q.firstValue("sort"); ok && v != "" {
		return v
	}
	return defaultSort
}

// Filter projects the parsed Query into the domain.IssueQuery the REST list
// endpoint (implementation/02 section 4.7) also builds, so the UI and API agree
// field for field on what a query selects. @me resolves to the viewer login here,
// the only viewer-relative rewrite, so the domain query carries a concrete login.
// Qualifiers the domain does not model are left in the Query for round-tripping
// but do not narrow the result (see the as-built note at the top of this file).
func (q *Query) Filter(viewer *view.Viewer) domain.IssueQuery {
	dq := domain.IssueQuery{State: q.state(), Labels: q.labels()}
	if v, ok := q.firstValue("author"); ok {
		dq.CreatorLogin = rewriteMe(v, viewer)
	}
	if v, ok := q.firstValue("assignee"); ok {
		dq.AssigneeLogin = rewriteMe(v, viewer)
	}
	if v, ok := q.firstValue("milestone"); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			dq.MilestoneNumber = &n
		}
	}
	dq.Sort, dq.Direction = splitSort(q.sortKey())
	return dq
}

// rewriteMe resolves the @me alias to the signed-in viewer's login. An anonymous
// viewer leaves @me as a literal, which resolves to no account and so matches
// nothing, the honest answer for a filtered-by-me view with nobody signed in.
func rewriteMe(login string, viewer *view.Viewer) string {
	if login == "@me" && viewer != nil {
		return viewer.Login
	}
	return login
}

// splitSort turns a sort key like "created-desc" or "comments-asc" into the
// domain field and direction. A key with no direction suffix defaults to desc, an
// unknown field falls back to the default created-desc.
func splitSort(key string) (field, dir string) {
	field, dir = key, "desc"
	if i := strings.LastIndexByte(key, '-'); i > 0 {
		if suf := key[i+1:]; suf == "asc" || suf == "desc" {
			field, dir = key[:i], suf
		}
	}
	switch field {
	case "created", "updated", "comments":
		return field, dir
	default:
		return "created", "desc"
	}
}

package issues

import (
	"net/url"
	"sort"
	"strings"
)

// compose.go holds the Query composer methods the index templates call to build
// filter hrefs server-side, so the state tabs, the label chips, and the
// dropdowns work with no JavaScript. Each method is a surgical edit to one
// qualifier that preserves every other token, and returns the full ?q=... query
// string for an <a href>. The <auto-complete> upgrade (doc 05) navigates to the
// same computed href, so the JS and no-JS paths land on identical URLs. See
// implementation/08 section 2.2.

// clone returns a deep copy so a composer method never mutates the parsed Query
// the handler still holds for the current render.
func (q *Query) clone() *Query {
	cp := &Query{Raw: q.Raw, State: q.State, Type: q.Type}
	cp.Terms = append(cp.Terms, q.Terms...)
	cp.Quals = append(cp.Quals, q.Quals...)
	return cp
}

// compose serializes a Query back to a canonical q string: is:issue, the state,
// then the qualifiers in source order, then the free-text terms. The default sort
// is omitted so a bare open query stays the clean canonical URL.
func (q *Query) compose() string {
	var parts []string
	parts = append(parts, "is:issue")
	parts = append(parts, "is:"+q.state())
	for _, ql := range q.Quals {
		if ql.Key == "sort" && ql.Value == defaultSort {
			continue
		}
		tok := ql.Key + ":" + quoteIfNeeded(ql.Value)
		if ql.Negate {
			tok = "-" + tok
		}
		parts = append(parts, tok)
	}
	parts = append(parts, q.Terms...)
	return strings.Join(parts, " ")
}

// href is the full ?q=... query string for a composed Query, URL-encoded so a
// value with spaces or quotes stays one q parameter.
func (q *Query) href() string {
	return "?" + url.Values{"q": {q.compose()}}.Encode()
}

// quoteIfNeeded wraps a value in double quotes when it contains whitespace, so a
// multi-word label or milestone title survives the round trip through tokenize.
func quoteIfNeeded(v string) string {
	if strings.ContainsAny(v, " \t\n") {
		return `"` + v + `"`
	}
	return v
}

// setQual replaces every qualifier with the given key by a single one, or appends
// it when none was present. It is the basis for the single-value pickers.
func (q *Query) setQual(key, val string) {
	out := q.Quals[:0:0]
	replaced := false
	for _, ql := range q.Quals {
		if ql.Key == key {
			if !replaced {
				out = append(out, Qualifier{Key: key, Value: val})
				replaced = true
			}
			continue
		}
		out = append(out, ql)
	}
	if !replaced {
		out = append(out, Qualifier{Key: key, Value: val})
	}
	q.Quals = out
}

// dropQual removes every qualifier with the given key (optionally only those with
// a matching value when val is non-empty).
func (q *Query) dropQual(key, val string) {
	out := q.Quals[:0:0]
	for _, ql := range q.Quals {
		if ql.Key == key && (val == "" || ql.Value == val) {
			continue
		}
		out = append(out, ql)
	}
	q.Quals = out
}

// WithState flips between is:open and is:closed, keeping every other token. The
// state tabs link here.
func (q *Query) WithState(s string) string {
	cp := q.clone()
	cp.State = s
	return cp.href()
}

// AddLabel appends a label: qualifier (labels are an AND set, so repeats stack).
// Re-adding a present label is a no-op so a chip click stays idempotent.
func (q *Query) AddLabel(name string) string {
	cp := q.clone()
	for _, l := range cp.labels() {
		if l == name {
			return cp.href()
		}
	}
	cp.Quals = append(cp.Quals, Qualifier{Key: "label", Value: name})
	return cp.href()
}

// RemoveLabel drops a label: qualifier, the active-chip dismiss link.
func (q *Query) RemoveLabel(name string) string {
	cp := q.clone()
	cp.dropQual("label", name)
	return cp.href()
}

// SetAuthor replaces any author: qualifier with the given login.
func (q *Query) SetAuthor(login string) string {
	cp := q.clone()
	cp.setQual("author", login)
	return cp.href()
}

// SetAssignee replaces any assignee: qualifier with the given login and clears a
// no:assignee that would contradict it.
func (q *Query) SetAssignee(login string) string {
	cp := q.clone()
	cp.dropQual("no", "assignee")
	cp.setQual("assignee", login)
	return cp.href()
}

// NoAssignee sets no:assignee and drops any concrete assignee:.
func (q *Query) NoAssignee() string {
	cp := q.clone()
	cp.dropQual("assignee", "")
	cp.dropQual("no", "assignee")
	cp.Quals = append(cp.Quals, Qualifier{Key: "no", Value: "assignee"})
	return cp.href()
}

// SetMilestone replaces any milestone: qualifier with the given title and clears
// a contradicting no:milestone.
func (q *Query) SetMilestone(title string) string {
	cp := q.clone()
	cp.dropQual("no", "milestone")
	cp.setQual("milestone", title)
	return cp.href()
}

// NoMilestone sets no:milestone and drops any concrete milestone:.
func (q *Query) NoMilestone() string {
	cp := q.clone()
	cp.dropQual("milestone", "")
	cp.dropQual("no", "milestone")
	cp.Quals = append(cp.Quals, Qualifier{Key: "no", Value: "milestone"})
	return cp.href()
}

// SetSort sets the sort: qualifier, omitting it entirely when it equals the
// default so the canonical default URL stays bare.
func (q *Query) SetSort(key string) string {
	cp := q.clone()
	cp.dropQual("sort", "")
	if key != defaultSort && key != "" {
		cp.Quals = append(cp.Quals, Qualifier{Key: "sort", Value: key})
	}
	return cp.href()
}

// Encode is the URL-encoded q value alone (no leading ?), for a caller that
// composes the query string itself.
func (q *Query) Encode() string {
	return q.compose()
}

// ActiveLabels returns the active label set sorted for a stable chip order.
func (q *Query) ActiveLabels() []string {
	ls := q.labels()
	sort.Strings(ls)
	return ls
}

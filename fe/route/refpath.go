package route

import "strings"

// RefMatch classifies what the leading segments of a ref-path tail resolved
// to. The caller uses it to build the unambiguous revision for the git layer:
// a bare name is ambiguous down there too, and git's own rev-parse order
// prefers the tag when a name is both a branch and a tag, while the web
// routes promise the branch (GitHub's documented precedence). Qualifying the
// revision from the match kind pins that choice.
type RefMatch int

const (
	// RefNone marks a tail whose leading segments name nothing.
	RefNone RefMatch = iota
	// RefBranch marks a branch name (refs/heads/...).
	RefBranch
	// RefTag marks a tag name (refs/tags/...).
	RefTag
	// RefCommit marks a commit-ish that is neither: a full or abbreviated
	// object id, or the symbolic HEAD.
	RefCommit
)

// RefLookup answers whether a candidate names a branch, a tag, or another
// commit-ish in the repository. The caller backs it with the repository's ref
// set so the split stays free of per-candidate git reads.
type RefLookup interface {
	Branch(name string) bool
	Tag(name string) bool
	Commitish(rev string) bool
}

// SplitRefPath separates a "<ref>/<path>" tail into its git ref and the file
// path within that ref. A ref can contain slashes (a branch named
// release/1.0), so the tail is ambiguous on its own: "main/cmd/foo" could be
// ref "main" path "cmd/foo", or a branch "main/cmd" path "foo". Git resolves
// this by preferring the longest leading segment sequence that names an
// existing ref, so the split tries the whole tail first and peels one
// trailing segment at a time into the path.
//
// A fully qualified tail (refs/heads/<branch>/... or refs/tags/<tag>/...)
// names its kind explicitly and resolves to the short name, so the page links
// it builds are the short form. When a name is both a branch and a tag, the
// branch wins (kind precedence within each candidate length: branch, then
// tag, then commit-ish). ok is false when no leading sequence names a ref,
// which the handler renders as a 404. See implementation/07 section 4.
func SplitRefPath(tail string, refs RefLookup) (ref string, kind RefMatch, path string, ok bool) {
	tail = strings.Trim(tail, "/")
	if tail == "" {
		return "", RefNone, "", false
	}
	segs := strings.Split(tail, "/")
	// The qualified forms first: they say outright which namespace the name
	// lives in, so only that namespace is consulted. A miss falls through to
	// the unqualified walk, since a branch literally named "refs/..." is legal.
	if len(segs) >= 3 && segs[0] == "refs" {
		for i := len(segs); i >= 3; i-- {
			name := strings.Join(segs[2:i], "/")
			rest := strings.Join(segs[i:], "/")
			switch segs[1] {
			case "heads":
				if refs.Branch(name) {
					return name, RefBranch, rest, true
				}
			case "tags":
				if refs.Tag(name) {
					return name, RefTag, rest, true
				}
			}
		}
	}
	for i := len(segs); i >= 1; i-- {
		candidate := strings.Join(segs[:i], "/")
		rest := strings.Join(segs[i:], "/")
		switch {
		case refs.Branch(candidate):
			return candidate, RefBranch, rest, true
		case refs.Tag(candidate):
			return candidate, RefTag, rest, true
		case refs.Commitish(candidate):
			return candidate, RefCommit, rest, true
		}
	}
	return "", RefNone, "", false
}

// FirstSegment returns the first path segment of p and the remainder, both
// without leading or trailing slashes. It is the small helper the top-level
// dispatcher uses to peel "/{owner}/..." without allocating a full split when
// only the head matters.
func FirstSegment(p string) (head, rest string) {
	p = strings.TrimPrefix(p, "/")
	head, rest, _ = strings.Cut(p, "/")
	return head, rest
}

// ParseBaseHead splits the basehead parameter of a compare URL into the base
// and head branch names. The canonical form is "base...head"; when there is no
// "...", the whole string is returned as the head with an empty base so the
// caller can substitute the repository's default branch.
func ParseBaseHead(s string) (base, head string, ok bool) {
	if s == "" {
		return "", "", false
	}
	if i := strings.Index(s, "..."); i >= 0 {
		b, h := s[:i], s[i+3:]
		if b == "" || h == "" {
			return "", "", false
		}
		return b, h, true
	}
	return "", s, true
}

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

// SplitPatchSuffix peels the .diff or .patch suffix off a path tail: the
// commit, compare, and pull URLs all grow a plain-text twin by suffix
// (github.com's /commit/{sha}.diff family). ok is false when the tail carries
// neither suffix, or nothing but the suffix; rest is then the input unchanged
// so the caller falls through to the HTML page.
func SplitPatchSuffix(s string) (rest, format string, ok bool) {
	switch {
	case strings.HasSuffix(s, ".diff"):
		rest, format = strings.TrimSuffix(s, ".diff"), "diff"
	case strings.HasSuffix(s, ".patch"):
		rest, format = strings.TrimSuffix(s, ".patch"), "patch"
	default:
		return s, "", false
	}
	if rest == "" {
		return s, "", false
	}
	return rest, format, true
}

// CompareSide is one side of a compare range. Owner and Repo are the optional
// qualifiers of the owner:ref and owner:repo:ref forms; both empty means the
// ref lives in the repository the URL names.
type CompareSide struct {
	Owner string
	Repo  string
	Ref   string
}

// CompareSpec is a parsed compare range. TwoDot marks the "base..head" form,
// the direct diff between the two ends; the canonical "base...head" form
// diffs head against the merge base.
type CompareSpec struct {
	Base   CompareSide
	Head   CompareSide
	TwoDot bool
}

// ParseBaseHead parses the basehead parameter of a compare URL. The canonical
// form is "base...head"; "base..head" selects the two-dot direct diff. Each
// side is a ref, an owner-qualified "owner:ref", or a fully qualified
// "owner:repo:ref" (github.com's cross-fork grammar). With no separator the
// whole string is the head with an empty base, so the caller can substitute
// the repository's default branch. ok is false when the string cannot parse:
// an empty side, or a side with more than two colons.
func ParseBaseHead(s string) (CompareSpec, bool) {
	if s == "" {
		return CompareSpec{}, false
	}
	var spec CompareSpec
	var rawBase, rawHead string
	switch {
	case strings.Contains(s, "..."):
		rawBase, rawHead, _ = strings.Cut(s, "...")
	case strings.Contains(s, ".."):
		rawBase, rawHead, _ = strings.Cut(s, "..")
		spec.TwoDot = true
	default:
		side, ok := parseCompareSide(s)
		if !ok {
			return CompareSpec{}, false
		}
		spec.Head = side
		return spec, true
	}
	if rawBase == "" || rawHead == "" {
		return CompareSpec{}, false
	}
	base, ok := parseCompareSide(rawBase)
	if !ok {
		return CompareSpec{}, false
	}
	head, ok := parseCompareSide(rawHead)
	if !ok {
		return CompareSpec{}, false
	}
	spec.Base, spec.Head = base, head
	return spec, true
}

// parseCompareSide parses one side of a compare range: "ref", "owner:ref", or
// "owner:repo:ref". A ref may itself contain a colon only in the unqualified
// form's absence, so the split is from the left: the first colon ends the
// owner, the second ends the repo. Empty components do not parse.
func parseCompareSide(s string) (CompareSide, bool) {
	parts := strings.Split(s, ":")
	for _, p := range parts {
		if p == "" {
			return CompareSide{}, false
		}
	}
	switch len(parts) {
	case 1:
		return CompareSide{Ref: parts[0]}, true
	case 2:
		return CompareSide{Owner: parts[0], Ref: parts[1]}, true
	case 3:
		return CompareSide{Owner: parts[0], Repo: parts[1], Ref: parts[2]}, true
	default:
		return CompareSide{}, false
	}
}

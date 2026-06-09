package route

import "strings"

// SplitRefPath separates a "<ref>/<path>" tail into its git ref and the file
// path within that ref. A ref can contain slashes (refs/heads/feature/x, or a
// branch named release/1.0), so the tail is ambiguous on its own: "main/cmd/foo"
// could be ref "main" path "cmd/foo", or a branch "main/cmd" path "foo". Git
// resolves this by preferring the longest leading segment sequence that names an
// existing ref. exists reports whether a candidate ref resolves; the caller backs
// it with the repository's ref set (and the commit-ish forms it accepts, such as
// a full or abbreviated SHA).
//
// It tries the whole tail as a ref first (a path-less ref URL), then peels one
// trailing segment at a time into the path. ok is false when no leading sequence
// names a ref, which the handler renders as a 404. See implementation/07 section 4.
func SplitRefPath(tail string, exists func(ref string) bool) (ref, path string, ok bool) {
	tail = strings.Trim(tail, "/")
	if tail == "" {
		return "", "", false
	}
	segs := strings.Split(tail, "/")
	for i := len(segs); i >= 1; i-- {
		candidate := strings.Join(segs[:i], "/")
		if exists(candidate) {
			return candidate, strings.Join(segs[i:], "/"), true
		}
	}
	return "", "", false
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

package route

import (
	"net/url"
	"strings"
)

// RefKind names which code-browsing view a URL addresses. The ref picker keeps a
// viewer on the same kind when they switch refs (a tree stays a tree, a blob
// stays a blob), so the kind travels with the resolved ref. See implementation/07
// section 10.3.
type RefKind int

const (
	// KindTree is a directory listing under /tree.
	KindTree RefKind = iota
	// KindBlob is a single file under /blob.
	KindBlob
	// KindCommits is the history view under /commits.
	KindCommits
	// KindRaw is the raw byte view under /raw.
	KindRaw
)

// These builders are the one place the web front turns an (owner, repo, ref,
// path) tuple into a URL, so a link in a template and the route that serves it
// can never drift. They are pure string functions with no router or domain
// dependency, which keeps fe/route testable on its own and free of an import
// cycle with the handlers. The presenter URL builder owns the REST and clone
// URLs (api, .git); these own the human-facing HTML routes. See implementation/02
// section 5 and implementation/07 section 11.

// Repo is the repository home, /{owner}/{repo}.
func Repo(owner, name string) string {
	return "/" + esc(owner) + "/" + esc(name)
}

// Tree is a directory at a ref, /{owner}/{repo}/tree/{ref}/{path}. An empty path
// addresses the ref root. The ref and each path segment are escaped, but the
// slashes between them stay literal so the longest-ref split (SplitRefPath) sees
// the same boundaries the builder wrote.
func Tree(owner, name, ref, path string) string {
	return refPathURL(owner, name, "tree", ref, path)
}

// Blob is a file at a ref, /{owner}/{repo}/blob/{ref}/{path}.
func Blob(owner, name, ref, path string) string {
	return refPathURL(owner, name, "blob", ref, path)
}

// Raw is the raw bytes of a file at a ref, /{owner}/{repo}/raw/{ref}/{path}.
func Raw(owner, name, ref, path string) string {
	return refPathURL(owner, name, "raw", ref, path)
}

// Commits is the history view, /{owner}/{repo}/commits/{ref}/{path}. The path is
// an optional history filter, so an empty path lists the whole ref history.
func Commits(owner, name, ref, path string) string {
	return refPathURL(owner, name, "commits", ref, path)
}

// Branches is the branch overview, /{owner}/{repo}/branches.
func Branches(owner, name string) string {
	return Repo(owner, name) + "/branches"
}

// Tags is the tag overview, /{owner}/{repo}/tags.
func Tags(owner, name string) string {
	return Repo(owner, name) + "/tags"
}

// Find is the file finder at a ref, /{owner}/{repo}/find/{ref}.
func Find(owner, name, ref string) string {
	return Repo(owner, name) + "/find/" + refSegments(ref)
}

// refPathURL joins the repo, the view verb, the ref, and the path. The ref may
// contain slashes (a branch named feature/x), so each of its segments is escaped
// individually and rejoined with literal slashes; the path is escaped the same
// way. The result round-trips through SplitRefPath because the boundaries are the
// literal slashes the splitter peels.
func refPathURL(owner, name, verb, ref, path string) string {
	u := Repo(owner, name) + "/" + verb + "/" + refSegments(ref)
	if path != "" {
		u += "/" + refSegments(path)
	}
	return u
}

// refSegments escapes each slash-separated segment but keeps the slashes literal.
func refSegments(s string) string {
	parts := strings.Split(s, "/")
	for i, p := range parts {
		parts[i] = esc(p)
	}
	return strings.Join(parts, "/")
}

// esc percent-encodes one path segment. It uses PathEscape, which leaves the
// segment readable for the common case (letters, digits, dot, dash) and encodes
// only what a path segment must.
func esc(s string) string {
	return url.PathEscape(s)
}

// Package git is Githome's read access to the bare repositories on disk. M2
// implements the read surface (refs, commits, trees, blobs, and path lookups)
// on a pure-Go go-git backend; writes and the smart-HTTP transport build on the
// same layout in later milestones. The on-disk layout is a sharded tree of bare
// repositories under a single root, addressed by the repository's internal pk.
package git

import "time"

// SHA is a 40-character lowercase hex object id. It is a string alias so call
// sites stay light while the type still documents intent.
type SHA = string

// ObjectType is a git object kind as it appears in the REST git-data models.
type ObjectType string

// The git object kinds.
const (
	ObjectCommit ObjectType = "commit"
	ObjectTree   ObjectType = "tree"
	ObjectBlob   ObjectType = "blob"
	ObjectTag    ObjectType = "tag"
)

// Signature is the name, email, and timestamp of a commit or tag author.
type Signature struct {
	Name  string
	Email string
	When  time.Time
}

// Commit is the parsed git commit object behind the git/commits and commits
// endpoints.
type Commit struct {
	SHA       SHA
	Tree      SHA
	Parents   []SHA
	Author    Signature
	Committer Signature
	Message   string
}

// TreeEntry is one entry of a tree. Path is the entry name within its tree, or
// the full slash-joined path when the tree was listed recursively. Mode is the
// six-digit octal string git stores (100644, 100755, 120000, 040000, 160000).
type TreeEntry struct {
	Path string
	Mode string
	Type ObjectType
	SHA  SHA
	Size int64 // blob byte size; zero for tree and submodule entries
}

// Tree is a git tree object. Truncated reports that the recursive walk hit the
// entry ceiling and stopped, matching GitHub's truncated flag.
type Tree struct {
	SHA       SHA
	Entries   []TreeEntry
	Truncated bool
}

// Blob is a git blob with its raw content.
type Blob struct {
	SHA     SHA
	Size    int64
	Content []byte
}

// Branch is a branch ref reduced to its name and head commit.
type Branch struct {
	Name   string
	Commit SHA
}

// Tag is a tag ref. Commit is the commit the tag ultimately points to. When the
// tag is annotated, Annotated carries the tag object's own metadata; it is nil
// for a lightweight tag.
type Tag struct {
	Name      string
	Commit    SHA
	Annotated *AnnotatedTag
}

// AnnotatedTag is the metadata of an annotated tag object.
type AnnotatedTag struct {
	SHA        SHA
	Tagger     Signature
	Message    string
	Target     SHA
	TargetType ObjectType
}

// Ref is a fully-qualified reference (refs/heads/main, refs/tags/v1.0). Target
// is the object the ref names directly: a commit for a branch or lightweight
// tag, or the tag object for an annotated tag. Type reports that object's kind.
type Ref struct {
	Name   string
	Target SHA
	Type   ObjectType
}

// PathEntry is one item the contents lookup returns: a file, a directory, a
// symlink, or a submodule, with the metadata the Contents API renders.
type PathEntry struct {
	Name string
	Path string
	Type ObjectType // blob (file/symlink), tree (dir), commit (submodule)
	Mode string
	SHA  SHA
	Size int64
}

// PathResult is the result of resolving a path at a ref. Exactly one of File or
// Dir is populated: File for a blob (with Content), Dir for a tree listing.
type PathResult struct {
	IsDir bool
	Entry PathEntry   // the resolved path's own metadata (set for files)
	File  *Blob       // populated when !IsDir
	Dir   []PathEntry // populated when IsDir
}

// LogOpts controls a commit history walk.
type LogOpts struct {
	From SHA    // starting commit (required)
	Path string // when set, only commits touching this path
	Skip int    // commits to skip before collecting, for page offsets
	Max  int    // cap on returned commits; zero means a sensible default
}

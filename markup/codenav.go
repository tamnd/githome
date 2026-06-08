package markup

import "context"

// codenav.go is the code-navigation surface: jump-to-definition and find-references
// over a blob, the "tags" layer that implementation/10 section 8 builds on a parsed
// syntax tree. That tree comes from the Tree-sitter backend, which is deferred in F2
// alongside the cgo highlighter (vendoring the grammars is a large step taken on its
// own). The pure-Go chroma backend lexes without parsing, so it cannot produce
// resolved tags.
//
// Rather than fake it, this build answers honestly: ScanTags reports unavailable and
// the lookups return nothing found. The types and the entry points are real and
// stable, so when the Tree-sitter backend lands it fills these in without changing a
// caller. The blob view checks the boolean and simply does not render navigation
// affordances when tags are unavailable, the same honest degradation the highlighter
// uses for an unknown language.

// TagKind classifies a symbol occurrence.
type TagKind int

// TagKind values: TagDefinition is the site where a symbol is defined, TagReference
// is a use of it.
const (
	TagDefinition TagKind = iota
	TagReference
)

// Tag is one symbol occurrence located in a blob: its name, what kind of occurrence
// it is, and the 1-based line and column where it starts.
type Tag struct {
	Name string
	Kind TagKind
	Line int
	Col  int
}

// Symbol is a named definition with the set of places it is referenced, the unit a
// jump-to-definition / find-references panel is built from.
type Symbol struct {
	Name       string
	Definition Tag
	References []Tag
}

// SymbolIndex is the per-blob result of tag scanning: the definitions keyed by name
// and the flat occurrence list. The zero value is a valid empty index.
type SymbolIndex struct {
	Symbols map[string]Symbol
	Tags    []Tag
}

// ScanTags parses code in the given grammar and returns its symbol index. In the
// pure-Go build it returns ok=false: there is no parse tree to derive tags from, so
// the caller renders the blob without navigation rather than with wrong links. The
// Tree-sitter build will return a populated index and ok=true.
func (r *Renderer) ScanTags(_ context.Context, code []byte, grammar string) (SymbolIndex, bool) {
	r.log.Debug("code navigation unavailable in this build", "grammar", grammar, "bytes", len(code))
	return SymbolIndex{Symbols: map[string]Symbol{}}, false
}

// Definitions returns the definition tags for a symbol name in a blob. It is empty
// in the pure-Go build; the boolean from ScanTags is the signal callers gate on, and
// this mirrors it by returning nothing.
func (r *Renderer) Definitions(_ context.Context, _ []byte, _ string, _ string) []Tag {
	return nil
}

// References returns the reference tags for a symbol name in a blob. Empty in the
// pure-Go build, for the same reason as Definitions.
func (r *Renderer) References(_ context.Context, _ []byte, _ string, _ string) []Tag {
	return nil
}

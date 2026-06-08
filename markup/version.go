package markup

// version.go holds the monotonic integers that bust the render caches
// (implementation/03 section 6.4 keys its caches on these). They are bumped, in
// the same commit, by any output-affecting change, so a bump transparently
// invalidates all stale cached HTML with no explicit purge. A security fix to the
// allowlist takes effect everywhere at once by bumping markupVersion. See
// implementation/10 section 8.1.
const (
	// markupVersion changes when the AST extensions, the sanitizer allowlist, the
	// post-processors, or the emoji/Linguist tables change rendered output.
	markupVersion = 1

	// highlighterVersion changes when the token mapping, the grammar set, or the
	// chroma fallback's token mapping changes highlighted output. The backend
	// choice folds in here too, so a backend swap re-keys the highlighted-cell
	// cache and one binary never serves another's cached cells from a shared store.
	highlighterVersion = 1

	// tagsVersion changes when the tags queries or their kind mapping change the
	// symbol index. Code navigation is deferred in this build (section 7), so this
	// only reserves the key.
	tagsVersion = 1
)

// Versions exposes the three constants for cache keys and for an admin/debug
// header, so a deployed instance's renderer version is observable.
type Versions struct{ Markup, Highlighter, Tags int }

// Version returns the renderer's three version integers.
func (r *Renderer) Version() Versions {
	return Versions{markupVersion, highlighterVersion, tagsVersion}
}

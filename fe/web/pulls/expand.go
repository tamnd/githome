package pulls

import (
	"strconv"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/view"
)

// maxExpandLines caps a single unfold so a malicious or fat-fingered count cannot
// ask the server to render an entire generated file in one fragment. A real gap
// between two hunks is small; GitHub itself unfolds in bounded steps.
const maxExpandLines = 1000

// ExpandDiff serves the hunk-unfold fragment: the context lines a collapsed gap on
// the Files tab hides, read from the head blob at the SHA the diff was built from.
// The query names the slice — the file path, the head SHA, the per-side start lines,
// the count, and the diff mode. The rows it returns are read-only context with no
// Position, so an unfolded line never becomes a comment anchor and the patch-offset
// space the review API resolves against is untouched. A missing PR, file, or blob is
// the soft 404; a count past the end of the file simply returns the lines that exist.
func (h *Handlers) ExpandDiff(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	// Load the PR so a request against a missing one takes the same soft-404 path the
	// Files tab does; the unfold reads from the head blob, not the PR row.
	if _, ok := h.loadPR(c, repo); !ok {
		return nil
	}

	q := c.Request().URL.Query()
	path := q.Get("path")
	sha := q.Get("sha")
	oldStart := atoiOr(q.Get("os"), 0)
	newStart := atoiOr(q.Get("ns"), 0)
	count := atoiOr(q.Get("n"), 0)
	if path == "" || sha == "" || newStart < 1 || oldStart < 1 || count < 1 {
		return h.notFound(c)
	}
	if count > maxExpandLines {
		count = maxExpandLines
	}

	res, err := h.repos.Contents(repo, path, sha)
	if err != nil || res.IsDir || res.File == nil {
		return h.notFound(c)
	}

	rows := view.BuildContextRows(string(res.File.Content), oldStart, newStart, count)
	vm := view.DiffContextVM{
		Rows:  rows,
		Split: q.Get("diff") == "split",
	}
	return h.render.Fragment(c, "pulls/diff_context", vm)
}

// atoiOr parses s as a base-10 int, returning def when it does not parse.
func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

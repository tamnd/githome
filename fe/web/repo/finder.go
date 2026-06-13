package repo

import (
	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/git"
)

// FileFinder renders the file index at a ref: GET /{owner}/{repo}/find/{rest}.
// find treats the whole tail as the ref (no path argument). The page lists every
// file in the recursive tree as a plain blob link, so it works with no JS, and a
// client filter narrows it as the viewer types. The recursive tree is capped; when
// the cap is hit the handler logs the truncation rather than dropping files
// silently. See implementation/07 section 10.4.
func (h *Handlers) FileFinder(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	rest := c.Param("rest")
	full, ok := h.resolveCommitish(repo, rest)
	if !ok {
		return h.notFound(c)
	}

	tree, err := h.repos.GetTree(repo, full, true)
	if err != nil {
		return err
	}
	if tree.Truncated {
		h.log.WarnContext(ctx, "file finder list truncated at the recursive-tree cap",
			"repo", repo.FullName(), "ref", rest)
	}

	owner := ownerLogin(repo)
	var files []view.FinderEntry
	for _, e := range tree.Entries {
		if e.Type != git.ObjectBlob {
			continue
		}
		files = append(files, view.FinderEntry{
			Path: e.Path,
			URL:  route.Blob(owner, repo.Name, rest, e.Path),
		})
	}

	vm := view.FileFinderVM{
		Chrome:    h.chrome(c, "Find a file · "+repo.FullName()),
		Header:    h.header(c.Context(), repo, "code"),
		Nav:       h.nav(repo, rest),
		Repo:      repoRef(repo),
		Ref:       rest,
		Files:     files,
		Truncated: tree.Truncated,
	}
	return h.render.Page(c, "repo/find", vm)
}

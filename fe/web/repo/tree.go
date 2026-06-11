package repo

import (
	"errors"
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// Tree renders a directory listing at a ref: GET /{owner}/{repo}/tree/{rest}. The
// tail splits into a ref and a path; a path that resolves to a blob 302-redirects
// to /blob (the tree/blob auto-correct, implementation/07 section 4), and a path
// that resolves to nothing is a soft 404 in the repo shell.
func (h *Handlers) Tree(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	refs := h.loadRefs(repo)
	ref, rev, path, ok := h.resolveRef(repo, refs, c.Param("rest"))
	if !ok {
		return h.notFound(c)
	}

	res, err := h.repos.Contents(repo, path, rev)
	if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
		return h.notFound(c)
	}
	if err != nil {
		return err
	}
	if !res.IsDir {
		// A blob reached through /tree corrects to /blob, matching github.com.
		return c.Redirect(http.StatusFound, route.Blob(ownerLogin(repo), repo.Name, ref, path))
	}

	r := view.Ref{Name: ref, IsDefault: ref == repo.DefaultBranch}
	vm := h.buildTreeFromDir(ctx, repo, refs, r, rev, path, res.Dir, false)
	vm.Chrome = h.chrome(c, treeTitle(repo, path))
	return h.render.Page(c, "repo/tree", vm)
}

// treeTitle is the browser title for a tree page: the repo name, then the path.
func treeTitle(repo *domain.Repo, path string) string {
	if path == "" {
		return repo.Name
	}
	return path + " at " + repo.DefaultBranch + " · " + repo.FullName()
}

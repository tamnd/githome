package pulls

import (
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/route"
)

// CommitRedirect handles /{owner}/{repo}/pull/{number}/commits/{sha}, the
// per-commit view inside a pull request. github.com renders the single commit's
// diff framed in the PR; Githome serves the same diff from the repository commit
// page, so this resolves the pull request (soft 404, with the issue-number
// crossover loadPR already does) and then 302s to /{owner}/{repo}/commit/{sha}.
// The commit page itself is the authority on whether the sha exists, so a bad
// sha lands on its 404 rather than being probed here.
func (h *Handlers) CommitRedirect(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	if _, ok := h.loadPR(c, repo); !ok {
		return nil
	}
	sha := c.Param("sha")
	if sha == "" {
		return h.notFound(c)
	}
	return c.Redirect(http.StatusFound, route.Commit(ownerLogin(repo), repo.Name, sha))
}

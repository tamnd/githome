package pulls

import (
	"strconv"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/view"
)

// Commits renders the PR Commits tab: the shell plus the pull request's own
// commits grouped by authored calendar date, newest day first. The commits are
// the head's commits over the base, the same range the diff and the API count, so
// the tab badge and the list never disagree. A missing PR is the soft 404. See
// implementation/09 section 4.
func (h *Handlers) Commits(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	pr, ok := h.loadPR(c, repo)
	if !ok {
		return nil
	}
	owner := ownerLogin(repo)

	commits, err := h.pulls.Commits(ctx, h.viewer(c).pk, owner, repo.Name, pr.Number)
	if err != nil {
		if isNotFound(err) {
			return h.notFound(c)
		}
		return h.render.ServerError(c, err)
	}

	title := pr.Title + " #" + strconv.FormatInt(pr.Number, 10)
	shell := h.shell(c, repo, pr, h.viewer(c).pk, "commits", title)
	vm := view.PRCommitsVM{
		Chrome: shell.Chrome,
		Shell:  shell,
		Groups: commitGroups(commits),
	}
	return h.render.Page(c, "pulls/commits", vm)
}

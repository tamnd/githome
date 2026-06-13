package repo

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/webmw"
)

// RepositoryByID 301-redirects the numeric /repositories/{id} permalink to the
// repository's canonical /{owner}/{repo} address. GitHub keeps this id-based URL
// so a repository survives an owner or name change — the id never moves while the
// path does. A non-numeric id, or a private repository the viewer cannot see,
// renders the soft 404 (the 404-not-403 rule), so the id space never confirms
// that a hidden repository exists. GET /repositories/{id}.
func (h *Handlers) RepositoryByID(c *mizu.Ctx) error {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		return h.notFound(c)
	}
	ctx := c.Context()
	repo, err := h.repos.GetRepoByID(ctx, webmw.ViewerID(ctx), id)
	if errors.Is(err, domain.ErrRepoNotFound) {
		return h.notFound(c)
	}
	if err != nil {
		return err
	}
	return c.Redirect(http.StatusMovedPermanently, route.Repo(ownerLogin(repo), repo.Name))
}

package pulls

import (
	"errors"
	"net/url"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/webmw"
)

// Create handles POST /{owner}/{repo}/pulls, the create-PR form submission from
// the compare range page. It reads the title, body, base, head, and draft flag
// from the form, delegates to the domain create, and 303-redirects to the new
// PR's Conversation tab. Validation errors (empty title, same base/head,
// unresolvable branches, duplicate PR) redirect back to the compare page with
// the form pre-filled; infrastructure errors let the recover layer render a 500.
// See implementation/09 section 8.
func (h *Handlers) Create(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	owner := ownerLogin(repo)
	actorPK := webmw.ViewerID(ctx)
	if actorPK == 0 {
		returnTo := route.CompareExpanded(owner, repo.Name, formString(c, "base"), formString(c, "head"))
		return redirect(c, "/login?return_to="+url.QueryEscape(returnTo))
	}

	title := formString(c, "title")
	body := formRaw(c, "body")
	base := formString(c, "base")
	head := formString(c, "head")
	draft := formString(c, "draft") == "1"

	var bodyPtr *string
	if body != "" {
		bodyPtr = &body
	}

	in := domain.PRInput{
		Title: title,
		Body:  bodyPtr,
		Base:  base,
		Head:  head,
		Draft: draft,
	}
	pr, err := h.pulls.CreatePR(ctx, actorPK, owner, repo.Name, in)
	if errors.Is(err, domain.ErrValidation) || errors.Is(err, domain.ErrGitNotFound) {
		fallback := route.CompareExpanded(owner, repo.Name, base, head)
		return redirect(c, fallback)
	}
	if err != nil {
		return h.render.ServerError(c, err)
	}
	return redirect(c, route.Pull(owner, repo.Name, pr.Number))
}

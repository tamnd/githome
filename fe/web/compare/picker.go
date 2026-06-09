package compare

import (
	"net/http"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// Picker renders the branch-picker page, GET /{owner}/{repo}/compare. When the
// form is submitted with base and head query params it redirects immediately to
// the compare range URL, so the no-JS path does not need a separate POST. An
// empty or uninitialized repository shows a blankslate. See implementation/09
// section 8.
func (h *Handlers) Picker(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	owner := ownerLogin(repo)

	// When the picker form submits, redirect straight to the compare range URL.
	base := strings.TrimSpace(c.Query("base"))
	head := strings.TrimSpace(c.Query("head"))
	if base != "" && head != "" {
		return c.Redirect(http.StatusSeeOther, route.Compare(owner, repo.Name, base, head))
	}

	branches, err := h.repos.ListBranches(repo)
	if err != nil {
		return h.render.ServerError(c, err)
	}

	var defaultBranch string
	if db, err := h.repos.DefaultBranchRef(repo); err == nil {
		defaultBranch = db.Name
	}

	names := make([]string, 0, len(branches))
	for _, b := range branches {
		names = append(names, b.Name)
	}

	title := "Compare · " + owner + "/" + repo.Name
	vm := view.ComparePickerVM{
		Chrome:   h.chrome(c, title),
		Header:   h.header(repo, "pulls"),
		Nav:      h.nav(repo),
		Branches: names,
		Base:     defaultBranch,
		Action:   route.ComparePicker(owner, repo.Name),
	}
	return h.render.Page(c, "compare/picker", vm)
}

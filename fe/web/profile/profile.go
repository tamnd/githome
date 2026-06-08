package profile

import (
	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

// Show renders a user or organization profile. The ?tab= facet selects the body:
// the overview (the default) pairs a short grid of the owner's recently updated
// repositories with their recent activity, and the repositories tab lists every
// visible repository with the same sort and pager the search page uses. The
// account was resolved by the Resolve middleware, so the handler only maps it and
// its backed data into the page model. A data error from the search or the feed is
// returned so the recover layer renders a 500; an empty result is a blankslate,
// not an error.
func (h *Handlers) Show(c *mizu.Ctx) error {
	ctx := c.Context()
	u, ok := userFromContext(ctx)
	if !ok {
		return h.render.NotFoundWithChrome(c, h.chrome(c, ""))
	}
	viewer := webmw.ViewerID(ctx)

	tab := view.ProfileTabOr(c.Query("tab"))
	vm := view.ProfilePageVM{
		Header:    h.header(u),
		ActiveTab: tab,
		Tabs:      h.tabs(u, tab),
	}
	title := u.Login
	if u.Name != nil && *u.Name != "" {
		title = *u.Name
	}
	vm.Chrome = h.chrome(c, title)

	switch tab {
	case view.ProfileRepositories:
		repos, err := h.repositories(ctx, viewer, u, c.Query("sort"), c.Query("order"), pageParam(c))
		if err != nil {
			return err
		}
		vm.Repos = repos
	default:
		overview, err := h.overview(ctx, viewer, u)
		if err != nil {
			return err
		}
		vm.Overview = overview
	}

	return h.render.Page(c, "profile/page", vm)
}

package issues

import (
	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/view"
)

// Labels renders the repository's label list: GET /{owner}/{repo}/labels. Each
// row is the same color chip the issue rows wear, linking to the issues index
// filtered to that label, plus the label description. The list is read through
// the same ListLabels the REST endpoint serves, in name order. Label
// management stays in the REST API and the issue sidebar; this page is the
// browse surface. See spec 02 section 3.6.
func (h *Handlers) Labels(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	owner := ownerLogin(repo)
	vc := h.viewer(c)

	labels, err := h.issues.ListLabels(ctx, vc.pk, owner, repo.Name)
	if err != nil {
		return h.listError(c, err)
	}

	chips := make([]view.LabelVM, 0, len(labels))
	for _, l := range labels {
		chips = append(chips, labelChip(owner, repo.Name, l))
	}

	vm := view.LabelsVM{
		Chrome: h.chrome(c, "Labels · "+owner+"/"+repo.Name),
		Header: h.header(repo),
		Nav:    h.nav(repo),
		Repo:   repoRef(repo),
		Labels: chips,
		Count:  len(chips),
	}
	return h.render.Page(c, "issues/labels", vm)
}

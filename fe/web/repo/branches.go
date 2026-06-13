package repo

import (
	"sort"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// Branches renders the branch overview: GET /{owner}/{repo}/branches. The default
// branch is listed first, then the rest in name order. The ahead/behind counts
// and per-branch PR status arrive with the compare domain; F1 lists the branches
// with their tree and history links. See implementation/07 section 10.1.
func (h *Handlers) Branches(c *mizu.Ctx) error {
	repo, ok := repoFromContext(c.Context())
	if !ok {
		return h.notFound(c)
	}
	branches, err := h.repos.ListBranches(repo)
	if err != nil {
		return err
	}

	owner := ownerLogin(repo)
	rows := make([]view.BranchRowVM, 0, len(branches))
	for _, b := range branches {
		rows = append(rows, view.BranchRowVM{
			Name:       b.Name,
			IsDefault:  b.Name == repo.DefaultBranch,
			TreeURL:    route.Tree(owner, repo.Name, b.Name, ""),
			CommitsURL: route.Commits(owner, repo.Name, b.Name, ""),
		})
	}
	sortBranchRows(rows, repo.DefaultBranch)

	vm := view.BranchesVM{
		Chrome:  h.chrome(c, "Branches · "+repo.FullName()),
		Header:  h.header(c.Context(), repo, "branches"),
		Nav:     h.nav(repo, repo.DefaultBranch),
		Repo:    repoRef(repo),
		Default: repo.DefaultBranch,
		Items:   rows,
	}
	return h.render.Page(c, "repo/branches", vm)
}

// sortBranchRows puts the default branch first, then orders the rest by name.
func sortBranchRows(rows []view.BranchRowVM, def string) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Name == def {
			return true
		}
		if rows[j].Name == def {
			return false
		}
		return rows[i].Name < rows[j].Name
	})
}

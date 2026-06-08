package profile

import (
	"context"
	"net/url"
	"strconv"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// build.go assembles the two profile tab bodies from the domain. The overview
// pairs a short grid of the owner's most recently updated repositories with their
// recent activity; the repositories tab lists every visible repository with a sort
// menu and a pager. Both repository lists come from the same domain repository
// search the search page uses, scoped to the owner with a user:/org: qualifier, so
// the profile and the search page render the same row and apply the same
// visibility rule (a private repository the viewer cannot see never appears). The
// activity comes from the event service's per-user feed, gated the same way. See
// implementation/12 sections 5, 6, and 7.

const (
	// overviewRepoLimit caps the overview's repository grid; the "show all" link
	// leads to the full repositories tab.
	overviewRepoLimit = 6
	// feedLimit caps the activity timeline on the overview.
	feedLimit = 15
	// reposPerPage is the repositories tab page size, matching the search page.
	reposPerPage = 30
)

// overview builds the overview tab: the recent-repository grid and the activity
// timeline. A fresh account with neither public repositories nor public activity
// renders the combined empty state; an account with one but not the other renders
// the section it has and the blankslate for the section it does not.
func (h *Handlers) overview(ctx context.Context, viewer int64, u *domain.User) (view.ProfileOverviewVM, error) {
	vm := view.ProfileOverviewVM{
		ReposURL: route.ProfileTab(u.Login, view.ProfileRepositories),
	}

	if h.search != nil {
		repos, _, err := h.search.SearchRepositories(ctx, viewer, ownerQualifier(u), "updated", "", 1, overviewRepoLimit)
		if err != nil {
			return view.ProfileOverviewVM{}, err
		}
		vm.PopularRepos = h.repoResults(repos)
	}

	if h.events != nil {
		events, err := h.events.UserFeed(ctx, viewer, u.Login, feedLimit)
		if err != nil {
			return view.ProfileOverviewVM{}, err
		}
		vm.Activity = h.feedItems(events)
	}
	vm.ActivityEmpty = len(vm.Activity) == 0
	vm.Empty = len(vm.PopularRepos) == 0 && vm.ActivityEmpty
	return vm, nil
}

// repositories builds the repositories tab: the owner's visible repositories for
// the requested page, the sort menu, and the pager. The sort and order are
// validated against the backed set before they reach the domain search, so a
// hand-edited URL degrades to the default rather than erroring.
func (h *Handlers) repositories(ctx context.Context, viewer int64, u *domain.User, sort, order string, page int) (view.ProfileReposVM, error) {
	sort, order = normalizeRepoSort(sort, order)
	if h.search == nil {
		return view.ProfileReposVM{
			Sorts:       h.sorts(u, sort, order),
			Empty:       true,
			EmptyReason: u.Login + " has no repositories yet.",
		}, nil
	}
	repos, total, err := h.search.SearchRepositories(ctx, viewer, ownerQualifier(u), sort, order, page, reposPerPage)
	if err != nil {
		return view.ProfileReposVM{}, err
	}
	vm := view.ProfileReposVM{
		Items: h.repoResults(repos),
		Sorts: h.sorts(u, sort, order),
		Pager: h.pager(u, sort, order, page, total),
	}
	if len(vm.Items) == 0 {
		vm.Empty = true
		vm.EmptyReason = u.Login + " has no repositories yet."
	}
	return vm, nil
}

// repoResults maps the matched repositories into the shared search result rows, so
// the profile grid and tab render the same row shape as the search page.
func (h *Handlers) repoResults(repos []*domain.Repo) []view.RepoResultVM {
	out := make([]view.RepoResultVM, 0, len(repos))
	for _, repo := range repos {
		owner := repoOwner(repo)
		row := view.RepoResultVM{
			FullName:   owner + "/" + repo.Name,
			URL:        route.Repo(owner, repo.Name),
			OwnerURL:   route.Profile(owner),
			OwnerLogin: owner,
			Private:    repo.Private,
			Fork:       repo.Fork,
			Archived:   repo.Archived,
		}
		if repo.Description != nil {
			row.Description = *repo.Description
		}
		if when := repo.PushedAt; when != nil {
			row.UpdatedAt = when.UTC().Format("Jan 2, 2006")
			row.UpdatedISO = when.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		out = append(out, row)
	}
	return out
}

// sorts builds the repositories-tab sort menu. The options map onto the sort keys
// the domain repository search understands, the same three the search page offers.
func (h *Handlers) sorts(u *domain.User, sort, order string) []view.SearchSortOption {
	opts := []struct{ label, sort, order string }{
		{"Newest", "", ""},
		{"Recently updated", "updated", ""},
		{"Oldest", "created", "asc"},
	}
	out := make([]view.SearchSortOption, 0, len(opts))
	for _, o := range opts {
		out = append(out, view.SearchSortOption{
			Label:    o.label,
			URL:      reposURL(u.Login, o.sort, o.order, 1),
			IsActive: sort == o.sort && order == o.order,
		})
	}
	return out
}

// pager builds the repositories-tab prev/next links from the running total,
// keeping the sort and order intact and setting only the page. The first page
// shows no prev; a page that holds the last row shows no next.
func (h *Handlers) pager(u *domain.User, sort, order string, page, total int) view.Pager {
	p := view.Pager{Page: page}
	if page > 1 {
		p.PrevURL = reposURL(u.Login, sort, order, page-1)
	}
	if page*reposPerPage < total {
		p.NextURL = reposURL(u.Login, sort, order, page+1)
	}
	return p
}

// reposURL builds a repositories-tab URL with the sort, order, and page facets.
// The first page drops the page facet so the canonical tab URL has none; an empty
// sort or order is omitted so the default reads clean.
func reposURL(login, sort, order string, page int) string {
	vals := url.Values{"tab": {view.ProfileRepositories}}
	if sort != "" {
		vals.Set("sort", sort)
	}
	if order != "" {
		vals.Set("order", order)
	}
	if page > 1 {
		vals.Set("page", strconv.Itoa(page))
	}
	return route.Profile(login) + "?" + vals.Encode()
}

// normalizeRepoSort validates a requested sort and order against the backed set,
// dropping an unknown value to the default. Only "updated" and "created" sort, and
// only "created" reads an "asc" order (oldest first); everything else is the
// newest-first default.
func normalizeRepoSort(sort, order string) (string, string) {
	switch sort {
	case "updated":
		return "updated", ""
	case "created":
		if order == "asc" {
			return "created", "asc"
		}
		return "created", ""
	default:
		return "", ""
	}
}

// repoOwner returns the repository owner's login, tolerating a repo assembled
// without its owner.
func repoOwner(r *domain.Repo) string {
	if r != nil && r.Owner != nil {
		return r.Owner.Login
	}
	return ""
}

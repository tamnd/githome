package profile

import (
	"context"
	"net/url"
	"strconv"
	"strings"

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
	// reposPerPage is the repositories tab page size, matching the search page. The
	// stars tab reuses it since it lists the same repository rows.
	reposPerPage = 30
	// peoplePerPage is the followers/following tab page size.
	peoplePerPage = 50
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
		vm.Activity = FeedItems(events)
	}
	vm.ActivityEmpty = len(vm.Activity) == 0
	vm.Empty = len(vm.PopularRepos) == 0 && vm.ActivityEmpty
	return vm, nil
}

// repositories builds the repositories tab: the owner's visible repositories for
// the requested page, the sort menu, and the pager. The sort and order are
// validated against the backed set before they reach the domain search, so a
// hand-edited URL degrades to the default rather than erroring. A ?q= term filters
// the list by name the same way the search page does, by appending the term to the
// owner qualifier the search already carries.
func (h *Handlers) repositories(ctx context.Context, viewer int64, u *domain.User, query, sort, order string, page int) (view.ProfileReposVM, error) {
	sort, order = normalizeRepoSort(sort, order)
	query = strings.TrimSpace(query)
	if h.search == nil {
		return view.ProfileReposVM{
			Sorts:       h.sorts(u, query, sort, order),
			Query:       query,
			OwnerLogin:  u.Login,
			Empty:       true,
			EmptyReason: u.Login + " has no repositories yet.",
		}, nil
	}
	repos, total, err := h.search.SearchRepositories(ctx, viewer, repoQuery(u, query), sort, order, page, reposPerPage)
	if err != nil {
		return view.ProfileReposVM{}, err
	}
	vm := view.ProfileReposVM{
		Items:      h.repoResults(repos),
		Sorts:      h.sorts(u, query, sort, order),
		Query:      query,
		OwnerLogin: u.Login,
		Pager:      h.pager(u, query, sort, order, page, total),
	}
	if len(vm.Items) == 0 {
		vm.Empty = true
		if query != "" {
			vm.EmptyReason = "No repositories matched your search."
		} else {
			vm.EmptyReason = u.Login + " has no repositories yet."
		}
	}
	return vm, nil
}

// stars builds the stars tab: the repositories the account has starred, filtered
// by the viewer's visibility (a private repository the viewer cannot see never
// appears) and paged in memory off the domain's full list. With no social service
// the tab is an honest blankslate rather than an error.
func (h *Handlers) stars(ctx context.Context, viewer int64, u *domain.User, page int) (view.ProfileStarsVM, error) {
	if h.social == nil {
		return view.ProfileStarsVM{Empty: true, EmptyReason: u.Login + " hasn't starred any repositories yet."}, nil
	}
	repos, err := h.social.StarredByLogin(ctx, viewer, u.Login)
	if err != nil {
		return view.ProfileStarsVM{}, err
	}
	total := len(repos)
	pageRepos := pageSlice(repos, page, reposPerPage)
	vm := view.ProfileStarsVM{
		Items: h.repoResults(pageRepos),
		Pager: tabPager(u, view.ProfileStars, page, total),
	}
	if len(vm.Items) == 0 {
		vm.Empty = true
		vm.EmptyReason = u.Login + " hasn't starred any repositories yet."
	}
	return vm, nil
}

// followers builds the followers tab: the accounts that follow this account, paged
// in memory. The list is public, so it needs no viewer filter.
func (h *Handlers) followers(ctx context.Context, u *domain.User, page int) (view.ProfilePeopleVM, error) {
	if h.social == nil {
		return view.ProfilePeopleVM{Heading: "Followers", Empty: true, EmptyReason: u.Login + " has no followers yet."}, nil
	}
	users, err := h.social.FollowersOfLogin(ctx, u.Login)
	if err != nil {
		return view.ProfilePeopleVM{}, err
	}
	return h.people(u, "Followers", view.ProfileFollowers, users, page, u.Login+" has no followers yet."), nil
}

// following builds the following tab: the accounts this account follows, paged in
// memory.
func (h *Handlers) following(ctx context.Context, u *domain.User, page int) (view.ProfilePeopleVM, error) {
	if h.social == nil {
		return view.ProfilePeopleVM{Heading: "Following", Empty: true, EmptyReason: u.Login + " isn't following anyone yet."}, nil
	}
	users, err := h.social.FollowingOfLogin(ctx, u.Login)
	if err != nil {
		return view.ProfilePeopleVM{}, err
	}
	return h.people(u, "Following", view.ProfileFollowing, users, page, u.Login+" isn't following anyone yet."), nil
}

// people assembles a followers or following tab body from a resolved user list,
// mapping each account into a card and paging the list in memory.
func (h *Handlers) people(u *domain.User, heading, tab string, users []*domain.User, page int, emptyReason string) view.ProfilePeopleVM {
	total := len(users)
	vm := view.ProfilePeopleVM{
		Heading: heading,
		Users:   h.userCards(pageSlice(users, page, peoplePerPage)),
		Pager:   tabPager(u, tab, page, total),
	}
	if len(vm.Users) == 0 {
		vm.Empty = true
		vm.EmptyReason = emptyReason
	}
	return vm
}

// userCards maps domain users into people-list cards, resolving each avatar and
// profile URL the same way the identity header does.
func (h *Handlers) userCards(users []*domain.User) []view.UserCardVM {
	out := make([]view.UserCardVM, 0, len(users))
	for _, u := range users {
		card := view.UserCardVM{
			Login:      u.Login,
			AvatarURL:  h.avatar(u),
			ProfileURL: route.Profile(u.Login),
		}
		if u.Name != nil {
			card.Name = strings.TrimSpace(*u.Name)
		}
		if u.Bio != nil {
			card.Bio = strings.TrimSpace(*u.Bio)
		}
		out = append(out, card)
	}
	return out
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
// Each option keeps the active ?q= so switching the sort does not drop the filter.
func (h *Handlers) sorts(u *domain.User, query, sort, order string) []view.SearchSortOption {
	opts := []struct{ label, sort, order string }{
		{"Newest", "", ""},
		{"Recently updated", "updated", ""},
		{"Oldest", "created", "asc"},
	}
	out := make([]view.SearchSortOption, 0, len(opts))
	for _, o := range opts {
		out = append(out, view.SearchSortOption{
			Label:    o.label,
			URL:      reposURL(u.Login, query, o.sort, o.order, 1),
			IsActive: sort == o.sort && order == o.order,
		})
	}
	return out
}

// pager builds the repositories-tab prev/next links from the running total,
// keeping the query, sort, and order intact and setting only the page. The first
// page shows no prev; a page that holds the last row shows no next.
func (h *Handlers) pager(u *domain.User, query, sort, order string, page, total int) view.Pager {
	p := view.Pager{Page: page}
	if page > 1 {
		p.PrevURL = reposURL(u.Login, query, sort, order, page-1)
	}
	if page*reposPerPage < total {
		p.NextURL = reposURL(u.Login, query, sort, order, page+1)
	}
	return p
}

// reposURL builds a repositories-tab URL with the query, sort, order, and page
// facets. The first page drops the page facet so the canonical tab URL has none;
// an empty query, sort, or order is omitted so the default reads clean.
func reposURL(login, query, sort, order string, page int) string {
	vals := url.Values{"tab": {view.ProfileRepositories}}
	if query != "" {
		vals.Set("q", query)
	}
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

// tabPager builds the prev/next links for a tab that pages a plain list in memory
// (stars, followers, following), keeping the tab facet and setting only the page.
func tabPager(u *domain.User, tab string, page, total int) view.Pager {
	per := reposPerPage
	if tab != view.ProfileStars {
		per = peoplePerPage
	}
	p := view.Pager{Page: page}
	if page > 1 {
		p.PrevURL = tabURL(u.Login, tab, page-1)
	}
	if page*per < total {
		p.NextURL = tabURL(u.Login, tab, page+1)
	}
	return p
}

// tabURL builds a plain tab URL with only the tab and page facets.
func tabURL(login, tab string, page int) string {
	vals := url.Values{"tab": {tab}}
	if page > 1 {
		vals.Set("page", strconv.Itoa(page))
	}
	return route.Profile(login) + "?" + vals.Encode()
}

// repoQuery composes the domain repository search query for the repositories tab:
// the owner qualifier the tab is always scoped to, plus the human's free-text term
// when present, so a ?q= narrows the owner's repositories by name.
func repoQuery(u *domain.User, query string) string {
	if query == "" {
		return ownerQualifier(u)
	}
	return ownerQualifier(u) + " " + query
}

// pageSlice returns the one-based page of width per from items, or an empty slice
// when the page is past the end. It backs the in-memory paging of the social lists,
// which the domain returns whole.
func pageSlice[T any](items []T, page, per int) []T {
	if page < 1 {
		page = 1
	}
	start := (page - 1) * per
	if start >= len(items) {
		return nil
	}
	end := start + per
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
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

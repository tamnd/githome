package search

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

// build.go maps the domain search results into the fe/view models and assembles
// the page: it runs the active type once, probes the cheap inactive types for
// their rail counts, builds the faceted URLs for the rail, the sort menu, and the
// pager, and maps each hit into a flat result row with its URLs precomputed. It
// keeps fe/view a pure data package by concentrating the mapping here. See
// implementation/12 section 2.

// build runs the search and returns the page view model. An empty query short
// circuits to the landing. The active type's results and total come from one
// domain call; the rail counts for the other cheap types come from one-row
// probes, the same shape the issues index uses for its inactive tab.
func (h *Handlers) build(c *mizu.Ctx, r req) (view.SearchPageVM, error) {
	ctx := c.Context()
	viewer := webmw.ViewerID(ctx)

	vm := view.SearchPageVM{
		Scope:      r.scope,
		QueryValue: r.q,
		Action:     r.action,
		ActiveType: r.active,
		Pager:      view.Pager{Page: r.page},
	}
	vm.Chrome = h.chrome(c, r.title)
	if r.scope == view.ScopeRepo {
		vm.Header = h.header(r.repo)
		vm.Nav = h.nav(r.repo)
		vm.Repo = repoRef(r.repo)
	}

	if strings.TrimSpace(r.q) == "" {
		vm.Landing = true
		vm.Types = h.rail(ctx, r, viewer, -1, false)
		return vm, nil
	}

	if err := h.runActive(ctx, viewer, r, &vm); err != nil {
		return view.SearchPageVM{}, err
	}
	vm.Types = h.rail(ctx, r, viewer, vm.Total, true)
	vm.Sorts = h.sorts(r)
	vm.Pager = h.pager(r, vm.Total)
	if !vm.Landing && !vm.Empty && vm.Total == 0 && len(vm.Notes) == 0 {
		vm.Empty = true
		vm.EmptyReason = emptyReason(r.active)
	}
	return vm, nil
}

// runActive runs the search for the active type and fills the matching result
// slice, the total, and any notes. A code search with no scope is not an error
// the viewer caused: it renders the scope-required blankslate rather than a 500.
func (h *Handlers) runActive(ctx context.Context, viewer int64, r req, vm *view.SearchPageVM) error {
	switch r.active {
	case view.SearchRepos:
		repos, total, err := h.search.SearchRepositories(ctx, viewer, effRaw(r, view.SearchRepos), r.sort, r.order, r.page, perPage)
		if err != nil {
			return err
		}
		vm.Repos = h.repoResults(repos)
		vm.Total = total

	case view.SearchIssues, view.SearchPulls:
		hits, total, err := h.search.SearchIssues(ctx, viewer, effRaw(r, r.active), r.sort, r.order, r.page, perPage)
		if err != nil {
			return err
		}
		vm.Issues = h.issueResults(hits, r.active == view.SearchPulls)
		vm.Total = total

	case view.SearchCode:
		results, total, incomplete, err := h.search.SearchCode(ctx, viewer, effRaw(r, view.SearchCode), r.page, perPage)
		if errors.Is(err, domain.ErrSearchScopeRequired) {
			vm.Empty = true
			vm.EmptyReason = "Code search needs a repo:, user:, or org: qualifier to scope the walk."
			return nil
		}
		if err != nil {
			return err
		}
		vm.Code = h.codeResults(results)
		vm.Total = total
		if incomplete {
			// A capped walk is a documented behavior, never a silent truncation:
			// surface it as a note and log it, per the front's "never hide a cap".
			vm.Notes = append(vm.Notes, "Showing partial results: the code search stopped before scanning every file.")
			h.logIncomplete(ctx, r, total)
		}
	}
	return nil
}

// effRaw builds the raw query string the domain search parses for a given type:
// the in-repo scope prepends repo:{owner}/{name}, and the issue/pull split
// prepends is:issue or is:pr so the type rail drives the result kind. The viewer's
// own q follows, so an explicit qualifier they typed still parses.
func effRaw(r req, typ string) string {
	var b strings.Builder
	if r.scope == view.ScopeRepo && r.repo != nil {
		b.WriteString("repo:")
		b.WriteString(ownerLogin(r.repo))
		b.WriteByte('/')
		b.WriteString(r.repo.Name)
		b.WriteByte(' ')
	}
	switch typ {
	case view.SearchIssues:
		b.WriteString("is:issue ")
	case view.SearchPulls:
		b.WriteString("is:pr ")
	}
	b.WriteString(r.q)
	return b.String()
}

// rail builds the result-type tabs with their faceted switch URLs and counts.
// activeTotal is the active type's total (already computed); a negative value
// means the landing, where no counts are probed. The inactive cheap types
// (repositories, issues, pull requests) get a one-row count probe; code is never
// probed because counting it means walking a tree.
func (h *Handlers) rail(ctx context.Context, r req, viewer int64, activeTotal int, withCounts bool) []view.SearchTab {
	tabs := make([]view.SearchTab, 0, len(r.types))
	for _, typ := range r.types {
		tab := view.SearchTab{
			Key:      typ,
			Label:    typeLabel(typ),
			Icon:     typeIcon(typ),
			IsActive: typ == r.active,
			URL:      r.facet(url.Values{"q": {r.q}, "type": {typ}}),
		}
		if withCounts && typ != view.SearchCode {
			if typ == r.active {
				tab.Count, tab.HasCount = activeTotal, true
			} else if n, ok := h.typeCount(ctx, viewer, r, typ); ok {
				tab.Count, tab.HasCount = n, true
			}
		}
		tabs = append(tabs, tab)
	}
	return tabs
}

// typeCount probes the total for an inactive cheap type with a one-row request.
// A probe error degrades to no count rather than failing the page; the tab still
// renders, just without a number.
func (h *Handlers) typeCount(ctx context.Context, viewer int64, r req, typ string) (int, bool) {
	switch typ {
	case view.SearchRepos:
		_, total, err := h.search.SearchRepositories(ctx, viewer, effRaw(r, typ), "", "", 1, 1)
		if err != nil {
			return 0, false
		}
		return total, true
	case view.SearchIssues, view.SearchPulls:
		_, total, err := h.search.SearchIssues(ctx, viewer, effRaw(r, typ), "", "", 1, 1)
		if err != nil {
			return 0, false
		}
		return total, true
	}
	return 0, false
}

// sorts builds the sort menu for the active type. Code search has no sort, so it
// returns nil and the template hides the control. The options map onto the sort
// keys the domain search understands.
func (h *Handlers) sorts(r req) []view.SearchSortOption {
	var opts []struct{ label, sort, order string }
	switch r.active {
	case view.SearchRepos:
		opts = []struct{ label, sort, order string }{
			{"Newest", "", ""},
			{"Recently updated", "updated", ""},
			{"Oldest", "created", "asc"},
		}
	case view.SearchIssues, view.SearchPulls:
		opts = []struct{ label, sort, order string }{
			{"Newest", "", ""},
			{"Recently updated", "updated", ""},
			{"Most commented", "comments", ""},
		}
	default:
		return nil
	}
	out := make([]view.SearchSortOption, 0, len(opts))
	for _, o := range opts {
		vals := url.Values{"q": {r.q}, "type": {r.active}}
		if o.sort != "" {
			vals.Set("sort", o.sort)
		}
		if o.order != "" {
			vals.Set("order", o.order)
		}
		out = append(out, view.SearchSortOption{
			Label:    o.label,
			URL:      r.facet(vals),
			IsActive: r.sort == o.sort && r.order == o.order,
		})
	}
	return out
}

// pager builds the prev/next links from the running total, keeping q, type, sort,
// and order intact and setting only the page. A page beyond the last shows no
// next; the first page shows no prev.
func (h *Handlers) pager(r req, total int) view.Pager {
	p := view.Pager{Page: r.page}
	base := url.Values{"q": {r.q}, "type": {r.active}}
	if r.sort != "" {
		base.Set("sort", r.sort)
	}
	if r.order != "" {
		base.Set("order", r.order)
	}
	if r.page > 1 {
		prev := cloneVals(base)
		if r.page-1 > 1 {
			prev.Set("page", strconv.Itoa(r.page-1))
		}
		p.PrevURL = r.facet(prev)
	}
	if r.page*perPage < total {
		next := cloneVals(base)
		next.Set("page", strconv.Itoa(r.page+1))
		p.NextURL = r.facet(next)
	}
	return p
}

// cloneVals copies a url.Values so a per-link override does not mutate the shared
// base the other links read.
func cloneVals(in url.Values) url.Values {
	out := make(url.Values, len(in))
	for k, v := range in {
		cp := make([]string, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// repoResults maps the matched repositories into result rows.
func (h *Handlers) repoResults(repos []*domain.Repo) []view.RepoResultVM {
	out := make([]view.RepoResultVM, 0, len(repos))
	for _, repo := range repos {
		owner := ownerLogin(repo)
		row := view.RepoResultVM{
			FullName:   owner + "/" + repo.Name,
			URL:        route.Repo(owner, repo.Name),
			OwnerURL:   "/" + owner,
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

// issueResults maps the matched issues or pull requests into result rows. isPull
// selects the /pull/{n} URL and the pull-request state badge; an issue uses
// /issues/{n}. The repository-context line comes from the hit's repo, since a
// cross-repository result does not imply one from the path.
func (h *Handlers) issueResults(hits []domain.IssueHit, isPull bool) []view.IssueResultVM {
	out := make([]view.IssueResultVM, 0, len(hits))
	for _, hit := range hits {
		owner := ownerLogin(hit.Repo)
		full := owner + "/" + hit.Repo.Name
		iss := hit.Issue
		row := view.IssueResultVM{
			Number:       iss.Number,
			Title:        iss.Title,
			State:        issueStateBadge(iss, isPull),
			Author:       h.userChip(iss.User),
			OpenedAt:     iss.CreatedAt.UTC().Format("Jan 2, 2006"),
			OpenedISO:    iss.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			CommentCount: iss.CommentsCount,
			RepoFullName: full,
			RepoURL:      route.Repo(owner, hit.Repo.Name),
		}
		if isPull {
			row.URL = route.Pull(owner, hit.Repo.Name, iss.Number)
		} else {
			row.URL = route.Issue(owner, hit.Repo.Name, iss.Number)
		}
		out = append(out, row)
	}
	return out
}

// codeResults maps the matched files into result rows, linking each to its blob
// at the repository's default branch.
func (h *Handlers) codeResults(results []domain.CodeResult) []view.CodeResultVM {
	out := make([]view.CodeResultVM, 0, len(results))
	for _, res := range results {
		owner := ownerLogin(res.Repo)
		out = append(out, view.CodeResultVM{
			Name:         res.Name,
			Path:         res.Path,
			BlobURL:      route.Blob(owner, res.Repo.Name, res.Repo.DefaultBranch, res.Path),
			RepoFullName: owner + "/" + res.Repo.Name,
			RepoURL:      route.Repo(owner, res.Repo.Name),
		})
	}
	return out
}

// emptyReason is the blankslate line for a query that matched nothing.
func emptyReason(typ string) string {
	switch typ {
	case view.SearchRepos:
		return "No repositories matched your search."
	case view.SearchIssues:
		return "No issues matched your search."
	case view.SearchPulls:
		return "No pull requests matched your search."
	case view.SearchCode:
		return "No code matched your search."
	default:
		return "No results matched your search."
	}
}

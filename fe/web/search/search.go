// Package search holds the Githome web front's search surface: the global
// /search page and the in-repo /{owner}/{repo}/search page. It parses the ?q=
// query with the same parser the REST search uses, calls the one domain search
// service the API also calls (so the page and the API never disagree about what
// matches or what a viewer may see), maps the hits into fe/view result models
// with every URL precomputed through fe/route, and renders one template. The
// result types the domain does not serve (users, commits) are absent from the
// type rail rather than shown disabled, so the UI never advertises a capability
// it does not have. A repository the viewer cannot read was turned into a hard
// 404 by the scoped Resolve middleware before any handler ran (the 404-not-403
// rule), and every control is a plain link or GET form that works with no
// JavaScript. See implementation/12 section 2.
package search

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/presenter"
)

// perPage is how many results one search page lists. It is a fixed window, not a
// user knob, matching the issues index and the code views' bounded lists.
const perPage = 25

// Deps are the search handlers' dependencies: the domain search service for every
// query, the repo service the scoped page resolves and read-gates the repository
// through, the presenter for avatar URLs, the render set, the view builder for
// the shell chrome, and a logger for the incomplete-walk notice.
type Deps struct {
	Search *domain.SearchService
	Repos  *domain.RepoService
	URLs   *presenter.URLBuilder
	Render *render.Set
	View   *view.Builder
	Logger *slog.Logger
}

// Handlers is the search handler set. One is built at boot and shared; it holds
// no per-request state.
type Handlers struct {
	search *domain.SearchService
	repos  *domain.RepoService
	urls   *presenter.URLBuilder
	render *render.Set
	view   *view.Builder
	log    *slog.Logger
}

// New wires the handler set from its dependencies.
func New(d Deps) *Handlers {
	return &Handlers{
		search: d.Search,
		repos:  d.Repos,
		urls:   d.URLs,
		render: d.Render,
		view:   d.View,
		log:    d.Logger,
	}
}

// repoCtxKey carries the resolved repository on the request context between the
// scoped Resolve middleware and the Scoped handler.
type repoCtxKey int

const keyRepo repoCtxKey = iota

// Resolve loads the repository named by the {owner} and {repo} path parameters
// for the in-repo search, read-gated for the viewer, and stores it on the
// context. A missing repository, or a private one the viewer cannot see, renders
// the same 404, so a private repo never leaks through the status code. This
// mirrors the issues and code-browsing Resolve so the surfaces gate identically.
func (h *Handlers) Resolve(next mizu.Handler) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Context()
		repo, err := h.repos.GetRepo(ctx, webmw.ViewerID(ctx), c.Param("owner"), c.Param("repo"))
		if errors.Is(err, domain.ErrRepoNotFound) {
			// The name may be a rename's old address: the redirect store keeps
			// old owner/name pairs pointing at the repository they now name.
			if moved, merr := h.repos.RepoRedirect(ctx, webmw.ViewerID(ctx), c.Param("owner"), c.Param("repo")); merr == nil {
				if target, ok := route.CanonicalRepoTarget(c.Request(), c.Param("owner"), c.Param("repo"), ownerLogin(moved), moved.Name); ok {
					return c.Redirect(http.StatusMovedPermanently, target)
				}
			}
			return h.render.RepoNotFound(c, h.view.Chrome(c, ""))
		}
		if err != nil {
			return err
		}
		// The lookup is case-insensitive; the URL is not. A wrong-cased owner or
		// name 301s to the canonical spelling instead of serving every variant.
		if target, ok := route.CanonicalRepoTarget(c.Request(), c.Param("owner"), c.Param("repo"), ownerLogin(repo), repo.Name); ok {
			return c.Redirect(http.StatusMovedPermanently, target)
		}
		r := c.Request()
		*r = *r.WithContext(context.WithValue(ctx, keyRepo, repo))
		return next(c)
	}
}

// globalTypes is the result-type rail on the global page, in display order. Code
// is included even though an unscoped code search needs a repo:/user:/org:
// qualifier: selecting it without a scope renders the scope-required blankslate,
// which is honest about what a host-wide code search can do.
var globalTypes = []string{view.SearchRepos, view.SearchCode, view.SearchIssues, view.SearchPulls}

// repoTypes is the rail inside a repository: the cross-repository types
// (repositories) drop out, since the scope is one repo, and code leads because an
// in-repo search most often greps the code.
var repoTypes = []string{view.SearchCode, view.SearchIssues, view.SearchPulls}

// Global renders /search, the host-wide search. An empty q renders the landing
// (no rows, no count) rather than an empty result set; otherwise it runs the
// active type and renders the results page.
func (h *Handlers) Global(c *mizu.Ctx) error {
	q := c.Query("q")
	typ := view.SearchTypeOr(c.Query("type"), view.SearchRepos, globalTypes)
	vm, err := h.build(c, req{
		scope:  view.ScopeGlobal,
		types:  globalTypes,
		active: typ,
		q:      q,
		sort:   c.Query("sort"),
		order:  c.Query("order"),
		page:   pageParam(c.Query("page")),
		action: route.Search(""),
		title:  searchTitle(q, ""),
	})
	if err != nil {
		return err
	}
	return h.render.Page(c, "search/page", vm)
}

// Scoped renders /{owner}/{repo}/search. The repo was resolved and read-gated by
// Resolve, so a private repo a viewer cannot read is already a 404 before this
// runs. It injects the repo: scope and drops the cross-repository types, with code
// as the default.
func (h *Handlers) Scoped(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.render.RepoNotFound(c, h.view.Chrome(c, ""))
	}
	q := c.Query("q")
	typ := view.SearchTypeOr(c.Query("type"), view.SearchCode, repoTypes)
	vm, err := h.build(c, req{
		scope:  view.ScopeRepo,
		repo:   repo,
		types:  repoTypes,
		active: typ,
		q:      q,
		sort:   c.Query("sort"),
		order:  c.Query("order"),
		page:   pageParam(c.Query("page")),
		action: route.RepoSearch(ownerLogin(repo), repo.Name, ""),
		title:  searchTitle(q, repo.Name),
	})
	if err != nil {
		return err
	}
	return h.render.Page(c, "search/page", vm)
}

// req is the resolved request the build step reads: the scope, the optional repo,
// the rail membership, the active type, and the raw facet inputs.
type req struct {
	scope  string
	repo   *domain.Repo
	types  []string
	active string
	q      string
	sort   string
	order  string
	page   int
	action string
	title  string
}

// repoFromContext returns the repository the Resolve middleware stored.
func repoFromContext(ctx context.Context) (*domain.Repo, bool) {
	repo, ok := ctx.Value(keyRepo).(*domain.Repo)
	return repo, ok
}

// pageParam parses a 1-based page number, clamping a missing or malformed value
// to the first page, the same rule the issues index uses.
func pageParam(s string) int {
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// facet builds a results URL that keeps the chosen facet values and drops the
// rest, encoding q so a value with spaces stays a single parameter. The type
// rail, the sort menu, and the pager each call it with the subset they preserve.
func (r req) facet(vals url.Values) string {
	raw := vals.Encode()
	if r.scope == view.ScopeRepo {
		return route.RepoSearch(ownerLogin(r.repo), r.repo.Name, raw)
	}
	return route.Search(raw)
}

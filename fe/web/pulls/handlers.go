// Package pulls holds the Githome web front's pull-request handlers: the index
// with its state tabs, the PR shell the four tabs hang off, the Conversation
// timeline, the Commits tab, the read-only Files-changed diff, and the merge box
// with its poll fragment and merge mutation. Each handler resolves and authorizes
// through the same domain.PRService the REST and GraphQL surfaces use, so the page
// and the API never disagree, maps the result into a fe/view model with every URL
// precomputed through fe/route, and renders through fe/render. A repository the
// viewer cannot see was turned into a hard 404 by the Resolve middleware before any
// handler ran (the 404-not-403 rule), and every mutation has a plain HTML form path
// that works with no JavaScript. The inline review threads and the review state
// machine over the Files tab arrive in F5; F4 ships the Files tab read-only. See
// implementation/09.
package pulls

import (
	"context"
	"errors"
	"log/slog"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/markup"
	"github.com/tamnd/githome/presenter"
)

// Deps are the pulls handlers' dependencies: the domain PR service for the shell,
// the diff, the commits, and the merge; the issue service for the Conversation
// timeline, since a PR shares its number and its comments with an issue; the repo
// service to resolve and read-gate the repository; the presenter for avatar URLs;
// the render set; the view builder for the shell chrome; the shared markup renderer
// for comment bodies; and a logger for the notices the views emit. A nil markup
// renderer falls back to escaped comment source.
type Deps struct {
	Pulls  *domain.PRService
	Issues *domain.IssueService
	Repos  *domain.RepoService
	URLs   *presenter.URLBuilder
	Render *render.Set
	View   *view.Builder
	Markup *markup.Renderer
	Logger *slog.Logger
}

// Handlers is the pulls handler set. One is built at boot and shared; it holds no
// per-request state.
type Handlers struct {
	pulls  *domain.PRService
	issues *domain.IssueService
	repos  *domain.RepoService
	urls   *presenter.URLBuilder
	render *render.Set
	view   *view.Builder
	markup *markup.Renderer
	log    *slog.Logger
}

// New wires the handler set from its dependencies.
func New(d Deps) *Handlers {
	return &Handlers{
		pulls:  d.Pulls,
		issues: d.Issues,
		repos:  d.Repos,
		urls:   d.URLs,
		render: d.Render,
		view:   d.View,
		markup: d.Markup,
		log:    d.Logger,
	}
}

// repoCtxKey carries the resolved repository on the request context between the
// Resolve middleware and the handlers.
type repoCtxKey int

const keyRepo repoCtxKey = iota

// Resolve loads the repository named by the {owner} and {repo} path parameters,
// read-gated for the viewer, and stores it on the context. A missing repository,
// or a private one the viewer cannot see, renders the same 404, so a private repo
// never leaks through the status code. This mirrors the issues and code-browsing
// Resolve so the three surfaces gate identically. It is the one place the repo is
// loaded; every handler reads it back with repoFromContext.
func (h *Handlers) Resolve(next mizu.Handler) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Context()
		repo, err := h.repos.GetRepo(ctx, webmw.ViewerID(ctx), c.Param("owner"), c.Param("repo"))
		if errors.Is(err, domain.ErrRepoNotFound) {
			return h.render.RepoNotFound(c, h.chrome(c, ""))
		}
		if err != nil {
			return err
		}
		r := c.Request()
		*r = *r.WithContext(context.WithValue(ctx, keyRepo, repo))
		return next(c)
	}
}

// repoFromContext returns the repository the Resolve middleware stored.
func repoFromContext(ctx context.Context) (*domain.Repo, bool) {
	repo, ok := ctx.Value(keyRepo).(*domain.Repo)
	return repo, ok
}

// loadPR resolves the {number} path parameter and the pull request, rendering the
// repo-shell 404 on any miss. The repo is already read-gated by Resolve; a pull
// request that does not exist, or a non-numeric or absent number, is 404-not-403
// here, the same soft 404 the issues surface renders. A nil return means the
// handler has already written the response and should return nil.
func (h *Handlers) loadPR(c *mizu.Ctx, repo *domain.Repo) (*domain.PullRequest, bool) {
	number, ok := numberParam(c.Param("number"))
	if !ok {
		_ = h.notFound(c)
		return nil, false
	}
	pr, err := h.pulls.GetPR(c.Context(), webmw.ViewerID(c.Context()), ownerLogin(repo), repo.Name, number)
	if isNotFound(err) {
		_ = h.notFound(c)
		return nil, false
	}
	if err != nil {
		_ = h.render.ServerError(c, err)
		return nil, false
	}
	return pr, true
}

// notFound renders the soft 404 in the repo shell: a missing PR, comment, or number
// inside a repository the viewer can see.
func (h *Handlers) notFound(c *mizu.Ctx) error {
	return h.render.RepoNotFound(c, h.chrome(c, ""))
}

// chrome builds the shell model for a pulls page through the view builder, so a
// pulls page wears the same header, theme, and viewer menu as any other page.
func (h *Handlers) chrome(c *mizu.Ctx, title string) view.Chrome {
	return h.view.Chrome(c, title)
}

// canWrite reports whether the signed-in viewer may write to the repository, the
// same owner-only rule the PR service enforces, read here so the page shows the
// merge and edit affordances only to a viewer who can use them. The service
// authorizes every mutation again, so this is a display gate, never the boundary.
func canWrite(repo *domain.Repo, viewerPK int64) bool {
	return viewerPK != 0 && viewerPK == repo.OwnerPK
}

// canComment reports whether the viewer may add a comment: any signed-in viewer
// who can see the repo. An anonymous viewer (PK 0) sees the timeline read-only.
func canComment(viewerPK int64) bool {
	return viewerPK != 0
}

// isNotFound reports whether err is one of the domain not-found sentinels the
// handlers turn into a soft 404. A PR missing in a visible repo is ErrPullNotFound;
// the underlying issue lookup can surface ErrIssueNotFound; a repo that vanished
// between the resolve and the read is ErrRepoNotFound.
func isNotFound(err error) bool {
	return errors.Is(err, domain.ErrPullNotFound) ||
		errors.Is(err, domain.ErrIssueNotFound) ||
		errors.Is(err, domain.ErrRepoNotFound)
}

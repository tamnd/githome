// Package checks holds the Githome web front's commit-checks surface: the page at
// /{owner}/{repo}/checks/{ref} that shows the status-check rollup for a ref, its
// check-run rows, and its commit-status rows. It is the backed slice of the
// Actions UI Spec 2005 doc 11 describes. Githome's domain models the check-run,
// check-suite, and commit-status state machines and their combined rollup (Spec
// 2003 docs 05 and 10), not the full Actions run engine (workflow runs, the needs
// job graph, live log streaming, artifacts, caches, deployments); those have no
// store behind them, so the front renders the checks it can back and leaves the
// run engine absent rather than faking it, the same honest absence the settings
// and search surfaces took for what they cannot yet back.
//
// The page is read-only and read-gated: the Resolve middleware loads the
// repository for the viewer, turning a missing or invisible repository into a 404
// so the surface never confirms a private repository's existence (the
// 404-not-403 rule). A ref that does not resolve is the repo-scoped soft 404. The
// page never mutates, so it carries no CSRF form; the re-run, cancel, approve, and
// dispatch controls doc 11 sketches need the unbacked run engine and are absent.
package checks

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

// Deps are the checks handlers' dependencies: the domain checks service for the
// rollup read, the repo service to resolve and read-gate the repository for the
// header bar, the render set, the view builder for the shell chrome, and a logger.
type Deps struct {
	Checks *domain.ChecksService
	Repos  *domain.RepoService
	Render *render.Set
	View   *view.Builder
	Logger *slog.Logger
}

// Handlers is the checks handler set. One is built at boot and shared; it holds no
// per-request state.
type Handlers struct {
	checks *domain.ChecksService
	repos  *domain.RepoService
	render *render.Set
	view   *view.Builder
	log    *slog.Logger
}

// New wires the handler set from its dependencies.
func New(d Deps) *Handlers {
	return &Handlers{
		checks: d.Checks,
		repos:  d.Repos,
		render: d.Render,
		view:   d.View,
		log:    d.Logger,
	}
}

// repoCtxKey carries the resolved repository on the request context between the
// Resolve middleware and the handler.
type repoCtxKey int

const keyRepo repoCtxKey = iota

// Resolve loads the repository named by the {owner} and {repo} path parameters,
// read-gated for the viewer, and stores it on the context. A missing repository
// and a private one the viewer cannot see both render the same 404, so the surface
// never leaks a repository's existence through the status code. The handler reads
// the repository back with repoFromContext for the header bar; the checks service
// re-authorizes the read again on the rollup call, so this gate is the front door,
// never the only lock.
func (h *Handlers) Resolve(next mizu.Handler) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Context()
		viewerPK := webmw.ViewerID(ctx)
		repo, err := h.repos.GetRepo(ctx, viewerPK, c.Param("owner"), c.Param("repo"))
		if errors.Is(err, domain.ErrRepoNotFound) {
			// The name may be a rename's old address: the redirect store keeps
			// old owner/name pairs pointing at the repository they now name.
			if moved, merr := h.repos.RepoRedirect(ctx, viewerPK, c.Param("owner"), c.Param("repo")); merr == nil {
				if target, ok := route.CanonicalRepoTarget(c.Request(), c.Param("owner"), c.Param("repo"), repoOwnerLogin(moved), moved.Name); ok {
					return c.Redirect(http.StatusMovedPermanently, target)
				}
			}
			return h.notFound(c)
		}
		if err != nil {
			return err
		}
		// The lookup is case-insensitive; the URL is not. A wrong-cased owner or
		// name 301s to the canonical spelling instead of serving every variant.
		if target, ok := route.CanonicalRepoTarget(c.Request(), c.Param("owner"), c.Param("repo"), repoOwnerLogin(repo), repo.Name); ok {
			return c.Redirect(http.StatusMovedPermanently, target)
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

// repoOwnerLogin returns the repo owner's canonical login, tolerating a repo
// assembled without its owner (which the resolver never does).
func repoOwnerLogin(r *domain.Repo) string {
	if r.Owner == nil {
		return ""
	}
	return r.Owner.Login
}

// notFound renders the repository 404 in the page shell, the page a missing or
// invisible repository's checks route renders.
func (h *Handlers) notFound(c *mizu.Ctx) error {
	return h.render.RepoNotFound(c, h.view.Chrome(c, ""))
}

// owner and name return the path parameters the URL builders and the header read.
// They come from the request rather than the resolved repo so a link round-trips
// the exact segments the viewer navigated.
func (h *Handlers) owner(c *mizu.Ctx) string { return c.Param("owner") }
func (h *Handlers) name(c *mizu.Ctx) string  { return c.Param("repo") }

// Index renders the checks page for a ref. The ref is the whole greedy tail, so a
// branch with slashes resolves as one ref; an empty or unresolvable ref is the
// soft 404 (the service reports ErrValidation when the ref does not resolve to a
// sha). The rollup folds the ref's statuses and check runs into the page model.
func (h *Handlers) Index(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	ref := strings.Trim(c.Param("rest"), "/")
	if ref == "" {
		return h.notFound(c)
	}
	rollup, err := h.checks.Rollup(ctx, webmw.ViewerID(ctx), h.owner(c), h.name(c), ref)
	if errors.Is(err, domain.ErrRepoNotFound) || errors.Is(err, domain.ErrValidation) {
		return h.notFound(c)
	}
	if err != nil {
		return err
	}
	return h.render.Page(c, "checks/index", h.build(c, repo, ref, rollup))
}

// safeExternalURL returns the URL only when it is an absolute http or https URL,
// the same guard the hook URL validation applies. A check run's details link and a
// commit status's target link are reported by an external client, so a relative,
// scheme-relative, or javascript: value is dropped rather than rendered into an
// href the viewer might click. An empty result tells the builder to omit the link.
func safeExternalURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return raw
}

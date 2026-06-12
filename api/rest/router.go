// Package rest implements Githome's REST API v3. It mounts onto a mizu router
// and is the only place HTTP request handling for the REST surface lives.
// Handlers call the domain layer for data and the presenter layer for
// rendering; they never touch the store or git directly.
package rest

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/config"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
)

// Deps are the dependencies the REST surface needs to mount. The auth, domain,
// and presenter members arrive in M1; a zero member leaves its routes unmounted,
// which keeps the M0 foundation tests able to build a minimal surface.
type Deps struct {
	Config   config.Config
	Logger   *slog.Logger
	Ready    Pinger
	Auth     *auth.Service
	Users    *domain.UserService
	Repos    *domain.RepoService
	Issues   *domain.IssueService
	Pulls    *domain.PRService
	Reviews  *domain.ReviewService
	Checks   *domain.ChecksService
	Keys     *domain.KeyService
	Teams    *domain.TeamService
	Hooks    *domain.HookService
	Events   *domain.EventService
	Search   *domain.SearchService
	Releases *domain.ReleaseService
	Gists    *domain.GistService
	// Notifications maintains and serves the per-user inbox. Its routes also
	// need Repos, both to gate the repo-scoped list and to render each
	// thread's repository summary.
	Notifications *domain.NotificationService
	URLs          *presenter.URLBuilder
	NodeFormat    nodeid.Format

	// WebFront reports that the server-rendered web front is mounted on the same
	// router and owns the bare root namespace (/{owner}/{repo}). When set, the
	// dotcom-style root API mount and the root catch-all 404 are omitted: the API
	// answers only under the GHES /api/v3 prefix (GraphQL under /api/graphql),
	// which is the single-host layout GHES itself uses. Without it the API also
	// answers at the bare root, the github.com-style shape, but those wildcard
	// routes cannot coexist with the web front's own /{owner}/{repo} wildcards on
	// one net/http mux: the mux rejects the overlapping patterns at registration.
	WebFront bool
}

// Mount wires the REST routes onto root. The API is served at the GHES-style
// /api/v3 prefix and, unless the web front owns the root (Deps.WebFront), also at
// the bare github.com-style root, sharing one set of handlers and the
// version/media-type middleware. Health probes sit outside that chain, and any
// unmatched path returns the GitHub-shaped 404 (again, unless the web front owns
// the root and supplies its own).
func Mount(root *mizu.Router, d Deps) {
	// Drop mizu's default stderr request logger; Githome logs through its own
	// configured slog handler and error handler.
	root.ClearMiddleware()
	root.Use(requestID)
	root.ErrorHandler(errorHandler(d.Logger))

	root.Get("/healthz", handleHealthz)
	root.Get("/readyz", handleReadyz(d.Ready))

	// The OAuth device flow lives at the bare root, outside the API version,
	// media-type, and auth middleware.
	if d.Auth != nil {
		mountOAuth(root, d.Auth)
	}

	// The OAuth discovery document lives at /.well-known/oauth-authorization-server
	// (RFC 8414). git-credential-oauth and GCM read it to locate the authorize and
	// token endpoints without hardcoding paths.
	root.Get("/.well-known/oauth-authorization-server", handleOAuthDiscovery(d))

	// The limiter sits after the auth middleware so each charge lands on the
	// resolved actor's bucket; one instance backs both API mounts and the
	// /rate_limit handler, so headers and body always report the same numbers.
	limiter := newRateLimiter(d.Config.RateLimit)
	api := root.With(apiVersion, mediaType, enterpriseVersion, maxBody(d.Config.Server.MaxBodyBytes))
	if d.Auth != nil {
		api = api.With(authMiddleware(d.Auth, limiter))
	}
	api = api.With(rateLimit(limiter))
	mountAPI(api.Prefix("/api/v3"), d, limiter)
	if d.URLs != nil {
		// The prefixed root document also answers without the trailing slash, the
		// form Octokit and gh build from a configured .../api/v3 base URL.
		api.Get("/api/v3", handleAPIRoot(d))
	}

	// Asset uploads go to /api/uploads/repos/... (GHES upload base convention).
	// go-github, GoReleaser and gh all construct the upload URL from the release's
	// upload_url field which Githome sets to this path.
	if d.Releases != nil {
		api.Prefix("/api/uploads").Post("/repos/{owner}/{repo}/releases/{release_id}/assets", requireScope(handleReleaseAssetUpload(d), "repo", "public_repo"))
	}

	if !d.WebFront {
		// The bare-root, github.com-style API and its root catch-all only mount
		// when the web front is not sharing this router. With the front present
		// these wildcards would collide with /{owner}/{repo}, and the front owns
		// the root 404.
		mountAPI(api, d, limiter)
		root.Compat.Handle("/", http.HandlerFunc(notFoundHandler))
	}
}

// mountAPI registers the versioned API endpoints on r, which already carries the
// API middleware chain. limiter is the live rate limiter behind that chain's
// headers, which GET /rate_limit reads so body and headers agree.
func mountAPI(r *mizu.Router, d Deps, limiter *rateLimiter) {
	if d.URLs != nil {
		r.Get("/{$}", handleAPIRoot(d))
	}
	r.Get("/meta", handleMeta(d.Config))
	r.Get("/versions", handleVersions)
	r.Get("/rate_limit", handleRateLimit(d.Config, limiter))
	if d.Users != nil {
		r.Get("/user", handleUserGet(d))
		mountUsers(r, d)
		mountOrgs(r, d)
	}
	if d.Repos != nil {
		if d.Users != nil {
			r.Get("/user/repos", handleUserReposList(d))
			r.Post("/user/repos", requireScope(handleUserRepoCreate(d), "repo", "public_repo"))
		}
		mountRepos(r, d)
	}
	if d.Issues != nil {
		mountIssues(r, d)
	}
	if d.Pulls != nil {
		mountPulls(r, d)
	}
	if d.Reviews != nil {
		mountReviews(r, d)
	}
	if d.Checks != nil {
		mountChecks(r, d)
	}
	if d.Keys != nil {
		mountKeys(r, d)
	}
	if d.Teams != nil {
		mountTeams(r, d)
	}
	mountGists(r, d)
	mountRepoExtra(r, d)
	mountMiscCompat(r, d)
	if d.Hooks != nil {
		mountHooks(r, d)
	}
	if d.Events != nil {
		mountEvents(r, d)
	}
	if d.Search != nil {
		mountSearch(r, d)
	}
	if d.Releases != nil {
		mountReleases(r, d)
	}
	if d.Auth != nil {
		mountApp(r, d)
	}
	if d.Notifications != nil && d.Repos != nil {
		mountNotifications(r, d)
	}
}

// mountReleases registers the releases and release assets endpoints on r.
// The upload endpoint lives at a separate /api/uploads/ prefix (GHES convention)
// and is wired directly in Mount() against the same upload router.
//
// The three two-segment GET shapes under /releases/ (tags/{tag},
// {release_id}/assets, assets/{asset_id}) overlap without any one being more
// specific, which net/http's ServeMux rejects at registration, so a single
// dispatcher owns the GET {a}/{b} shape and routes by the literal segment.
func mountReleases(r *mizu.Router, d Deps) {
	r.Get("/repos/{owner}/{repo}/releases", handleReleasesList(d))
	r.Post("/repos/{owner}/{repo}/releases", requireScope(handleReleaseCreate(d), "repo", "public_repo"))
	r.Get("/repos/{owner}/{repo}/releases/latest", handleReleaseLatest(d))
	r.Get("/repos/{owner}/{repo}/releases/{release_id}", handleReleaseGet(d))
	r.Patch("/repos/{owner}/{repo}/releases/{release_id}", requireScope(handleReleaseEdit(d), "repo", "public_repo"))
	r.Delete("/repos/{owner}/{repo}/releases/{release_id}", requireScope(handleReleaseDelete(d), "repo", "public_repo"))
	r.Get("/repos/{owner}/{repo}/releases/{seg}/{sub}", handleReleaseSubresource(d))
	r.Patch("/repos/{owner}/{repo}/releases/assets/{asset_id}", requireScope(handleReleaseAssetEdit(d), "repo", "public_repo"))
	r.Delete("/repos/{owner}/{repo}/releases/assets/{asset_id}", requireScope(handleReleaseAssetDelete(d), "repo", "public_repo"))
}

// mountSearch registers the search endpoints on r.
func mountSearch(r *mizu.Router, d Deps) {
	r.Get("/search/issues", handleSearchIssues(d))
	r.Get("/search/repositories", handleSearchRepositories(d))
	r.Get("/search/code", handleSearchCode(d))
}

// mountPulls registers the pull request endpoints on r. The diff and patch
// bodies of a single pull request are negotiated inside the GET handler from the
// Accept header rather than from a path suffix, matching GitHub.
func mountPulls(r *mizu.Router, d Deps) {
	r.Get("/repos/{owner}/{repo}/pulls", handlePullsList(d))
	r.Post("/repos/{owner}/{repo}/pulls", handlePullCreate(d))
	r.Get("/repos/{owner}/{repo}/pulls/{number}", handlePullGet(d))
	r.Patch("/repos/{owner}/{repo}/pulls/{number}", handlePullUpdate(d))
	r.Put("/repos/{owner}/{repo}/pulls/{number}/merge", handlePullMerge(d))
	r.Put("/repos/{owner}/{repo}/pulls/{number}/update-branch", handlePullUpdateBranch(d))
	r.Post("/repos/{owner}/{repo}/pulls/{number}/requested_reviewers", handleRequestedReviewersAdd(d))
	r.Get("/repos/{owner}/{repo}/pulls/{number}/requested_reviewers", handleRequestedReviewersList(d))

	// The pull request sub-collections (files, commits, comments, reviews) and the
	// standalone /pulls/comments/{id} comment lookup all read as /pulls/{x}/{y},
	// which net/http's mux rejects as an ambiguous pair, so one dispatcher fans
	// them out. The files and commits diffs are served even without the review
	// service mounted, so this route always carries them.
	r.Get("/repos/{owner}/{repo}/pulls/{seg1}/{seg2}", handlePullSubGet(d))
	// DELETE /pulls/comments/{id} and DELETE /pulls/{number}/requested_reviewers
	// share the same two-segment space, so a single dispatcher fans them out.
	r.Delete("/repos/{owner}/{repo}/pulls/{seg1}/{seg2}", handlePullDeleteDispatch(d))
}

// mountReviews registers the code review endpoints on r: reviews and their
// submit, dismiss, and get shapes, plus inline review comments and replies.
func mountReviews(r *mizu.Router, d Deps) {
	r.Post("/repos/{owner}/{repo}/pulls/{number}/reviews", handleReviewCreate(d))
	r.Get("/repos/{owner}/{repo}/pulls/{number}/reviews/{review_id}", handleReviewGet(d))
	r.Post("/repos/{owner}/{repo}/pulls/{number}/reviews/{review_id}/events", handleReviewSubmit(d))
	r.Put("/repos/{owner}/{repo}/pulls/{number}/reviews/{review_id}/dismissals", handleReviewDismiss(d))

	r.Post("/repos/{owner}/{repo}/pulls/{number}/comments", handleReviewCommentCreate(d))
	r.Post("/repos/{owner}/{repo}/pulls/{number}/comments/{comment_id}/replies", handlePullCommentReply(d))
	r.Post("/repos/{owner}/{repo}/pulls/comments/{comment_id}/replies", handleReviewCommentReply(d))
}

// mountIssues registers the issue, comment, label, milestone, and reaction
// endpoints on r.
func mountIssues(r *mizu.Router, d Deps) {
	r.Get("/repos/{owner}/{repo}/issues", handleIssuesList(d))
	r.Post("/repos/{owner}/{repo}/issues", handleIssueCreate(d))
	r.Get("/repos/{owner}/{repo}/issues/{number}", handleIssueGet(d))
	r.Patch("/repos/{owner}/{repo}/issues/{number}", handleIssueEdit(d))

	// The two GET comment shapes share one dispatcher because net/http's mux
	// rejects "/issues/{number}/comments" and "/issues/comments/{id}" as an
	// ambiguous pair; POST, PATCH, and DELETE do not collide and stay distinct.
	r.Get("/repos/{owner}/{repo}/issues/{seg1}/{seg2}", handleIssueCommentsGet(d))
	r.Post("/repos/{owner}/{repo}/issues/{number}/comments", handleIssueCommentCreate(d))
	r.Patch("/repos/{owner}/{repo}/issues/comments/{id}", handleCommentEdit(d))
	r.Delete("/repos/{owner}/{repo}/issues/{seg1}/{seg2}", handleIssueDeleteDispatch(d))

	r.Get("/repos/{owner}/{repo}/labels", handleLabelsList(d))
	r.Post("/repos/{owner}/{repo}/labels", handleLabelCreate(d))
	r.Get("/repos/{owner}/{repo}/labels/{name}", handleLabelGet(d))
	r.Patch("/repos/{owner}/{repo}/labels/{name}", handleLabelEdit(d))
	r.Delete("/repos/{owner}/{repo}/labels/{name}", handleLabelDelete(d))

	r.Get("/repos/{owner}/{repo}/milestones", handleMilestonesList(d))
	r.Post("/repos/{owner}/{repo}/milestones", handleMilestoneCreate(d))
	r.Get("/repos/{owner}/{repo}/milestones/{number}", handleMilestoneGet(d))
	r.Patch("/repos/{owner}/{repo}/milestones/{number}", handleMilestoneEdit(d))
	r.Delete("/repos/{owner}/{repo}/milestones/{number}", handleMilestoneDelete(d))

	r.Get("/repos/{owner}/{repo}/issues/{number}/reactions", handleIssueReactionsList(d))
	r.Post("/repos/{owner}/{repo}/issues/{number}/reactions", handleIssueReactionCreate(d))
	r.Delete("/repos/{owner}/{repo}/issues/{number}/reactions/{id}", handleIssueReactionDelete(d))

	r.Get("/repos/{owner}/{repo}/issues/comments/{id}/reactions", handleCommentReactionsList(d))
	r.Post("/repos/{owner}/{repo}/issues/comments/{id}/reactions", handleCommentReactionCreate(d))
	r.Delete("/repos/{owner}/{repo}/issues/comments/{id}/reactions/{reaction_id}", handleCommentReactionDelete(d))
}

// mountKeys registers the deploy key, user key, and branch protection endpoints on r.
func mountKeys(r *mizu.Router, d Deps) {
	// Deploy keys live under the repo scope, like on GitHub.
	r.Get("/repos/{owner}/{repo}/keys", requireScope(handleDeployKeysList(d), "repo"))
	r.Post("/repos/{owner}/{repo}/keys", requireScope(handleDeployKeyCreate(d), "repo"))
	r.Get("/repos/{owner}/{repo}/keys/{key_id}", requireScope(handleDeployKeyGet(d), "repo"))
	r.Delete("/repos/{owner}/{repo}/keys/{key_id}", requireScope(handleDeployKeyDelete(d), "repo"))
	// User SSH keys carry the public_key scope family per verb; the scope
	// lattice lets admin:public_key and write:public_key through a read gate.
	r.Get("/user/keys", requireScope(handleUserKeysList(d), "read:public_key"))
	r.Post("/user/keys", requireScope(handleUserKeyCreate(d), "write:public_key"))
	r.Get("/user/keys/{key_id}", requireScope(handleUserKeyGet(d), "read:public_key"))
	r.Delete("/user/keys/{key_id}", requireScope(handleUserKeyDelete(d), "admin:public_key"))
	// Branch protection.
	r.Get("/repos/{owner}/{repo}/branches/{branch}/protection", handleBranchProtectionGet(d))
	r.Put("/repos/{owner}/{repo}/branches/{branch}/protection", requireScope(handleBranchProtectionPut(d), "repo"))
	r.Delete("/repos/{owner}/{repo}/branches/{branch}/protection", requireScope(handleBranchProtectionDelete(d), "repo"))
}

// mountApp registers the GitHub App meta and installation-token endpoints on r.
// All require App JWT auth; the handlers reject other credential kinds with 401.
func mountApp(r *mizu.Router, d Deps) {
	r.Get("/app", handleAppGet(d))
	r.Get("/app/installations", handleAppInstallationsList(d))
	r.Post("/app/installations/{installation_id}/access_tokens", handleInstallationAccessTokens(d))
}

// mountUsers registers the public user profile and listing endpoints on r.
func mountUsers(r *mizu.Router, d Deps) {
	r.Get("/users/{username}", handlePublicUserGet(d))
	r.Get("/users/{username}/repos", handlePublicUserRepos(d))
}

// mountOrgs registers the organization profile and repository endpoints on r.
func mountOrgs(r *mizu.Router, d Deps) {
	r.Get("/orgs/{org}", handleOrgGet(d))
	r.Get("/orgs/{org}/repos", handleOrgReposList(d))
	r.Post("/orgs/{org}/repos", requireScope(handleOrgRepoCreate(d), "repo", "public_repo"))
}

// mountRepos registers the repository and git-read endpoints on r.
func mountRepos(r *mizu.Router, d Deps) {
	r.Get("/repos/{owner}/{repo}", handleRepoGet(d))
	r.Patch("/repos/{owner}/{repo}", requireScope(handleRepoUpdate(d), "repo", "public_repo"))
	r.Delete("/repos/{owner}/{repo}", requireScope(handleRepoDelete(d), "delete_repo"))
	r.Get("/repos/{owner}/{repo}/branches", handleBranches(d))
	r.Get("/repos/{owner}/{repo}/branches/{branch}", handleBranch(d))
	r.Get("/repos/{owner}/{repo}/tags", handleTags(d))
	r.Get("/repos/{owner}/{repo}/commits", handleCommits(d))
	r.Get("/repos/{owner}/{repo}/contents", handleContents(d))
	r.Get("/repos/{owner}/{repo}/contents/{path...}", handleContents(d))
	r.Get("/repos/{owner}/{repo}/git/blobs/{sha}", handleBlob(d))
	r.Get("/repos/{owner}/{repo}/git/trees/{sha}", handleTree(d))
	r.Get("/repos/{owner}/{repo}/git/commits/{sha}", handleGitCommit(d))
	r.Get("/repos/{owner}/{repo}/git/refs", handleRefs(d))
	r.Get("/repos/{owner}/{repo}/git/ref/{ref...}", handleRef(d))
	r.Post("/repos/{owner}/{repo}/git/refs", requireScope(handleCreateRef(d), "repo", "public_repo"))
	r.Patch("/repos/{owner}/{repo}/git/refs/{ref...}", requireScope(handleUpdateRef(d), "repo", "public_repo"))
	r.Delete("/repos/{owner}/{repo}/git/refs/{ref...}", requireScope(handleDeleteRef(d), "repo", "public_repo"))
}

// errorHandler turns a handler-returned error or a recovered panic into the
// GitHub-shaped 500 envelope, logging it with the request id. Handlers write
// their own success and error responses and return nil; this fires only for the
// unexpected.
func errorHandler(log *slog.Logger) func(*mizu.Ctx, error) {
	return func(c *mizu.Ctx, err error) {
		if log != nil {
			attrs := []any{
				"request_id", c.Header().Get("X-GitHub-Request-Id"),
				"path", c.Request().URL.Path,
			}
			if panicErr, ok := errors.AsType[*mizu.PanicError](err); ok {
				log.ErrorContext(c.Context(), "panic recovered",
					append(attrs, "err", panicErr.Value, "stack", string(panicErr.Stack))...)
			} else {
				log.ErrorContext(c.Context(), "handler error", append(attrs, "err", err)...)
			}
		}
		writeError(c.Writer(), &apiError{Status: http.StatusInternalServerError, Message: "Server Error", DocURL: docRoot})
	}
}

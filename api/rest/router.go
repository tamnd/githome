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
	Config     config.Config
	Logger     *slog.Logger
	Ready      Pinger
	Auth       *auth.Service
	Users      *domain.UserService
	Repos      *domain.RepoService
	Issues     *domain.IssueService
	Pulls      *domain.PRService
	Reviews    *domain.ReviewService
	Checks     *domain.ChecksService
	Hooks      *domain.HookService
	Events     *domain.EventService
	Search     *domain.SearchService
	URLs       *presenter.URLBuilder
	NodeFormat nodeid.Format
}

// Mount wires the REST routes onto root. The API is served both at the
// GHES-style /api/v3 prefix and at the bare github.com-style root, sharing one
// set of handlers and the version/media-type middleware. Health probes sit
// outside that chain, and any unmatched path returns the GitHub-shaped 404.
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

	api := root.With(apiVersion, mediaType, maxBody(d.Config.Server.MaxBodyBytes))
	if d.Auth != nil {
		api = api.With(authMiddleware(d.Auth))
	}
	mountAPI(api.Prefix("/api/v3"), d)
	mountAPI(api, d)

	root.Compat.Handle("/", http.HandlerFunc(notFoundHandler))
}

// mountAPI registers the versioned API endpoints on r, which already carries the
// API middleware chain.
func mountAPI(r *mizu.Router, d Deps) {
	r.Get("/meta", handleMeta(d.Config))
	r.Get("/rate_limit", handleRateLimit(d.Config))
	if d.Users != nil {
		r.Get("/user", handleUserGet(d))
	}
	if d.Repos != nil {
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
	if d.Hooks != nil {
		mountHooks(r, d)
	}
	if d.Events != nil {
		mountEvents(r, d)
	}
	if d.Search != nil {
		mountSearch(r, d)
	}
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
	r.Put("/repos/{owner}/{repo}/pulls/{number}/merge", handlePullMerge(d))

	// The pull request sub-collections (files, commits, comments, reviews) and the
	// standalone /pulls/comments/{id} comment lookup all read as /pulls/{x}/{y},
	// which net/http's mux rejects as an ambiguous pair, so one dispatcher fans
	// them out. The files and commits diffs are served even without the review
	// service mounted, so this route always carries them.
	r.Get("/repos/{owner}/{repo}/pulls/{seg1}/{seg2}", handlePullSubGet(d))
}

// mountReviews registers the code review endpoints on r: reviews and their
// submit, dismiss, and get shapes, plus inline review comments and replies.
func mountReviews(r *mizu.Router, d Deps) {
	r.Post("/repos/{owner}/{repo}/pulls/{number}/reviews", handleReviewCreate(d))
	r.Get("/repos/{owner}/{repo}/pulls/{number}/reviews/{review_id}", handleReviewGet(d))
	r.Post("/repos/{owner}/{repo}/pulls/{number}/reviews/{review_id}/events", handleReviewSubmit(d))
	r.Put("/repos/{owner}/{repo}/pulls/{number}/reviews/{review_id}/dismissals", handleReviewDismiss(d))

	r.Post("/repos/{owner}/{repo}/pulls/{number}/comments", handleReviewCommentCreate(d))
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
	r.Delete("/repos/{owner}/{repo}/issues/comments/{id}", handleCommentDelete(d))

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

// mountRepos registers the repository and git-read endpoints on r.
func mountRepos(r *mizu.Router, d Deps) {
	r.Get("/repos/{owner}/{repo}", handleRepoGet(d))
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
	r.Post("/repos/{owner}/{repo}/git/refs", handleCreateRef(d))
	r.Patch("/repos/{owner}/{repo}/git/refs/{ref...}", handleUpdateRef(d))
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

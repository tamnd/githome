package rest

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
)

// handleRepoLanguages serves GET /repos/{owner}/{repo}/languages.
// Returns an empty object — language detection is not implemented.
func handleRepoLanguages(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		if _, err := loadRepo(d, c); err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, map[string]any{})
		return nil
	}
}

// handleRepoContributors serves GET /repos/{owner}/{repo}/contributors.
// Returns an empty array — contributor detection is not implemented.
func handleRepoContributors(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		if _, err := loadRepo(d, c); err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, []any{})
		return nil
	}
}

// handleRepoCollaboratorsList serves GET /repos/{owner}/{repo}/collaborators:
// the owner first as the admin, then every stored grant oldest first, each
// entry a user object extended with role_name and the permission booleans.
func handleRepoCollaboratorsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		page, perr := parsePageFor(c, "Repository")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		out := []collaboratorUser{collaboratorObject(d, repo.Owner, "admin")}
		if d.Teams != nil {
			grants, err := d.Teams.ListCollaborators(c.Request().Context(), repo.PK)
			if err != nil {
				return err
			}
			for _, g := range grants {
				out = append(out, collaboratorObject(d, g.User, g.Permission))
			}
		}
		out = paginateSlice(&page, out)
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleOrgMembersList serves GET /orgs/{org}/members.
func handleOrgMembersList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		org, err := d.Users.ByLogin(c.Request().Context(), c.Param("org"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, []any{
			d.URLs.SimpleUser(org, d.NodeFormat),
		})
		_ = actor
		return nil
	}
}

// handleOrgMemberGet serves GET /orgs/{org}/members/{username}.
func handleOrgMemberGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		_, err := d.Users.ByLogin(c.Request().Context(), c.Param("username"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		// In a real org system we'd check membership. For now return 204 (member).
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleUserEmails serves GET /user/emails.
func handleUserEmails(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		user, err := d.Users.Viewer(ctx, actor.UserID)
		if err != nil {
			return err
		}
		email := ""
		if user.Email != nil {
			email = *user.Email
		}
		out := []any{
			map[string]any{
				"email":      email,
				"primary":    true,
				"verified":   true,
				"visibility": "public",
			},
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleUserOrgs serves GET /user/orgs.
func handleUserOrgs(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		writeJSON(c.Writer(), http.StatusOK, []any{})
		return nil
	}
}

// handleUserFollowingCheck serves GET /user/following/{username}.
func handleUserFollowingCheck(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		// Users have no follow relationship - return 404 per GitHub's
		// "not following" response.
		c.Writer().WriteHeader(http.StatusNotFound)
		return nil
	}
}

// handlePublicUserKeys serves GET /users/{username}/keys.
func handlePublicUserKeys(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		u, err := d.Users.ByLogin(ctx, c.Param("username"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		pk, err := d.Users.PKByLogin(ctx, u.Login)
		if err != nil {
			return err
		}
		keys, err := d.Keys.ListUserKeys(ctx, pk)
		if err != nil {
			return err
		}
		out := make([]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, map[string]any{
				"id":  k.DBID,
				"key": k.PublicKey,
			})
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleWebhookPing serves POST /repos/{owner}/{repo}/hooks/{hook_id}/pings.
func handleWebhookPing(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		owner, repo := c.Param("owner"), c.Param("repo")
		hookID, ok := pathInt64(c, "hook_id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		// A ping is a real delivery: the worker POSTs the {zen, hook_id, hook}
		// body to the endpoint, it does not just acknowledge here.
		err := d.Hooks.PingHook(ctx, actor.UserID, owner, repo, hookID)
		if errors.Is(err, domain.ErrHookNotFound) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrRepoNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleSingleCommitGet serves GET /repos/{owner}/{repo}/commits/{sha}.
func handleSingleCommitGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		sha := c.Param("sha")
		commit, err := d.Repos.GetCommit(repo, sha)
		if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		rc := d.URLs.RepoCommit(repo.Owner.Login, repo.Name, repo.ID, commit)
		conditionalJSON(c.Writer(), c.Request(), http.StatusOK, rc)
		return nil
	}
}

// handleRepoTeamsList serves GET /repos/{owner}/{repo}/teams.
func handleRepoTeamsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		writeJSON(c.Writer(), http.StatusOK, []any{})
		return nil
	}
}

// handleIssueEventsList serves GET /repos/{owner}/{repo}/issues/{number}/events.
func handleIssueEventsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		writeJSON(c.Writer(), http.StatusOK, []any{})
		return nil
	}
}

// handleIssueTimeline serves GET /repos/{owner}/{repo}/issues/{number}/timeline.
func handleIssueTimeline(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		writeJSON(c.Writer(), http.StatusOK, []any{})
		return nil
	}
}

// handleSearchUsers serves GET /search/users.
func handleSearchUsers(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		writeJSON(c.Writer(), http.StatusOK, map[string]any{
			"total_count":        0,
			"incomplete_results": false,
			"items":              []any{},
		})
		return nil
	}
}

// handleSearchCommits serves GET /search/commits.
func handleSearchCommits(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		writeJSON(c.Writer(), http.StatusOK, map[string]any{
			"total_count":        0,
			"incomplete_results": false,
			"items":              []any{},
		})
		return nil
	}
}

// handleSearchTopics serves GET /search/topics.
func handleSearchTopics(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		writeJSON(c.Writer(), http.StatusOK, map[string]any{
			"total_count":        0,
			"incomplete_results": false,
			"items":              []any{},
		})
		return nil
	}
}

// handleSearchLabels serves GET /search/labels.
func handleSearchLabels(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		writeJSON(c.Writer(), http.StatusOK, map[string]any{
			"total_count":        0,
			"incomplete_results": false,
			"items":              []any{},
		})
		return nil
	}
}

// handleCheckSuiteGet serves GET /repos/{owner}/{repo}/check-suites/{suite_id}.
func handleCheckSuiteGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		suiteID, ok := pathInt64(c, "suite_id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		// Scan the ref-level list to find the suite by DB ID.
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		suites, _, err := d.Checks.ListCheckSuites(ctx, actor.UserID, repo.Owner.Login, repo.Name, "HEAD")
		if err != nil {
			return err
		}
		for _, s := range suites {
			if s.ID == suiteID {
				writeJSON(c.Writer(), http.StatusOK, d.URLs.CheckSuite(repo.Owner.Login, repo.Name, s, d.NodeFormat))
				return nil
			}
		}
		writeError(c.Writer(), errNotFound())
		return nil
	}
}

// handleOrgTeamsList serves GET /orgs/{org}/teams.
func handleOrgTeamsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		writeJSON(c.Writer(), http.StatusOK, []any{})
		return nil
	}
}

// handleOrgTeamReposList serves GET /orgs/{org}/teams/{team_slug}/repos.
func handleOrgTeamReposList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		writeJSON(c.Writer(), http.StatusOK, []any{})
		return nil
	}
}

// handleForkCreate serves POST /repos/{owner}/{repo}/forks. GitHub answers
// 202 with the fork's repository object; the copy here is synchronous, so the
// 202 is already complete when it lands. Re-forking an already-forked source
// returns the existing fork, also a 202.
func handleForkCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		var body struct {
			Organization      string `json:"organization"`
			Name              string `json:"name"`
			DefaultBranchOnly bool   `json:"default_branch_only"`
		}
		if !decodeJSONOpt(c, &body) {
			return nil
		}
		fork, err := d.Repos.ForkRepo(ctx, actor.UserID, repo, domain.ForkInput{
			Organization:      body.Organization,
			Name:              body.Name,
			DefaultBranchOnly: body.DefaultBranchOnly,
		})
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if errors.Is(err, domain.ErrForbidden) {
			writeError(c.Writer(), errForbidden("Must have admin rights to the organization to fork into it"))
			return nil
		}
		if errors.Is(err, domain.ErrRepoExists) {
			writeError(c.Writer(), errForbidden("Name already exists on this account"))
			return nil
		}
		if err != nil {
			return err
		}
		det, err := repoDetail(d, c, fork)
		if err != nil {
			return err
		}
		perm, err := repoPermissions(ctx, d, actor, fork)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusAccepted, d.URLs.RepositoryFull(fork, d.NodeFormat, perm, det))
		return nil
	}
}

// handleForksList serves GET /repos/{owner}/{repo}/forks, the live forks the
// caller can see, newest first. The sort parameter is accepted for wire
// compatibility; with no stars or watchers tracked, every order collapses to
// newest first.
func handleForksList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		page, perr := parsePageFor(c, "Repository")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		forks, err := d.Repos.ListForks(ctx, actor.UserID, repo.PK)
		if err != nil {
			return err
		}
		forks = paginateSlice(&page, forks)
		out := make([]any, 0, len(forks))
		for _, f := range forks {
			perm, err := repoPermissions(ctx, d, actor, f)
			if err != nil {
				return err
			}
			out = append(out, d.URLs.Repository(f, d.NodeFormat, perm))
		}
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleInstallationRepos serves GET /installation/repositories.
func handleInstallationRepos(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		writeJSON(c.Writer(), http.StatusOK, map[string]any{
			"total_count":  0,
			"repositories": []any{},
		})
		return nil
	}
}

// handleRepoInstallation serves GET /repos/{owner}/{repo}/installation.
func handleRepoInstallation(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		writeError(c.Writer(), errNotFound())
		return nil
	}
}

// handleCheckSuiteCreate serves POST /repos/{owner}/{repo}/check-suites
// (GitHub auto-creates; we accept but return a synthetic 200).
func handleCheckSuiteCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, map[string]any{
			"id":         0,
			"status":     "completed",
			"created_at": time.Now().UTC().Format(time.RFC3339),
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		})
		return nil
	}
}

// mountMiscCompat registers all the "compat gap" endpoints that don't belong
// to a primary sub-system file.
func mountMiscCompat(r *mizu.Router, d Deps) {
	if d.Users != nil {
		r.Get("/user/emails", handleUserEmails(d))
		r.Get("/user/orgs", handleUserOrgs(d))
		r.Get("/user/following/{username}", handleUserFollowingCheck(d))
		r.Get("/orgs/{org}/members", handleOrgMembersList(d))
		r.Get("/orgs/{org}/members/{username}", handleOrgMemberGet(d))
		r.Get("/users/{username}/keys", handlePublicUserKeys(d))
		r.Get("/search/users", handleSearchUsers(d))
	}
	if d.Repos != nil {
		r.Get("/repos/{owner}/{repo}/languages", handleRepoLanguages(d))
		r.Get("/repos/{owner}/{repo}/contributors", handleRepoContributors(d))
		r.Get("/repos/{owner}/{repo}/collaborators", handleRepoCollaboratorsList(d))
		r.Get("/repos/{owner}/{repo}/teams", handleRepoTeamsList(d))
		r.Get("/repos/{owner}/{repo}/forks", handleForksList(d))
		r.Post("/repos/{owner}/{repo}/forks", requireScope(handleForkCreate(d), "repo", "public_repo"))
		r.Get("/repos/{owner}/{repo}/commits/{sha}", handleSingleCommitGet(d))
		r.Post("/repos/{owner}/{repo}/git/blobs", requireScope(handleGitBlobCreate(d), "repo", "public_repo"))
		r.Post("/repos/{owner}/{repo}/git/trees", requireScope(handleGitTreeCreate(d), "repo", "public_repo"))
		r.Post("/repos/{owner}/{repo}/git/commits", requireScope(handleGitCommitCreate(d), "repo", "public_repo"))
		r.Get("/repos/{owner}/{repo}/git/tags/{sha}", handleGitTagGet(d))
		r.Post("/repos/{owner}/{repo}/git/tags", requireScope(handleGitTagCreate(d), "repo", "public_repo"))
		r.Get("/search/commits", handleSearchCommits(d))
		r.Get("/search/topics", handleSearchTopics(d))
		r.Get("/search/labels", handleSearchLabels(d))
		r.Get("/installation/repositories", handleInstallationRepos(d))
		r.Get("/repos/{owner}/{repo}/installation", handleRepoInstallation(d))
		r.Post("/repos/{owner}/{repo}/check-suites", handleCheckSuiteCreate(d))
	}
	if d.Issues != nil {
		r.Get("/repos/{owner}/{repo}/issues/{number}/labels", handleIssueLabelsList(d))
		r.Post("/repos/{owner}/{repo}/issues/{number}/labels", handleIssueLabelsAdd(d))
		r.Put("/repos/{owner}/{repo}/issues/{number}/labels", handleIssueLabelsReplace(d))
		r.Delete("/repos/{owner}/{repo}/issues/{number}/labels/{name}", handleIssueLabelRemove(d))
		r.Get("/repos/{owner}/{repo}/assignees", handleAssigneesList(d))
		r.Post("/repos/{owner}/{repo}/issues/{number}/assignees", handleIssueAssigneesAdd(d))
		r.Delete("/repos/{owner}/{repo}/issues/{number}/assignees", handleIssueAssigneesRemove(d))
		r.Get("/repos/{owner}/{repo}/issues/{number}/events", handleIssueEventsList(d))
		r.Get("/repos/{owner}/{repo}/issues/{number}/timeline", handleIssueTimeline(d))
	}
	if d.Reviews != nil {
		r.Get("/repos/{owner}/{repo}/pulls/comments", handleAllReviewCommentsList(d))
		r.Patch("/repos/{owner}/{repo}/pulls/comments/{comment_id}", handleReviewCommentEdit(d))
		r.Delete("/repos/{owner}/{repo}/pulls/{number}/reviews/{review_id}", handlePullReviewDelete(d))
	}
	if d.Checks != nil {
		r.Get("/repos/{owner}/{repo}/check-suites/{suite_id}", handleCheckSuiteGet(d))
	}
	if d.Hooks != nil {
		r.Post("/repos/{owner}/{repo}/hooks/{hook_id}/pings", requireScope(handleWebhookPing(d), "repo", "write:repo_hook"))
	}
	if d.Teams != nil {
		r.Get("/orgs/{org}/teams", handleOrgTeamsList(d))
		r.Get("/orgs/{org}/teams/{team_slug}/repos", handleOrgTeamReposList(d))
	}
}

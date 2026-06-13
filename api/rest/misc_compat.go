package rest

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/presenter/restmodel"
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

// handleOrgMembersList serves GET /orgs/{org}/members. The backing account
// itself counts as a member (it is the org's built-in admin); persisted
// memberships follow in grant order.
func handleOrgMembersList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		org, err := d.Users.ByLogin(ctx, c.Param("org"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		page, perr := parsePageFor(c, "User")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		out := []restmodel.SimpleUser{d.URLs.SimpleUser(org, d.NodeFormat)}
		orgPK, err := d.Users.PKByLogin(ctx, org.Login)
		if err != nil {
			return err
		}
		members, err := d.Teams.ListOrgMembers(ctx, orgPK)
		if err != nil {
			return err
		}
		for _, m := range members {
			if m.User.Login == org.Login {
				continue
			}
			out = append(out, d.URLs.SimpleUser(m.User, d.NodeFormat))
		}
		out = paginateSlice(&page, out)
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleOrgMemberGet serves GET /orgs/{org}/members/{username}: 204 when the
// user holds a membership (or is the org's backing account), 404 otherwise.
func handleOrgMemberGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		orgPK, err := d.Users.PKByLogin(ctx, c.Param("org"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		userPK, err := d.Users.PKByLogin(ctx, c.Param("username"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		if userPK != orgPK {
			if _, err := d.Teams.GetOrgMembership(ctx, orgPK, userPK); errors.Is(err, domain.ErrNotFound) {
				writeError(c.Writer(), errNotFound())
				return nil
			} else if err != nil {
				return err
			}
		}
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

// handleUserOrgs serves GET /user/orgs, listing the organizations the
// authenticated user belongs to.
func handleUserOrgs(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		page, perr := parsePageFor(c, "Organization")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		orgs, err := d.Teams.ListUserOrgs(ctx, actor.UserID)
		if err != nil {
			return err
		}
		out := make([]restmodel.OrganizationSimple, 0, len(orgs))
		for _, m := range orgs {
			out = append(out, d.URLs.OrgSimple(m.User, d.NodeFormat))
		}
		out = paginateSlice(&page, out)
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleUserOrgMemberships serves GET /user/memberships/orgs, listing the
// authenticated user's org memberships.
func handleUserOrgMemberships(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		page, perr := parsePageFor(c, "Organization")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		user, err := d.Users.Viewer(ctx, actor.UserID)
		if err != nil {
			return err
		}
		orgs, err := d.Teams.ListUserOrgs(ctx, actor.UserID)
		if err != nil {
			return err
		}
		out := make([]restmodel.OrgMembership, 0, len(orgs))
		for _, m := range orgs {
			out = append(out, d.URLs.OrgMembership(m.User, user, m.Role, d.NodeFormat))
		}
		out = paginateSlice(&page, out)
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleUserOrgMembershipGet serves GET /user/memberships/orgs/{org}, returning
// the authenticated user's membership in one org (404 when not a member).
func handleUserOrgMembershipGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		org, err := d.Users.ByLogin(ctx, c.Param("org"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		orgPK, err := d.Users.PKByLogin(ctx, org.Login)
		if err != nil {
			return err
		}
		role, err := d.Teams.GetOrgMembership(ctx, orgPK, actor.UserID)
		if errors.Is(err, domain.ErrNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		user, err := d.Users.Viewer(ctx, actor.UserID)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.OrgMembership(org, user, role, d.NodeFormat))
		return nil
	}
}

// handleUsersList serves GET /users, the id-cursor listing of all accounts.
func handleUsersList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		since := int64(0)
		if s := c.Query("since"); s != "" {
			v, ok := parseInt64(s)
			if !ok || v < 0 {
				writeError(c.Writer(), errValidation(FieldError{
					Resource: "User", Field: "since", Code: "invalid",
				}))
				return nil
			}
			since = v
		}
		perPage := 30
		if p := c.Query("per_page"); p != "" {
			v, ok := parseInt64(p)
			if !ok || v < 1 || v > 100 {
				writeError(c.Writer(), errValidation(FieldError{
					Resource: "User", Field: "per_page", Code: "invalid",
				}))
				return nil
			}
			perPage = int(v)
		}
		// Fetch one extra to decide whether a next page exists.
		users, err := d.Users.ListUsers(ctx, since, perPage+1)
		if err != nil {
			return err
		}
		hasNext := len(users) > perPage
		if hasNext {
			users = users[:perPage]
		}
		out := make([]restmodel.SimpleUser, 0, len(users))
		for _, u := range users {
			out = append(out, d.URLs.SimpleUser(u, d.NodeFormat))
		}
		if hasNext && len(out) > 0 {
			last := out[len(out)-1].ID
			writeUsersSinceLink(c.Writer(), c.Request(), d.URLs, last)
		}
		writeJSON(c.Writer(), http.StatusOK, out)
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
// It returns the issue's recorded action events (closed, reopened, locked, ...)
// in chronological order, paginated with a Link header.
func handleIssueEventsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		page, perr := parsePageFor(c, "Issue")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		events, err := d.Issues.ListIssueEvents(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), number)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		out := make([]restmodel.IssueEvent, 0, len(events))
		for _, e := range events {
			out = append(out, d.URLs.IssueEvent(c.Param("owner"), c.Param("repo"), e, d.NodeFormat))
		}
		paged := paginateSlice(&page, out)
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		writeJSON(c.Writer(), http.StatusOK, paged)
		return nil
	}
}

// timelineComment is a comment as it appears on the timeline: the comment object
// with the discriminating event:"commented" field GitHub adds.
type timelineComment struct {
	restmodel.IssueComment
	Event string `json:"event"`
}

// handleIssueTimeline serves GET /repos/{owner}/{repo}/issues/{number}/timeline.
// The timeline merges the issue's comments (as commented entries) with its
// action events, ordered by time, then paginates the merged list with a Link
// header. It is the union of the comments and events surfaces in one feed.
func handleIssueTimeline(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		page, perr := parsePageFor(c, "Issue")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		owner, repoName := c.Param("owner"), c.Param("repo")
		actor := auth.ActorFrom(c.Request().Context())
		ctx := c.Request().Context()

		events, err := d.Issues.ListIssueEvents(ctx, actor.UserID, owner, repoName, number)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		comments, err := allIssueComments(ctx, d, actor.UserID, owner, repoName, number)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}

		// Merge into time-ordered entries; a comment and an event sharing a
		// timestamp keep comment-before-event so the order is deterministic.
		type entry struct {
			when  time.Time
			value any
		}
		entries := make([]entry, 0, len(comments)+len(events))
		for _, cm := range comments {
			entries = append(entries, entry{when: cm.CreatedAt, value: timelineComment{
				IssueComment: d.URLs.IssueComment(owner, repoName, cm, d.NodeFormat),
				Event:        "commented",
			}})
		}
		for _, e := range events {
			entries = append(entries, entry{when: e.CreatedAt, value: d.URLs.IssueEvent(owner, repoName, e, d.NodeFormat)})
		}
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].when.Before(entries[j].when) })

		paged := paginateSlice(&page, entries)
		out := make([]any, 0, len(paged))
		for _, e := range paged {
			out = append(out, e.value)
		}
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// allIssueComments walks every page of an issue's comments so the timeline can
// merge the full set. The page size matches the API ceiling; the loop stops on
// the first short page.
func allIssueComments(ctx context.Context, d Deps, viewerPK int64, owner, repoName string, number int64) ([]*domain.Comment, error) {
	const perPage = 100
	var all []*domain.Comment
	for pageNo := int64(1); ; pageNo++ {
		batch, err := d.Issues.ListComments(ctx, viewerPK, owner, repoName, number, pageNo, perPage)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < perPage {
			return all, nil
		}
	}
}

// handleSearchUsers serves GET /search/users. It runs a real account search
// over the users table: the q free-text matches login, name, and public
// email, the type: qualifier narrows to user or org accounts, and sort/order
// pick the ordering. A missing q is the required-field 422, matching GitHub.
func handleSearchUsers(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		raw, ok := searchTerm(c)
		if !ok {
			writeError(c.Writer(), errValidation(missingQ()))
			return nil
		}
		page, perr := parsePageFor(c, "Search")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		users, total, err := d.Search.SearchUsers(c.Request().Context(), raw, c.Query("sort"), c.Query("order"), page.Page, page.PerPage)
		if err != nil {
			return err
		}
		body := d.URLs.SearchUsers(users, total, false, d.NodeFormat)
		page.Total = total
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		conditionalJSON(c.Writer(), c.Request(), http.StatusOK, body)
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

// handleOrgTeamsList serves GET /orgs/{org}/teams.
func handleOrgTeamsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		org, err := d.Users.ByLogin(ctx, c.Param("org"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		page, perr := parsePageFor(c, "Team")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		orgPK, err := d.Users.PKByLogin(ctx, org.Login)
		if err != nil {
			return err
		}
		teams, err := d.Teams.ListTeams(ctx, orgPK)
		if err != nil {
			return err
		}
		out := make([]any, 0, len(teams))
		for _, t := range teams {
			j, err := teamJSON(ctx, d, t, org.Login)
			if err != nil {
				return err
			}
			out = append(out, j)
		}
		out = paginateSlice(&page, out)
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		writeJSON(c.Writer(), http.StatusOK, out)
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

// handleInstallationRepos serves GET /installation/repositories, the autodiscovery
// endpoint Renovate and Dependabot poll to learn what an installation token can
// reach. The caller is an installation token (ghs_); the listing is the repos
// the installation is scoped to ("selected") or every repo its account owns
// ("all").
func handleInstallationRepos(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if actor.Kind != auth.KindInstallation {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		inst, err := d.Auth.InstallationByPK(ctx, actor.InstallationID)
		if err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		acct := d.account(ctx, inst.AccountPK)
		if acct == nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}

		var repos []*domain.Repo
		if inst.RepositorySelection == "selected" {
			pks, err := d.Auth.InstallationRepoPKs(ctx, inst.PK)
			if err != nil {
				return err
			}
			for _, pk := range pks {
				r, err := d.Repos.GetRepoByPK(ctx, inst.AccountPK, pk)
				if err == nil {
					repos = append(repos, r)
				}
			}
		} else {
			repos, err = d.Repos.ListRepos(ctx, inst.AccountPK, inst.AccountPK)
			if err != nil {
				return err
			}
		}

		items := make([]any, 0, len(repos))
		for _, r := range repos {
			items = append(items, d.URLs.Repository(r, d.NodeFormat, nil))
		}
		writeJSON(c.Writer(), http.StatusOK, map[string]any{
			"total_count":           len(items),
			"repository_selection":  inst.RepositorySelection,
			"repositories":          items,
		})
		return nil
	}
}

// handleRepoInstallation serves GET /repos/{owner}/{repo}/installation, the
// lookup Terraform's app_auth and octokit run to find the installation that can
// reach a repository. The caller is an app JWT; the answer is the app's
// installation on the repository's owning account.
func handleRepoInstallation(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if actor.Kind != auth.KindAppJWT {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		ownerPK, err := d.Users.PKByLogin(ctx, c.Param("owner"))
		if err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		inst, err := d.Auth.InstallationByAppAndAccount(ctx, actor.AppID, ownerPK)
		if err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		app, err := d.Auth.AppByPK(ctx, actor.AppID)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, installationToJSON(inst, app, d.account(ctx, inst.AccountPK), d))
		return nil
	}
}

// mountMiscCompat registers all the "compat gap" endpoints that don't belong
// to a primary sub-system file.
func mountMiscCompat(r *mizu.Router, d Deps) {
	if d.Users != nil {
		r.Get("/user/emails", handleUserEmails(d))
		r.Get("/user/orgs", handleUserOrgs(d))
		r.Get("/user/memberships/orgs", handleUserOrgMemberships(d))
		r.Get("/user/memberships/orgs/{org}", handleUserOrgMembershipGet(d))
		r.Get("/users", handleUsersList(d))
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
	}
	if d.Issues != nil {
		r.Get("/repos/{owner}/{repo}/issues/{number}/labels", handleIssueLabelsList(d))
		r.Post("/repos/{owner}/{repo}/issues/{number}/labels", handleIssueLabelsAdd(d))
		r.Put("/repos/{owner}/{repo}/issues/{number}/labels", handleIssueLabelsReplace(d))
		r.Delete("/repos/{owner}/{repo}/issues/{number}/labels/{name}", handleIssueLabelRemove(d))
		r.Get("/repos/{owner}/{repo}/assignees", handleAssigneesList(d))
		r.Get("/repos/{owner}/{repo}/assignees/{username}", handleAssigneeCheck(d))
		r.Post("/repos/{owner}/{repo}/issues/{number}/assignees", handleIssueAssigneesAdd(d))
		r.Delete("/repos/{owner}/{repo}/issues/{number}/assignees", handleIssueAssigneesRemove(d))
		r.Get("/repos/{owner}/{repo}/issues/{number}/events", handleIssueEventsList(d))
		r.Get("/repos/{owner}/{repo}/issues/{number}/timeline", handleIssueTimeline(d))
		r.Put("/repos/{owner}/{repo}/issues/{number}/lock", handleIssueLock(d))
		r.Delete("/repos/{owner}/{repo}/issues/{number}/lock", handleIssueUnlock(d))
	}
	if d.Reviews != nil {
		r.Get("/repos/{owner}/{repo}/pulls/comments", handleAllReviewCommentsList(d))
		r.Patch("/repos/{owner}/{repo}/pulls/comments/{comment_id}", handleReviewCommentEdit(d))
		r.Delete("/repos/{owner}/{repo}/pulls/{number}/reviews/{review_id}", handlePullReviewDelete(d))
	}
	if d.Hooks != nil {
		r.Post("/repos/{owner}/{repo}/hooks/{hook_id}/pings", requireScope(handleWebhookPing(d), "repo", "write:repo_hook"))
	}
	if d.Teams != nil {
		r.Get("/orgs/{org}/teams", handleOrgTeamsList(d))
		r.Get("/orgs/{org}/teams/{team_slug}/repos", handleOrgTeamReposList(d))
	}
}

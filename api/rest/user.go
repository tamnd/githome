package rest

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/presenter"
)

// handleUserGet serves GET /user, the authenticated viewer's own profile. An
// anonymous caller gets 401 "Requires authentication"; a user whose backing
// account has vanished gets 401 "Bad credentials". The body is the full User
// with the authenticated-user private counters present.
func handleUserGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		u, err := d.Users.Viewer(ctx, actor.UserID)
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errBadCredentials())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.User(u, d.NodeFormat, true))
		return nil
	}
}

// handlePublicUserGet serves GET /users/{username}, returning the public
// profile for any user. A missing user is 404.
func handlePublicUserGet(d Deps) mizu.Handler {
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
		actor := auth.ActorFrom(ctx)
		// User.ID is the public db_id; actor.UserID is the internal PK; IDs differ.
		// We only need to know if the viewer is the same user to show private fields.
		authenticated := actor.IsUser() && actor.UserLogin == u.Login
		writeJSON(c.Writer(), http.StatusOK, d.URLs.User(u, d.NodeFormat, authenticated))
		return nil
	}
}

// handlePublicUserRepos serves GET /users/{username}/repos, listing repos
// visible to the caller for the named user.
func handlePublicUserRepos(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		page, perr := parsePageFor(c, "Repository")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		repos, err := d.Repos.ListReposByLogin(ctx, actor.UserID, c.Param("username"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		repos = paginateSlice(&page, repos)
		out := make([]any, 0, len(repos))
		for _, r := range repos {
			out = append(out, d.URLs.Repository(r, d.NodeFormat, repoPermissions(actor, r)))
		}
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleUserReposList serves GET /user/repos, listing all repositories the
// authenticated viewer can access (their own repos). Requires authentication.
func handleUserReposList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		page, perr := parsePageFor(c, "Repository")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		repos, err := d.Repos.ListRepos(ctx, actor.UserID, actor.UserID)
		if err != nil {
			return err
		}
		repos = paginateSlice(&page, repos)
		out := make([]any, 0, len(repos))
		for _, r := range repos {
			out = append(out, d.URLs.Repository(r, d.NodeFormat, presenter.OwnerPermissions()))
		}
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// repoCreateBody is the JSON body for POST /user/repos and POST /orgs/{org}/repos.
type repoCreateBody struct {
	Name          string  `json:"name"`
	Description   *string `json:"description"`
	Homepage      *string `json:"homepage"`
	Private       bool    `json:"private"`
	AutoInit      bool    `json:"auto_init"`
	DefaultBranch string  `json:"default_branch"`
}

// handleUserRepoCreate serves POST /user/repos, creating a new repository
// under the authenticated user. Requires authentication.
func handleUserRepoCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		var body repoCreateBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if strings.TrimSpace(body.Name) == "" {
			writeError(c.Writer(), errUnprocessable("Repository name is required"))
			return nil
		}
		u, err := d.Users.Viewer(ctx, actor.UserID)
		if err != nil {
			return err
		}
		repo, err := d.Repos.CreateRepo(ctx, actor.UserID, u.Login, domain.RepoInput{
			Name:          body.Name,
			Description:   body.Description,
			Homepage:      body.Homepage,
			Private:       body.Private,
			AutoInit:      body.AutoInit,
			DefaultBranch: body.DefaultBranch,
		})
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.Repository(repo, d.NodeFormat, presenter.OwnerPermissions()))
		return nil
	}
}

// handleOrgGet serves GET /orgs/{org}. In Githome organizations and users share
// the same users table; an org is just a user with type="Organization".
func handleOrgGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		orgLogin := c.Param("org")
		u, err := d.Users.ByLogin(ctx, orgLogin)
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		actor := auth.ActorFrom(ctx)
		authenticated := actor.IsUser() && actor.UserLogin == u.Login
		writeJSON(c.Writer(), http.StatusOK, d.URLs.User(u, d.NodeFormat, authenticated))
		return nil
	}
}

// handleOrgReposList serves GET /orgs/{org}/repos, listing repos visible to
// the caller under the named organization.
func handleOrgReposList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		page, perr := parsePageFor(c, "Repository")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		repos, err := d.Repos.ListReposByLogin(ctx, actor.UserID, c.Param("org"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		repos = paginateSlice(&page, repos)
		out := make([]any, 0, len(repos))
		for _, r := range repos {
			out = append(out, d.URLs.Repository(r, d.NodeFormat, repoPermissions(actor, r)))
		}
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleOrgRepoCreate serves POST /orgs/{org}/repos, creating a new repository
// under the named organization. Requires authentication and org ownership.
func handleOrgRepoCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		var body repoCreateBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if strings.TrimSpace(body.Name) == "" {
			writeError(c.Writer(), errUnprocessable("Repository name is required"))
			return nil
		}
		orgLogin := c.Param("org")
		repo, err := d.Repos.CreateRepo(ctx, actor.UserID, orgLogin, domain.RepoInput{
			Name:          body.Name,
			Description:   body.Description,
			Homepage:      body.Homepage,
			Private:       body.Private,
			AutoInit:      body.AutoInit,
			DefaultBranch: body.DefaultBranch,
		})
		if errors.Is(err, domain.ErrForbidden) {
			writeError(c.Writer(), errForbidden("Must be a member of the org"))
			return nil
		}
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.Repository(repo, d.NodeFormat, presenter.OwnerPermissions()))
		return nil
	}
}

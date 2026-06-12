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
// visible to the caller for the named user. The type selector picks owned
// repos (the default), repos the user collaborates on (member), or both
// (all); sort and direction order the result the way GitHub does.
func handlePublicUserRepos(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		page, perr := parsePageFor(c, "Repository")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		opts, perr := parseRepoListOpts(c, "all", "owner", "member")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		login := c.Param("username")
		var repos []*domain.Repo
		if opts.Type == "" || opts.Type == "all" || opts.Type == "owner" {
			owned, err := d.Repos.ListReposByLogin(ctx, actor.UserID, login)
			if errors.Is(err, domain.ErrUserNotFound) {
				writeError(c.Writer(), errNotFound())
				return nil
			}
			if err != nil {
				return err
			}
			repos = owned
		}
		if opts.Type == "all" || opts.Type == "member" {
			pk, err := d.Users.PKByLogin(ctx, login)
			if errors.Is(err, domain.ErrUserNotFound) {
				writeError(c.Writer(), errNotFound())
				return nil
			}
			if err != nil {
				return err
			}
			member, err := d.Repos.ListCollaboratorRepos(ctx, actor.UserID, pk)
			if err != nil {
				return err
			}
			repos = mergeRepos(repos, member)
		}
		sortRepos(repos, opts.Sort, opts.Direction)
		repos = paginateSlice(&page, repos)
		out := make([]any, 0, len(repos))
		for _, r := range repos {
			perm, err := repoPermissions(ctx, d, actor, r)
			if err != nil {
				return err
			}
			out = append(out, d.URLs.Repository(r, d.NodeFormat, perm))
		}
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleUserReposList serves GET /user/repos, listing the repositories the
// authenticated viewer can access: owned repos, collaborator grants, and
// team-granted repos, selected by affiliation or the legacy type parameter
// (sending type alongside visibility or affiliation is GitHub's 422), then
// narrowed by visibility and the since/before window. Requires
// authentication.
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
		opts, perr := parseRepoListOpts(c, "all", "owner", "public", "private", "member")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		if opts.Type != "" && (opts.Visibility != "" || opts.Affiliation != nil) {
			writeError(c.Writer(), errUnprocessable("If you specify visibility or affiliation, you cannot specify type."))
			return nil
		}

		// The legacy type parameter is shorthand for an affiliation set plus a
		// visibility: owner and member narrow the affiliations, public and
		// private narrow the visibility over every affiliation.
		affiliation := opts.Affiliation
		visibility := opts.Visibility
		switch opts.Type {
		case "owner":
			affiliation = map[string]bool{"owner": true}
		case "member":
			affiliation = map[string]bool{"collaborator": true}
		case "public", "private":
			visibility = opts.Type
		}
		if affiliation == nil {
			affiliation = map[string]bool{"owner": true, "collaborator": true, "organization_member": true}
		}

		var repos []*domain.Repo
		if affiliation["owner"] {
			owned, err := d.Repos.ListRepos(ctx, actor.UserID, actor.UserID)
			if err != nil {
				return err
			}
			repos = owned
		}
		if affiliation["collaborator"] {
			member, err := d.Repos.ListCollaboratorRepos(ctx, actor.UserID, actor.UserID)
			if err != nil {
				return err
			}
			repos = mergeRepos(repos, member)
		}
		if affiliation["organization_member"] {
			team, err := d.Repos.ListTeamRepos(ctx, actor.UserID, actor.UserID)
			if err != nil {
				return err
			}
			repos = mergeRepos(repos, team)
		}

		repos = filterRepoVisibility(repos, visibility)
		repos = filterRepoWindow(repos, opts.Since, opts.Before)
		sortRepos(repos, opts.Sort, opts.Direction)
		repos = paginateSlice(&page, repos)
		out := make([]any, 0, len(repos))
		for _, r := range repos {
			perm, err := repoPermissions(ctx, d, actor, r)
			if err != nil {
				return err
			}
			out = append(out, d.URLs.Repository(r, d.NodeFormat, perm))
		}
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// repoCreateBody is the JSON body for POST /user/repos and POST /orgs/{org}/repos.
// TeamID and HasDownloads are accepted for wire compatibility: githome has no
// team grants at creation time and downloads are always on, so both decode
// and are ignored rather than rejected.
type repoCreateBody struct {
	Name          string  `json:"name"`
	Description   *string `json:"description"`
	Homepage      *string `json:"homepage"`
	Private       bool    `json:"private"`
	AutoInit      bool    `json:"auto_init"`
	DefaultBranch string  `json:"default_branch"`

	HasIssues    *bool `json:"has_issues"`
	HasProjects  *bool `json:"has_projects"`
	HasWiki      *bool `json:"has_wiki"`
	HasDownloads *bool `json:"has_downloads"`
	IsTemplate   bool  `json:"is_template"`
	TeamID       int64 `json:"team_id"`

	AllowSquashMerge    *bool `json:"allow_squash_merge"`
	AllowMergeCommit    *bool `json:"allow_merge_commit"`
	AllowRebaseMerge    *bool `json:"allow_rebase_merge"`
	AllowAutoMerge      *bool `json:"allow_auto_merge"`
	DeleteBranchOnMerge *bool `json:"delete_branch_on_merge"`

	GitignoreTemplate string `json:"gitignore_template"`
	LicenseTemplate   string `json:"license_template"`
}

// repoCreateInput validates the create body and maps it onto the domain
// input. It returns the structured 422 GitHub sends for a missing name or an
// unknown gitignore/license template.
func repoCreateInput(body repoCreateBody) (domain.RepoInput, *apiError) {
	if strings.TrimSpace(body.Name) == "" {
		return domain.RepoInput{}, &apiError{
			Status:  http.StatusUnprocessableEntity,
			Message: "Repository creation failed.",
			Errors: []FieldError{{
				Resource: "Repository",
				Field:    "name",
				Code:     "custom",
				Message:  "name is too short (minimum is 1 character)",
			}},
			DocURL: docRoot + "/repos/repos#create-a-repository-for-the-authenticated-user",
		}
	}
	if body.GitignoreTemplate != "" {
		if _, ok := domain.GitignoreTemplate(body.GitignoreTemplate); !ok {
			return domain.RepoInput{}, errValidation(FieldError{
				Resource: "Repository", Field: "gitignore_template", Code: "invalid",
			})
		}
	}
	if body.LicenseTemplate != "" {
		if _, ok := domain.LicenseTemplate(body.LicenseTemplate); !ok {
			return domain.RepoInput{}, errValidation(FieldError{
				Resource: "Repository", Field: "license_template", Code: "invalid",
			})
		}
	}
	return domain.RepoInput{
		Name:          body.Name,
		Description:   body.Description,
		Homepage:      body.Homepage,
		Private:       body.Private,
		AutoInit:      body.AutoInit,
		DefaultBranch: body.DefaultBranch,

		HasIssues:   body.HasIssues,
		HasProjects: body.HasProjects,
		HasWiki:     body.HasWiki,
		IsTemplate:  body.IsTemplate,

		AllowSquashMerge:    body.AllowSquashMerge,
		AllowMergeCommit:    body.AllowMergeCommit,
		AllowRebaseMerge:    body.AllowRebaseMerge,
		AllowAutoMerge:      body.AllowAutoMerge,
		DeleteBranchOnMerge: body.DeleteBranchOnMerge,

		GitignoreTemplate: body.GitignoreTemplate,
		LicenseTemplate:   body.LicenseTemplate,
	}, nil
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
		inp, verr := repoCreateInput(body)
		if verr != nil {
			writeError(c.Writer(), verr)
			return nil
		}
		u, err := d.Users.Viewer(ctx, actor.UserID)
		if err != nil {
			return err
		}
		repo, err := d.Repos.CreateRepo(ctx, actor.UserID, u.Login, inp)
		if err != nil {
			return err
		}
		det, err := repoDetail(d, c, repo)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.RepositoryFull(repo, d.NodeFormat, presenter.OwnerPermissions(), det))
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
// the caller under the named organization. The type selector narrows by
// visibility (public, private), fork status (forks, sources), or the caller's
// collaborator grants (member); sort and direction order the result.
func handleOrgReposList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		page, perr := parsePageFor(c, "Repository")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		opts, perr := parseRepoListOpts(c, "all", "public", "private", "forks", "sources", "member")
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
		switch opts.Type {
		case "public", "private":
			repos = filterRepoVisibility(repos, opts.Type)
		case "forks":
			repos = filterRepoFork(repos, true)
		case "sources":
			repos = filterRepoFork(repos, false)
		case "member":
			member, err := d.Repos.ListCollaboratorRepos(ctx, actor.UserID, actor.UserID)
			if err != nil {
				return err
			}
			granted := make(map[int64]bool, len(member))
			for _, m := range member {
				granted[m.PK] = true
			}
			kept := repos[:0]
			for _, r := range repos {
				if granted[r.PK] {
					kept = append(kept, r)
				}
			}
			repos = kept
		}
		sortRepos(repos, opts.Sort, opts.Direction)
		repos = paginateSlice(&page, repos)
		out := make([]any, 0, len(repos))
		for _, r := range repos {
			perm, err := repoPermissions(ctx, d, actor, r)
			if err != nil {
				return err
			}
			out = append(out, d.URLs.Repository(r, d.NodeFormat, perm))
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
		inp, verr := repoCreateInput(body)
		if verr != nil {
			writeError(c.Writer(), verr)
			return nil
		}
		orgLogin := c.Param("org")
		repo, err := d.Repos.CreateRepo(ctx, actor.UserID, orgLogin, inp)
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
		det, err := repoDetail(d, c, repo)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.RepositoryFull(repo, d.NodeFormat, presenter.OwnerPermissions(), det))
		return nil
	}
}

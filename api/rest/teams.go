package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/restmodel"
	"github.com/tamnd/githome/store"
)

// mountTeams registers teams, collaborators, and topics endpoints on r.
func mountTeams(r *mizu.Router, d Deps) {
	// Topics.
	r.Get("/repos/{owner}/{repo}/topics", handleTopicsGet(d))
	r.Put("/repos/{owner}/{repo}/topics", handleTopicsPut(d))

	// Collaborators.
	r.Get("/repos/{owner}/{repo}/collaborators/{username}", handleCollaboratorCheck(d))
	r.Put("/repos/{owner}/{repo}/collaborators/{username}", handleCollaboratorAdd(d))
	r.Delete("/repos/{owner}/{repo}/collaborators/{username}", handleCollaboratorDelete(d))
	r.Get("/repos/{owner}/{repo}/collaborators/{username}/permission", handleCollaboratorPermission(d))

	// Teams.
	r.Post("/orgs/{org}/teams", handleTeamCreate(d))
	r.Get("/orgs/{org}/teams/{team_slug}", handleTeamGet(d))
	r.Patch("/orgs/{org}/teams/{team_slug}", handleTeamUpdate(d))
	r.Delete("/orgs/{org}/teams/{team_slug}", handleTeamDelete(d))
	r.Put("/orgs/{org}/teams/{team_slug}/memberships/{username}", handleTeamMemberAdd(d))
	r.Get("/orgs/{org}/teams/{team_slug}/memberships/{username}", handleTeamMemberGet(d))
	r.Delete("/orgs/{org}/teams/{team_slug}/memberships/{username}", handleTeamMemberDelete(d))
	r.Put("/orgs/{org}/teams/{team_slug}/repos/{owner}/{repo}", handleTeamRepoAdd(d))
	r.Get("/orgs/{org}/teams/{team_slug}/repos/{owner}/{repo}", handleTeamRepoGet(d))
	r.Delete("/orgs/{org}/teams/{team_slug}/repos/{owner}/{repo}", handleTeamRepoDelete(d))

	// Org membership (github_organization_member / github_membership Terraform resources).
	r.Put("/orgs/{org}/memberships/{username}", handleOrgMembershipPut(d))
	r.Get("/orgs/{org}/memberships/{username}", handleOrgMembershipGet(d))
	r.Delete("/orgs/{org}/members/{username}", handleOrgMemberDelete(d))
}

// handleTopicsGet serves GET /repos/{owner}/{repo}/topics.
func handleTopicsGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		var names []string
		if repo.Topics != "" && repo.Topics != "[]" {
			_ = json.Unmarshal([]byte(repo.Topics), &names)
		}
		if names == nil {
			names = []string{}
		}
		writeJSON(c.Writer(), http.StatusOK, map[string]any{"names": names})
		return nil
	}
}

// handleTopicsPut serves PUT /repos/{owner}/{repo}/topics.
func handleTopicsPut(d Deps) mizu.Handler {
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
		if actor.UserID != repo.OwnerPK {
			writeError(c.Writer(), errForbidden("Must have admin rights to Repository."))
			return nil
		}
		var body struct {
			Names []string `json:"names"`
		}
		if !decodeJSON(c, &body) {
			return nil
		}
		if err := d.Teams.SetTopics(ctx, repo.PK, body.Names); err != nil {
			return err
		}
		names := body.Names
		if names == nil {
			names = []string{}
		}
		writeJSON(c.Writer(), http.StatusOK, map[string]any{"names": names})
		return nil
	}
}

// roleName maps a stored collaborator permission to GitHub's role_name
// vocabulary: the legacy pull and push grants read back as read and write,
// the granular roles read back as themselves.
func roleName(permission string) string {
	switch permission {
	case "pull":
		return "read"
	case "push":
		return "write"
	default:
		return permission
	}
}

// collaboratorUser is a SimpleUser carrying the collaborator listing's extra
// fields: the granular role and the expanded permission booleans.
type collaboratorUser struct {
	restmodel.SimpleUser
	RoleName    string                     `json:"role_name"`
	Permissions *restmodel.RepoPermissions `json:"permissions"`
}

// collaboratorObject renders one collaborator entry from a user and a stored
// permission grant. A role with no expansion (none) still carries the
// all-false booleans, never a null block.
func collaboratorObject(d Deps, u *domain.User, permission string) collaboratorUser {
	perms := permissionBlock(permission)
	if perms == nil {
		perms = &restmodel.RepoPermissions{}
	}
	return collaboratorUser{
		SimpleUser:  d.URLs.SimpleUser(u, d.NodeFormat),
		RoleName:    roleName(permission),
		Permissions: perms,
	}
}

// handleCollaboratorCheck serves GET /repos/{owner}/{repo}/collaborators/{username}:
// 204 when the user is the owner or holds a grant, 404 otherwise.
func handleCollaboratorCheck(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		targetPK, err := d.Users.PKByLogin(ctx, c.Param("username"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		if targetPK != repo.OwnerPK {
			if _, err := d.Teams.GetCollaboratorPermission(ctx, repo.PK, targetPK); errors.Is(err, domain.ErrNotFound) {
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

// handleCollaboratorAdd serves PUT /repos/{owner}/{repo}/collaborators/{username}.
// A new grant answers 201 with the invitation object GitHub sends, even though
// the grant here is immediate rather than pending; updating an existing
// collaborator (or naming the owner) is the 204 GitHub uses for someone who is
// already a collaborator.
func handleCollaboratorAdd(d Deps) mizu.Handler {
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
		if actor.UserID != repo.OwnerPK {
			writeError(c.Writer(), errForbidden("Must have admin rights to Repository."))
			return nil
		}
		target, err := d.Users.ByLogin(ctx, c.Param("username"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		targetPK, err := d.Users.PKByLogin(ctx, c.Param("username"))
		if err != nil {
			return err
		}
		var body struct {
			Permission string `json:"permission"`
		}
		_ = decodeJSONOpt(c, &body)
		if body.Permission == "" {
			body.Permission = "push"
		}
		switch body.Permission {
		case "pull", "push", "admin", "maintain", "triage":
		default:
			writeError(c.Writer(), errValidation(FieldError{
				Resource: "Repository", Field: "permission", Code: "invalid",
			}))
			return nil
		}
		if targetPK == repo.OwnerPK {
			// The owner already holds every permission; GitHub answers the
			// already-a-collaborator 204 without writing anything.
			c.Writer().WriteHeader(http.StatusNoContent)
			return nil
		}
		id, created, err := d.Teams.AddCollaborator(ctx, repo.PK, targetPK, body.Permission)
		if err != nil {
			return err
		}
		if !created {
			c.Writer().WriteHeader(http.StatusNoContent)
			return nil
		}
		inviter, err := d.Users.Viewer(ctx, actor.UserID)
		if err != nil {
			return err
		}
		perm, err := repoPermissions(ctx, d, actor, repo)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, map[string]any{
			"id":          id,
			"node_id":     "",
			"repository":  d.URLs.Repository(repo, d.NodeFormat, perm),
			"invitee":     d.URLs.SimpleUser(target, d.NodeFormat),
			"inviter":     d.URLs.SimpleUser(inviter, d.NodeFormat),
			"permissions": roleName(body.Permission),
			"created_at":  time.Now().UTC().Format(time.RFC3339),
			"url":         d.URLs.API("repos", repo.Owner.Login, repo.Name, "invitations", strconv.FormatInt(id, 10)),
			"html_url":    d.URLs.HTML(repo.Owner.Login, repo.Name, "invitations"),
			"expired":     false,
		})
		return nil
	}
}

// handleCollaboratorDelete serves DELETE /repos/{owner}/{repo}/collaborators/{username}.
func handleCollaboratorDelete(d Deps) mizu.Handler {
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
		if actor.UserID != repo.OwnerPK {
			writeError(c.Writer(), errForbidden("Must have admin rights to Repository."))
			return nil
		}
		targetPK, err := d.Users.PKByLogin(ctx, c.Param("username"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		if err := d.Teams.RemoveCollaborator(ctx, repo.PK, targetPK); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// coarsePermission folds a granular role into the four-value permission field
// the permission endpoint's top level carries: admin stays admin, maintain
// and push fold to write, triage and pull fold to read.
func coarsePermission(role string) string {
	switch role {
	case "admin":
		return "admin"
	case "maintain", "push":
		return "write"
	case "triage", "pull":
		return "read"
	default:
		return "none"
	}
}

// handleCollaboratorPermission serves
// GET /repos/{owner}/{repo}/collaborators/{username}/permission. The top-level
// permission field is GitHub's coarse admin/write/read/none vocabulary;
// role_name and the nested user.permissions carry the granular role. The
// owner reads as admin without a grant; an existing user with no grant is
// none, never a 404, matching GitHub.
func handleCollaboratorPermission(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		target, err := d.Users.ByLogin(ctx, c.Param("username"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		targetPK, err := d.Users.PKByLogin(ctx, c.Param("username"))
		if err != nil {
			return err
		}
		role := "none"
		if targetPK == repo.OwnerPK {
			role = "admin"
		} else if granted, err := d.Teams.GetCollaboratorPermission(ctx, repo.PK, targetPK); err == nil {
			role = granted
		} else if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, map[string]any{
			"permission": coarsePermission(role),
			"role_name":  roleName(role),
			"user":       collaboratorObject(d, target, role),
		})
		return nil
	}
}

// --- Teams ---

type teamCreateBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Privacy     string `json:"privacy"`
	Permission  string `json:"permission"`
}

func handleTeamCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		orgPK, err := d.Users.PKByLogin(ctx, c.Param("org"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		org, _ := d.Users.ByLogin(ctx, c.Param("org"))
		var body teamCreateBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if body.Name == "" {
			writeError(c.Writer(), errUnprocessable("name is required"))
			return nil
		}
		t, err := d.Teams.CreateTeam(ctx, orgPK, body.Name, body.Description, body.Privacy, body.Permission)
		if errors.Is(err, domain.ErrDuplicateKey) {
			writeError(c.Writer(), errUnprocessable("team slug already exists"))
			return nil
		}
		if err != nil {
			return err
		}
		orgLogin := c.Param("org")
		if org != nil {
			orgLogin = org.Login
		}
		writeJSON(c.Writer(), http.StatusCreated, teamToJSON(t, d, orgLogin, 0, 0))
		return nil
	}
}

func handleTeamGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		t, orgLogin, err := loadTeamByCtx(d, c, ctx)
		if t == nil {
			return err
		}
		j, err := teamJSON(ctx, d, t, orgLogin)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, j)
		return nil
	}
}

func handleTeamUpdate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		t, orgLogin, err := loadTeamByCtx(d, c, ctx)
		if t == nil {
			return err
		}
		var body struct {
			Name        *string `json:"name"`
			Description *string `json:"description"`
			Privacy     *string `json:"privacy"`
			Permission  *string `json:"permission"`
		}
		if !decodeJSON(c, &body) {
			return nil
		}
		updated, err := d.Teams.UpdateTeam(ctx, t.PK, body.Name, body.Description, body.Privacy, body.Permission)
		if err != nil {
			return err
		}
		j, err := teamJSON(ctx, d, updated, orgLogin)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, j)
		return nil
	}
}

func handleTeamDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		t, _, err := loadTeamByCtx(d, c, ctx)
		if t == nil {
			return err
		}
		if err := d.Teams.DeleteTeam(ctx, t.PK); err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

func handleTeamMemberAdd(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		t, _, err := loadTeamByCtx(d, c, ctx)
		if t == nil {
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
		var body struct {
			Role string `json:"role"`
		}
		_ = decodeJSONOpt(c, &body)
		if err := d.Teams.AddTeamMember(ctx, t.PK, userPK, body.Role); err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, map[string]any{
			"url":   d.URLs.API("orgs", c.Param("org"), "teams", t.Slug, "memberships", c.Param("username")),
			"role":  body.Role,
			"state": "active",
		})
		return nil
	}
}

func handleTeamMemberGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		t, _, err := loadTeamByCtx(d, c, ctx)
		if t == nil {
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
		role, err := d.Teams.GetTeamMembership(ctx, t.PK, userPK)
		if errors.Is(err, domain.ErrNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, map[string]any{
			"url":   d.URLs.API("orgs", c.Param("org"), "teams", t.Slug, "memberships", c.Param("username")),
			"role":  role,
			"state": "active",
		})
		return nil
	}
}

func handleTeamMemberDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		t, _, err := loadTeamByCtx(d, c, ctx)
		if t == nil {
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
		if err := d.Teams.RemoveTeamMember(ctx, t.PK, userPK); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

func handleTeamRepoAdd(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		t, _, err := loadTeamByCtx(d, c, ctx)
		if t == nil {
			return err
		}
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		var body struct {
			Permission string `json:"permission"`
		}
		_ = decodeJSONOpt(c, &body)
		if err := d.Teams.AddTeamRepo(ctx, t.PK, repo.PK, body.Permission); err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

func handleTeamRepoGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		t, _, err := loadTeamByCtx(d, c, ctx)
		if t == nil {
			return err
		}
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		perm, err := d.Teams.GetTeamRepoPermission(ctx, t.PK, repo.PK)
		if errors.Is(err, domain.ErrNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, map[string]any{"permission": perm})
		return nil
	}
}

func handleTeamRepoDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		t, _, err := loadTeamByCtx(d, c, ctx)
		if t == nil {
			return err
		}
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		if err := d.Teams.RemoveTeamRepo(ctx, t.PK, repo.PK); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// loadTeamByCtx looks up org + team by the route params.
func loadTeamByCtx(d Deps, c *mizu.Ctx, _ interface{}) (*store.TeamRow, string, error) {
	ctx := c.Request().Context()
	org, err := d.Users.ByLogin(ctx, c.Param("org"))
	if errors.Is(err, domain.ErrUserNotFound) {
		writeError(c.Writer(), errNotFound())
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	orgPK, err := d.Users.PKByLogin(ctx, c.Param("org"))
	if err != nil {
		return nil, "", err
	}
	t, err := d.Teams.GetTeamBySlug(ctx, orgPK, c.Param("team_slug"))
	if errors.Is(err, domain.ErrNotFound) {
		writeError(c.Writer(), errNotFound())
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return t, org.Login, nil
}

// teamToJSON renders a team object for the REST API.
func teamToJSON(t *store.TeamRow, d Deps, orgLogin string, members, repos int) map[string]any {
	desc := ""
	if t.Description != nil {
		desc = *t.Description
	}
	base := d.URLs.API("orgs", orgLogin, "teams", t.Slug)
	return map[string]any{
		"id":               t.DBID,
		"node_id":          nodeid.Encode(nodeid.KindTeam, t.DBID, d.NodeFormat),
		"url":              base,
		"html_url":         d.URLs.HTML("orgs", orgLogin, "teams", t.Slug),
		"members_url":      base + "/members{/member}",
		"repositories_url": base + "/repos",
		"name":             t.Name,
		"slug":             t.Slug,
		"description":      desc,
		"privacy":          t.Privacy,
		"permission":       t.Permission,
		"parent":           nil,
		"members_count":    members,
		"repos_count":      repos,
		"created_at":       t.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":       t.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// teamJSON is teamToJSON with the member and repo counts resolved.
func teamJSON(ctx context.Context, d Deps, t *store.TeamRow, orgLogin string) (map[string]any, error) {
	members, repos, err := d.Teams.TeamCounts(ctx, t.PK)
	if err != nil {
		return nil, err
	}
	return teamToJSON(t, d, orgLogin, members, repos), nil
}

// decodeJSONOpt is like decodeJSON but returns true even if the body is absent
// or empty, treating it as an empty object. Used for endpoints where the request
// body is optional (e.g. PUT with no body).
func decodeJSONOpt(c *mizu.Ctx, v any) bool {
	r := c.Request()
	if r.ContentLength == 0 {
		return true
	}
	return decodeJSON(c, v)
}

// handleOrgMembershipPut serves PUT /orgs/{org}/memberships/{username}.
// GitHub's Terraform provider uses this to add/invite an org member with a role.
func handleOrgMembershipPut(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		org := c.Param("org")
		username := c.Param("username")
		var body struct {
			Role string `json:"role"` // "member" or "admin"
		}
		body.Role = "member"
		decodeJSONOpt(c, &body)
		// Verify both the org and user exist.
		orgUser, err := d.Users.ByLogin(ctx, org)
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		user, err := d.Users.ByLogin(ctx, username)
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		orgPK, err := d.Users.PKByLogin(ctx, orgUser.Login)
		if err != nil {
			return err
		}
		userPK, err := d.Users.PKByLogin(ctx, user.Login)
		if err != nil {
			return err
		}
		role := body.Role
		if userPK == orgPK {
			role = "admin"
		} else if role, err = d.Teams.AddOrgMember(ctx, orgPK, userPK, role); err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, orgMembershipJSON(org, user, role, "active", d))
		return nil
	}
}

// handleOrgMembershipGet serves GET /orgs/{org}/memberships/{username}. The
// org's backing account reads as its built-in admin; anyone else needs a
// persisted membership or the answer is the 404 GitHub gives a non-member.
func handleOrgMembershipGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		org := c.Param("org")
		username := c.Param("username")
		orgPK, err := d.Users.PKByLogin(ctx, org)
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		user, err := d.Users.ByLogin(ctx, username)
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		userPK, err := d.Users.PKByLogin(ctx, user.Login)
		if err != nil {
			return err
		}
		role := "admin"
		if userPK != orgPK {
			role, err = d.Teams.GetOrgMembership(ctx, orgPK, userPK)
			if errors.Is(err, domain.ErrNotFound) {
				writeError(c.Writer(), errNotFound())
				return nil
			}
			if err != nil {
				return err
			}
		}
		writeJSON(c.Writer(), http.StatusOK, orgMembershipJSON(org, user, role, "active", d))
		return nil
	}
}

// handleOrgMemberDelete serves DELETE /orgs/{org}/members/{username}.
func handleOrgMemberDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
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
		if err := d.Teams.RemoveOrgMember(ctx, orgPK, userPK); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

func orgMembershipJSON(org string, user *domain.User, role, state string, d Deps) map[string]any {
	return map[string]any{
		"url":              d.URLs.API("orgs", org, "memberships", user.Login),
		"state":            state,
		"role":             role,
		"organization_url": d.URLs.API("orgs", org),
		"user":             d.URLs.SimpleUser(user, d.NodeFormat),
	}
}

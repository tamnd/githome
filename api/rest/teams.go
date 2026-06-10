package rest

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/store"
)

// mountTeams registers teams, collaborators, and topics endpoints on r.
func mountTeams(r *mizu.Router, d Deps) {
	// Topics.
	r.Get("/repos/{owner}/{repo}/topics", handleTopicsGet(d))
	r.Put("/repos/{owner}/{repo}/topics", handleTopicsPut(d))

	// Collaborators.
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
			writeError(c.Writer(), errForbidden("Must be owner to set topics"))
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

// handleCollaboratorAdd serves PUT /repos/{owner}/{repo}/collaborators/{username}.
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
			writeError(c.Writer(), errForbidden("Must be repo admin to add collaborators"))
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
		var body struct {
			Permission string `json:"permission"`
		}
		_ = decodeJSONOpt(c, &body)
		if body.Permission == "" {
			body.Permission = "push"
		}
		if err := d.Teams.AddCollaborator(ctx, repo.PK, targetPK, body.Permission); err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
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
			writeError(c.Writer(), errForbidden("Must be repo admin to remove collaborators"))
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

// handleCollaboratorPermission serves GET /repos/{owner}/{repo}/collaborators/{username}/permission.
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
		perm, err := d.Teams.GetCollaboratorPermission(ctx, repo.PK, targetPK)
		if errors.Is(err, domain.ErrNotFound) {
			if target.ID == repo.ID {
				perm = "admin"
			} else {
				writeError(c.Writer(), errNotFound())
				return nil
			}
		} else if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, map[string]any{
			"permission": perm,
			"user":       d.URLs.SimpleUser(target, d.NodeFormat),
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
		writeJSON(c.Writer(), http.StatusCreated, teamToJSON(t, d, orgLogin))
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
		writeJSON(c.Writer(), http.StatusOK, teamToJSON(t, d, orgLogin))
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
		writeJSON(c.Writer(), http.StatusOK, teamToJSON(updated, d, orgLogin))
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
func teamToJSON(t *store.TeamRow, d Deps, orgLogin string) map[string]any {
	desc := ""
	if t.Description != nil {
		desc = *t.Description
	}
	return map[string]any{
		"id":          t.DBID,
		"node_id":     "",
		"url":         d.URLs.API("orgs", orgLogin, "teams", t.Slug),
		"html_url":    d.URLs.HTML("orgs", orgLogin, "teams", t.Slug),
		"name":        t.Name,
		"slug":        t.Slug,
		"description": desc,
		"privacy":     t.Privacy,
		"permission":  t.Permission,
		"created_at":  t.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":  t.UpdatedAt.UTC().Format(time.RFC3339),
	}
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
		_ = orgUser
		writeJSON(c.Writer(), http.StatusOK, orgMembershipJSON(org, user, body.Role, "active", d))
		return nil
	}
}

// handleOrgMembershipGet serves GET /orgs/{org}/memberships/{username}.
func handleOrgMembershipGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		org := c.Param("org")
		username := c.Param("username")
		_, err := d.Users.ByLogin(ctx, org)
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
		writeJSON(c.Writer(), http.StatusOK, orgMembershipJSON(org, user, "member", "active", d))
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
		_, err := d.Users.ByLogin(ctx, c.Param("username"))
		if errors.Is(err, domain.ErrUserNotFound) {
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

func orgMembershipJSON(org string, user *domain.User, role, state string, d Deps) map[string]any {
	return map[string]any{
		"url":              d.URLs.API("orgs", org, "memberships", user.Login),
		"state":            state,
		"role":             role,
		"organization_url": d.URLs.API("orgs", org),
		"user":             d.URLs.SimpleUser(user, d.NodeFormat),
	}
}

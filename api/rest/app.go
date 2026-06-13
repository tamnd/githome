package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/store"
)

// handleAppGet serves GET /app, returning the authenticated app's metadata.
// Only a KindAppJWT actor may call this; all other callers get 401.
func handleAppGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if actor.Kind != auth.KindAppJWT {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		app, err := d.Auth.AppByPK(ctx, actor.AppID)
		if errors.Is(err, store.ErrNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, appToJSON(app, d.appOwner(ctx, app), d))
		return nil
	}
}

// handleAppInstallationsList serves GET /app/installations. Requires App JWT auth.
func handleAppInstallationsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if actor.Kind != auth.KindAppJWT {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		insts, err := d.Auth.InstallationsByApp(ctx, actor.AppID)
		if err != nil {
			return err
		}
		app, err := d.Auth.AppByPK(ctx, actor.AppID)
		if err != nil {
			return err
		}
		out := make([]any, 0, len(insts))
		for _, inst := range insts {
			out = append(out, installationToJSON(inst, app, d.account(ctx, inst.AccountPK), d))
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleInstallationAccessTokens serves POST /app/installations/{id}/access_tokens.
// A GitHub App JWT mints a ghs_ token for the named installation.
func handleInstallationAccessTokens(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if actor.Kind != auth.KindAppJWT {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		instDBID, err := strconv.ParseInt(c.Param("installation_id"), 10, 64)
		if err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		// The URL carries the installation's public id (the one the installation
		// object and its access_tokens_url expose); resolve it to the internal PK
		// the token machinery keys on.
		inst, err := d.Auth.InstallationByDBID(ctx, instDBID)
		if errors.Is(err, store.ErrNotFound) {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		if err != nil {
			return err
		}

		var body struct {
			Repositories []string          `json:"repositories"`
			Permissions  map[string]string `json:"permissions"`
		}
		if !decodeJSON(c, &body) {
			return nil
		}

		plaintext, expiresAt, err := d.Auth.CreateInstallationToken(ctx, actor, inst.PK,
			body.Repositories, body.Permissions)
		if errors.Is(err, auth.ErrBadCredentials) {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		if errors.Is(err, auth.ErrInstallationSuspended) {
			writeError(c.Writer(), errForbidden("This installation has been suspended"))
			return nil
		}
		if err != nil {
			return err
		}

		perms := body.Permissions
		if perms == nil {
			perms = map[string]string{}
		}
		resp := map[string]any{
			"token":                plaintext,
			"expires_at":           expiresAt.UTC().Format("2006-01-02T15:04:05Z"),
			"permissions":          perms,
			"repository_selection": "all",
		}
		// A request that names repositories mints a token scoped to just those,
		// and GitHub echoes the resolved repository objects back so the caller
		// (ghinstallation, Renovate) learns exactly what the token can reach.
		if len(body.Repositories) > 0 {
			resp["repository_selection"] = "selected"
			resp["repositories"] = d.scopedRepos(ctx, inst, body.Repositories)
		}
		writeJSON(c.Writer(), http.StatusCreated, resp)
		return nil
	}
}

// appOwner resolves the app's owning account for the owner object, or nil when
// it cannot be loaded (a dangling owner_pk is rendered as a null owner rather
// than failing the read).
func (d Deps) appOwner(ctx context.Context, app *store.GitHubAppRow) *domain.User {
	return d.account(ctx, app.OwnerPK)
}

// account resolves a user/org by internal pk for the account and owner objects,
// returning nil when it cannot be loaded.
func (d Deps) account(ctx context.Context, pk int64) *domain.User {
	if d.Users == nil || pk == 0 {
		return nil
	}
	u, err := d.Users.Viewer(ctx, pk)
	if err != nil {
		return nil
	}
	return u
}

// scopedRepos resolves the repository names a scoped installation token names
// into repository objects, viewed as the installation's account so its private
// repositories resolve. A name that does not resolve is skipped.
func (d Deps) scopedRepos(ctx context.Context, inst *store.InstallationRow, names []string) []any {
	out := make([]any, 0, len(names))
	if d.Repos == nil || inst == nil {
		return out
	}
	acct := d.account(ctx, inst.AccountPK)
	if acct == nil {
		return out
	}
	for _, name := range names {
		r, err := d.Repos.GetRepo(ctx, inst.AccountPK, acct.Login, name)
		if err != nil {
			continue
		}
		out = append(out, d.URLs.Repository(r, d.NodeFormat, nil))
	}
	return out
}

// appToJSON renders the app object for GET /app, carrying the node_id, owner,
// permissions, and events ghinstallation and octokit auth-app readers expect.
func appToJSON(app *store.GitHubAppRow, owner *domain.User, d Deps) map[string]any {
	created := app.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")
	out := map[string]any{
		"id":           app.DBID,
		"slug":         app.Slug,
		"node_id":      nodeid.Encode(nodeid.KindApp, app.DBID, d.NodeFormat),
		"name":         app.Name,
		"client_id":    app.ClientID,
		"owner":        nil,
		"description":  nil,
		"external_url": "",
		"permissions":  jsonObject(app.Permissions),
		"events":       jsonArray(app.Events),
		"created_at":   created,
		"updated_at":   created,
		"html_url":     d.URLs.HTML("github-apps", app.Slug),
	}
	if owner != nil {
		out["owner"] = d.URLs.SimpleUser(owner, d.NodeFormat)
	}
	return out
}

// installationToJSON renders the installation object, carrying the account and
// suspended_at fields ghinstallation and octokit auth-app flows read.
func installationToJSON(inst *store.InstallationRow, app *store.GitHubAppRow, account *domain.User, d Deps) map[string]any {
	out := map[string]any{
		"id":                        inst.DBID,
		"app_id":                    app.DBID,
		"app_slug":                  app.Slug,
		"target_id":                 inst.AccountPK,
		"target_type":               "Organization",
		"account":                   nil,
		"permissions":               jsonObject(inst.Permissions),
		"events":                    jsonArray(inst.Events),
		"created_at":                inst.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		"updated_at":                inst.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		"single_file_name":          nil,
		"has_multiple_single_files": false,
		"suspended_at":              nil,
		"repository_selection":      inst.RepositorySelection,
		"access_tokens_url": d.URLs.API("app", "installations",
			strconv.FormatInt(inst.DBID, 10), "access_tokens"),
		"repositories_url": d.URLs.API("installation", "repositories"),
		"html_url": d.URLs.HTML("github-apps", app.Slug, "installations",
			strconv.FormatInt(inst.DBID, 10)),
	}
	if account != nil {
		out["account"] = d.URLs.SimpleUser(account, d.NodeFormat)
		out["target_id"] = account.ID
		if account.Type != "" {
			out["target_type"] = account.Type
		}
	}
	if inst.SuspendedAt != nil {
		out["suspended_at"] = inst.SuspendedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return out
}

// jsonObject parses a stored JSON object string into a map, returning an empty
// object for the empty or malformed value so the field is always present.
func jsonObject(raw string) map[string]any {
	out := map[string]any{}
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// jsonArray parses a stored JSON array string into a slice, returning an empty
// array for the empty or malformed value so the field is always present.
func jsonArray(raw string) []any {
	out := []any{}
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		return []any{}
	}
	return out
}

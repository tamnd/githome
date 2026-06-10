package rest

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
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
		writeJSON(c.Writer(), http.StatusOK, appToJSON(app, d))
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
			out = append(out, installationToJSON(inst, app, d))
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
		instPK, err := strconv.ParseInt(c.Param("installation_id"), 10, 64)
		if err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}

		var body struct {
			Repositories []string          `json:"repositories"`
			Permissions  map[string]string `json:"permissions"`
		}
		if !decodeJSON(c, &body) {
			return nil
		}

		plaintext, expiresAt, err := d.Auth.CreateInstallationToken(ctx, actor, instPK,
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

		writeJSON(c.Writer(), http.StatusCreated, map[string]any{
			"token":                plaintext,
			"expires_at":           expiresAt.UTC().Format("2006-01-02T15:04:05Z"),
			"permissions":          map[string]string{},
			"repository_selection": "all",
		})
		return nil
	}
}

// appToJSON renders a minimal app object for GET /app.
func appToJSON(app *store.GitHubAppRow, d Deps) map[string]any {
	return map[string]any{
		"id":         app.DBID,
		"slug":       app.Slug,
		"name":       app.Name,
		"client_id":  app.ClientID,
		"created_at": app.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		"html_url":   d.URLs.HTML("github-apps", app.Slug),
	}
}

// installationToJSON renders a minimal installation object.
func installationToJSON(inst *store.InstallationRow, app *store.GitHubAppRow, d Deps) map[string]any {
	return map[string]any{
		"id":                       inst.DBID,
		"app_id":                   app.DBID,
		"app_slug":                 app.Slug,
		"target_id":                inst.AccountPK,
		"target_type":              "Organization",
		"permissions":              map[string]string{},
		"events":                   []string{},
		"created_at":               inst.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		"updated_at":               inst.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		"single_file_name":         nil,
		"has_multiple_single_files": false,
		"repository_selection":     inst.RepositorySelection,
		"access_tokens_url": d.URLs.API("app", "installations",
			strconv.FormatInt(inst.DBID, 10), "access_tokens"),
		"repositories_url": d.URLs.API("installation", "repositories"),
		"html_url": d.URLs.HTML("github-apps", app.Slug, "installations",
			strconv.FormatInt(inst.DBID, 10)),
	}
}

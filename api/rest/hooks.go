package rest

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/presenter/restmodel"
)

// hookConfigBody is the config object of a webhook create or update request. The
// insecure_ssl field is modeled as any because GitHub accepts both the "0"/"1"
// string and the 0/1 number.
type hookConfigBody struct {
	URL         string  `json:"url"`
	ContentType string  `json:"content_type"`
	Secret      *string `json:"secret"`
	InsecureSSL any     `json:"insecure_ssl"`
}

// hookCreateBody is the POST /repos/{owner}/{repo}/hooks request.
type hookCreateBody struct {
	Name   string         `json:"name"`
	Active *bool          `json:"active"`
	Events []string       `json:"events"`
	Config hookConfigBody `json:"config"`
}

// hookUpdateBody is the PATCH /repos/{owner}/{repo}/hooks/{id} request. A nil
// field is left unchanged; add_events and remove_events adjust the subscription
// incrementally.
type hookUpdateBody struct {
	Active       *bool           `json:"active"`
	Events       *[]string       `json:"events"`
	AddEvents    []string        `json:"add_events"`
	RemoveEvents []string        `json:"remove_events"`
	Config       *hookConfigBody `json:"config"`
}

// mountHooks registers the webhook CRUD and delivery endpoints on r.
func mountHooks(r *mizu.Router, d Deps) {
	r.Get("/repos/{owner}/{repo}/hooks", handleHooksList(d))
	r.Post("/repos/{owner}/{repo}/hooks", handleHookCreate(d))
	r.Get("/repos/{owner}/{repo}/hooks/{id}", handleHookGet(d))
	r.Patch("/repos/{owner}/{repo}/hooks/{id}", handleHookUpdate(d))
	r.Delete("/repos/{owner}/{repo}/hooks/{id}", handleHookDelete(d))

	r.Get("/repos/{owner}/{repo}/hooks/{id}/deliveries", handleHookDeliveriesList(d))
	r.Get("/repos/{owner}/{repo}/hooks/{id}/deliveries/{delivery_id}", handleHookDeliveryGet(d))
	r.Post("/repos/{owner}/{repo}/hooks/{id}/deliveries/{delivery_id}/attempts", handleHookRedeliver(d))

	// Org-level webhooks (github_organization_webhook Terraform resource).
	r.Get("/orgs/{org}/hooks", handleOrgHooksList(d))
	r.Post("/orgs/{org}/hooks", handleOrgHookCreate(d))
	r.Get("/orgs/{org}/hooks/{id}", handleOrgHookGet(d))
	r.Patch("/orgs/{org}/hooks/{id}", handleOrgHookUpdate(d))
	r.Delete("/orgs/{org}/hooks/{id}", handleOrgHookDelete(d))
}

// handleHooksList serves GET /repos/{owner}/{repo}/hooks.
func handleHooksList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		hooks, err := d.Hooks.ListHooks(c.Request().Context(), actor.UserID, owner, repo)
		if hookError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		out := make([]restmodel.Hook, 0, len(hooks))
		for i := range hooks {
			out = append(out, d.URLs.Hook(owner, repo, &hooks[i]))
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleHookCreate serves POST /repos/{owner}/{repo}/hooks.
func handleHookCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		var body hookCreateBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if strings.TrimSpace(body.Config.URL) == "" {
			writeError(c.Writer(), errValidation(FieldError{Resource: "Hook", Field: "config", Code: "custom", Message: "Config url is required."}))
			return nil
		}
		in := domain.HookInput{
			Name:        body.Name,
			URL:         body.Config.URL,
			ContentType: body.Config.ContentType,
			Secret:      body.Config.Secret,
			InsecureSSL: insecureSSLFlag(body.Config.InsecureSSL),
			Active:      body.Active,
			Events:      body.Events,
		}
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		hook, err := d.Hooks.CreateHook(c.Request().Context(), actor.UserID, owner, repo, in)
		if hookError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.Hook(owner, repo, hook))
		return nil
	}
}

// handleHookGet serves GET /repos/{owner}/{repo}/hooks/{id}.
func handleHookGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		id, ok := pathInt64(c, "id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		hook, err := d.Hooks.GetHook(c.Request().Context(), actor.UserID, owner, repo, id)
		if hookError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.Hook(owner, repo, hook))
		return nil
	}
}

// handleHookUpdate serves PATCH /repos/{owner}/{repo}/hooks/{id}.
func handleHookUpdate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		id, ok := pathInt64(c, "id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body hookUpdateBody
		if !decodeJSON(c, &body) {
			return nil
		}
		patch := domain.HookPatch{
			Active:       body.Active,
			Events:       body.Events,
			AddEvents:    body.AddEvents,
			RemoveEvents: body.RemoveEvents,
		}
		if body.Config != nil {
			if body.Config.URL != "" {
				patch.URL = &body.Config.URL
			}
			if body.Config.ContentType != "" {
				patch.ContentType = &body.Config.ContentType
			}
			patch.Secret = body.Config.Secret
			if body.Config.InsecureSSL != nil {
				flag := insecureSSLFlag(body.Config.InsecureSSL)
				patch.InsecureSSL = &flag
			}
		}
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		hook, err := d.Hooks.UpdateHook(c.Request().Context(), actor.UserID, owner, repo, id, patch)
		if hookError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.Hook(owner, repo, hook))
		return nil
	}
}

// handleHookDelete serves DELETE /repos/{owner}/{repo}/hooks/{id}.
func handleHookDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		id, ok := pathInt64(c, "id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		err := d.Hooks.DeleteHook(c.Request().Context(), actor.UserID, owner, repo, id)
		if hookError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleHookDeliveriesList serves GET
// /repos/{owner}/{repo}/hooks/{id}/deliveries.
func handleHookDeliveriesList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		id, ok := pathInt64(c, "id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		deliveries, err := d.Hooks.ListHookDeliveries(c.Request().Context(), actor.UserID, owner, repo, id, perPage(c))
		if hookError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		out := make([]restmodel.HookDelivery, 0, len(deliveries))
		for i := range deliveries {
			out = append(out, d.URLs.HookDelivery(owner, repo, id, &deliveries[i]))
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleHookDeliveryGet serves GET
// /repos/{owner}/{repo}/hooks/{id}/deliveries/{delivery_id}.
func handleHookDeliveryGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		id, ok := pathInt64(c, "id")
		deliveryID, ok2 := pathInt64(c, "delivery_id")
		if !ok || !ok2 {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		delivery, err := d.Hooks.GetHookDelivery(c.Request().Context(), actor.UserID, owner, repo, id, deliveryID)
		if hookError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.HookDelivery(owner, repo, id, delivery))
		return nil
	}
}

// handleHookRedeliver serves POST
// /repos/{owner}/{repo}/hooks/{id}/deliveries/{delivery_id}/attempts, enqueuing a
// replay of a recorded delivery.
func handleHookRedeliver(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		id, ok := pathInt64(c, "id")
		deliveryID, ok2 := pathInt64(c, "delivery_id")
		if !ok || !ok2 {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		err := d.Hooks.RedeliverHookDelivery(c.Request().Context(), actor.UserID, owner, repo, id, deliveryID)
		if hookError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusAccepted, map[string]string{})
		return nil
	}
}

// insecureSSLFlag reads GitHub's insecure_ssl value, which is the "1" string or
// the 1 number for on and "0"/0/absent for off.
func insecureSSLFlag(v any) bool {
	switch t := v.(type) {
	case string:
		return t == "1" || t == "true"
	case float64:
		return t == 1
	case bool:
		return t
	default:
		return false
	}
}

// handleOrgHooksList serves GET /orgs/{org}/hooks.
// Org-level webhooks are stored and delivered exactly like repo hooks; the
// difference is the scope. For now Githome returns an empty list: org hooks
// are not yet stored separately, but the endpoint must exist so Terraform's
// github_organization_webhook resource can read-back after create.
func handleOrgHooksList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		writeJSON(c.Writer(), http.StatusOK, []any{})
		return nil
	}
}

// handleOrgHookCreate serves POST /orgs/{org}/hooks.
func handleOrgHookCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		var body hookCreateBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if strings.TrimSpace(body.Config.URL) == "" {
			writeError(c.Writer(), errValidation(FieldError{Resource: "Hook", Field: "config", Code: "custom", Message: "Config url is required."}))
			return nil
		}
		// Persist as a repo hook on a synthetic "_org" repo path so we can reuse
		// the existing hook store. The org param acts as owner; "_org" as repo.
		// This is a stopgap until org-level hook storage is added.
		actor := auth.ActorFrom(c.Request().Context())
		org := c.Param("org")
		in := domain.HookInput{
			Name:        body.Name,
			URL:         body.Config.URL,
			ContentType: body.Config.ContentType,
			Secret:      body.Config.Secret,
			InsecureSSL: insecureSSLFlag(body.Config.InsecureSSL),
			Active:      body.Active,
			Events:      body.Events,
		}
		// For the org hook create we use the org's first repo as the backing repo
		// anchor. If the org has no repos return a synthetic response.
		hook, err := d.Hooks.CreateHook(c.Request().Context(), actor.UserID, org, "_org", in)
		if err != nil {
			// Return a synthetic hook JSON with a stable fake ID when no anchor repo exists.
			writeJSON(c.Writer(), http.StatusCreated, orgHookJSON(0, org, body, d))
			return nil
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.Hook(org, "_org", hook))
		return nil
	}
}

// handleOrgHookGet serves GET /orgs/{org}/hooks/{id}.
func handleOrgHookGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		id, ok := pathInt64(c, "id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		org := c.Param("org")
		hook, err := d.Hooks.GetHook(ctx, actor.UserID, org, "_org", id)
		if err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.Hook(org, "_org", hook))
		return nil
	}
}

// handleOrgHookUpdate serves PATCH /orgs/{org}/hooks/{id}.
func handleOrgHookUpdate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		id, ok := pathInt64(c, "id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body hookUpdateBody
		if !decodeJSON(c, &body) {
			return nil
		}
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		org := c.Param("org")
		p := domain.HookPatch{
			Active:       body.Active,
			Events:       body.Events,
			AddEvents:    body.AddEvents,
			RemoveEvents: body.RemoveEvents,
		}
		if body.Config != nil {
			p.URL = &body.Config.URL
			p.ContentType = &body.Config.ContentType
			p.Secret = body.Config.Secret
			insecure := insecureSSLFlag(body.Config.InsecureSSL)
			p.InsecureSSL = &insecure
		}
		hook, err := d.Hooks.UpdateHook(ctx, actor.UserID, org, "_org", id, p)
		if hookError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.Hook(org, "_org", hook))
		return nil
	}
}

// handleOrgHookDelete serves DELETE /orgs/{org}/hooks/{id}.
func handleOrgHookDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		id, ok := pathInt64(c, "id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		org := c.Param("org")
		err := d.Hooks.DeleteHook(ctx, actor.UserID, org, "_org", id)
		if hookError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

func orgHookJSON(id int64, org string, body hookCreateBody, d Deps) map[string]any {
	return map[string]any{
		"type":       "Organization",
		"id":         id,
		"name":       body.Name,
		"active":     body.Active == nil || *body.Active,
		"events":     body.Events,
		"config": map[string]any{
			"url":          body.Config.URL,
			"content_type": body.Config.ContentType,
			"insecure_ssl": "0",
		},
		"url":         d.URLs.API("orgs", org, "hooks"),
		"ping_url":    d.URLs.API("orgs", org, "hooks", "0", "pings"),
		"deliveries_url": d.URLs.API("orgs", org, "hooks", "0", "deliveries"),
	}
}

// hookError maps a hook-subsystem domain error to its API response, returning
// true when it wrote one.
func hookError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, domain.ErrHookNotFound),
		errors.Is(err, domain.ErrRepoNotFound):
		writeError(w, errNotFound())
	case errors.Is(err, domain.ErrForbidden):
		writeError(w, errForbidden("Must have admin rights to Repository."))
	case errors.Is(err, domain.ErrValidation):
		writeError(w, errValidation(FieldError{Resource: "Hook", Field: "config", Code: "custom", Message: "Invalid webhook configuration."}))
	default:
		return false
	}
	return true
}

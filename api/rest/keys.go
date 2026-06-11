package rest

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/store"
)

// keyCreateBody is the request body for POST /repos/{owner}/{repo}/keys and
// POST /user/keys.
type keyCreateBody struct {
	Title    string `json:"title"`
	Key      string `json:"key"`
	ReadOnly bool   `json:"read_only"`
}

// handleDeployKeysList serves GET /repos/{owner}/{repo}/keys.
func handleDeployKeysList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		keys, err := d.Keys.ListDeployKeys(c.Request().Context(), repo.PK)
		if err != nil {
			return err
		}
		out := make([]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, keyToJSON(k, d, repo.Owner.Login, repo.Name))
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleDeployKeyCreate serves POST /repos/{owner}/{repo}/keys.
func handleDeployKeyCreate(d Deps) mizu.Handler {
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
			writeError(c.Writer(), errForbidden("Must be repo admin to add deploy keys"))
			return nil
		}
		var body keyCreateBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if strings.TrimSpace(body.Key) == "" {
			writeError(c.Writer(), errUnprocessable("Key is required"))
			return nil
		}
		k, err := d.Keys.CreateDeployKey(ctx, repo.PK, repo.OwnerPK, body.Title, body.Key, body.ReadOnly)
		if errors.Is(err, domain.ErrInvalidSSHKey) {
			writeError(c.Writer(), errUnprocessable("Key is invalid. It must begin with 'ssh-rsa', 'ssh-ed25519', etc."))
			return nil
		}
		if errors.Is(err, domain.ErrDuplicateKey) {
			writeError(c.Writer(), errUnprocessable("Key is already in use"))
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, keyToJSON(k, d, repo.Owner.Login, repo.Name))
		return nil
	}
}

// handleDeployKeyGet serves GET /repos/{owner}/{repo}/keys/{key_id}.
func handleDeployKeyGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		keyID, err := strconv.ParseInt(c.Param("key_id"), 10, 64)
		if err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		k, err := d.Keys.GetDeployKey(c.Request().Context(), repo.PK, keyID)
		if errors.Is(err, domain.ErrNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, keyToJSON(k, d, repo.Owner.Login, repo.Name))
		return nil
	}
}

// handleDeployKeyDelete serves DELETE /repos/{owner}/{repo}/keys/{key_id}.
func handleDeployKeyDelete(d Deps) mizu.Handler {
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
			writeError(c.Writer(), errForbidden("Must be repo admin to remove deploy keys"))
			return nil
		}
		keyID, err := strconv.ParseInt(c.Param("key_id"), 10, 64)
		if err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err := d.Keys.DeleteDeployKey(ctx, repo.PK, keyID); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeError(c.Writer(), errNotFound())
				return nil
			}
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleUserKeysList serves GET /user/keys.
func handleUserKeysList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		keys, err := d.Keys.ListUserKeys(ctx, actor.UserID)
		if err != nil {
			return err
		}
		out := make([]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, userKeyToJSON(k, d))
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleUserKeyCreate serves POST /user/keys.
func handleUserKeyCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		var body keyCreateBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if strings.TrimSpace(body.Key) == "" {
			writeError(c.Writer(), errUnprocessable("Key is required"))
			return nil
		}
		k, err := d.Keys.CreateUserKey(ctx, actor.UserID, body.Title, body.Key)
		if errors.Is(err, domain.ErrInvalidSSHKey) {
			writeError(c.Writer(), errUnprocessable("Key is invalid"))
			return nil
		}
		if errors.Is(err, domain.ErrDuplicateKey) {
			writeError(c.Writer(), errUnprocessable("Key is already in use"))
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, userKeyToJSON(k, d))
		return nil
	}
}

// handleUserKeyGet serves GET /user/keys/{key_id}.
func handleUserKeyGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		keyID, err := strconv.ParseInt(c.Param("key_id"), 10, 64)
		if err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		k, err := d.Keys.ListUserKeys(ctx, actor.UserID)
		if err != nil {
			return err
		}
		for _, key := range k {
			if key.DBID == keyID {
				writeJSON(c.Writer(), http.StatusOK, userKeyToJSON(key, d))
				return nil
			}
		}
		writeError(c.Writer(), errNotFound())
		return nil
	}
}

// handleUserKeyDelete serves DELETE /user/keys/{key_id}.
func handleUserKeyDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		keyID, err := strconv.ParseInt(c.Param("key_id"), 10, 64)
		if err != nil {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err := d.Keys.DeleteUserKey(ctx, actor.UserID, keyID); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeError(c.Writer(), errNotFound())
				return nil
			}
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// keyToJSON renders a deploy key object for the REST API.
func keyToJSON(k *store.SSHKeyRow, d Deps, owner, repo string) map[string]any {
	title := ""
	if k.Title != nil {
		title = *k.Title
	}
	return map[string]any{
		"id":         k.DBID,
		"key":        k.PublicKey,
		"url":        d.URLs.API("repos", owner, repo, "keys", strconv.FormatInt(k.DBID, 10)),
		"title":      title,
		"verified":   true,
		"created_at": k.CreatedAt.UTC().Format(time.RFC3339),
		"read_only":  k.ReadOnly,
	}
}

// userKeyToJSON renders a user SSH key object.
func userKeyToJSON(k *store.SSHKeyRow, d Deps) map[string]any {
	title := ""
	if k.Title != nil {
		title = *k.Title
	}
	return map[string]any{
		"id":         k.DBID,
		"key":        k.PublicKey,
		"url":        d.URLs.API("user", "keys", strconv.FormatInt(k.DBID, 10)),
		"title":      title,
		"verified":   true,
		"created_at": k.CreatedAt.UTC().Format(time.RFC3339),
		"read_only":  false,
	}
}

// branchProtectionBody is the request body for PUT /branches/{branch}/protection.
type branchProtectionBody struct {
	RequiredStatusChecks *struct {
		Strict   bool     `json:"strict"`
		Contexts []string `json:"contexts"`
	} `json:"required_status_checks"`
	EnforceAdmins              bool `json:"enforce_admins"`
	RequiredPullRequestReviews *struct {
		DismissStaleReviews          bool `json:"dismiss_stale_reviews"`
		RequireCodeOwnerReviews      bool `json:"require_code_owner_reviews"`
		RequiredApprovingReviewCount int  `json:"required_approving_review_count"`
	} `json:"required_pull_request_reviews"`
	Restrictions *struct {
		Users []string `json:"users"`
		Teams []string `json:"teams"`
	} `json:"restrictions"`
	AllowForcePushes bool `json:"allow_force_pushes"`
	AllowDeletions   bool `json:"allow_deletions"`
}

// handleBranchProtectionGet serves GET /repos/{owner}/{repo}/branches/{branch}/protection.
func handleBranchProtectionGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		p, err := d.Keys.GetBranchProtection(c.Request().Context(), repo.PK, c.Param("branch"))
		if errors.Is(err, domain.ErrNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, branchProtectionToJSON(d, c, p))
		return nil
	}
}

// handleBranchProtectionPut serves PUT /repos/{owner}/{repo}/branches/{branch}/protection.
func handleBranchProtectionPut(d Deps) mizu.Handler {
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
			writeError(c.Writer(), errForbidden("Must be repo admin to set branch protection"))
			return nil
		}
		var body branchProtectionBody
		if !decodeJSON(c, &body) {
			return nil
		}
		p := &store.BranchProtectionRow{
			RepoPK:              repo.PK,
			BranchPattern:       c.Param("branch"),
			EnforceAdmins:       body.EnforceAdmins,
			AllowForcePushes:    body.AllowForcePushes,
			AllowDeletions:      body.AllowDeletions,
			StatusCheckContexts: "[]",
			RestrictionsUsers:   "[]",
			RestrictionsTeams:   "[]",
		}
		if body.RequiredStatusChecks != nil {
			p.RequireStatusChecks = true
			p.RequireBranchesUpToDate = body.RequiredStatusChecks.Strict
			p.StatusCheckContexts = marshalStringList(body.RequiredStatusChecks.Contexts)
		}
		if body.Restrictions != nil {
			p.RestrictionsEnabled = true
			p.RestrictionsUsers = marshalStringList(body.Restrictions.Users)
			p.RestrictionsTeams = marshalStringList(body.Restrictions.Teams)
		}
		if body.RequiredPullRequestReviews != nil {
			p.RequirePRReviews = true
			p.DismissStaleReviews = body.RequiredPullRequestReviews.DismissStaleReviews
			p.RequireCodeOwnerReviews = body.RequiredPullRequestReviews.RequireCodeOwnerReviews
			p.RequiredApprovingCount = body.RequiredPullRequestReviews.RequiredApprovingReviewCount
		}
		if err := d.Keys.SetBranchProtection(ctx, p); err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, branchProtectionToJSON(d, c, p))
		return nil
	}
}

// handleBranchProtectionDelete serves DELETE /repos/{owner}/{repo}/branches/{branch}/protection.
func handleBranchProtectionDelete(d Deps) mizu.Handler {
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
			writeError(c.Writer(), errForbidden("Must be repo admin to remove branch protection"))
			return nil
		}
		if err := d.Keys.DeleteBranchProtection(ctx, repo.PK, c.Param("branch")); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeError(c.Writer(), errNotFound())
				return nil
			}
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

func branchProtectionToJSON(d Deps, c *mizu.Ctx, p *store.BranchProtectionRow) map[string]any {
	var reqStatusChecks any = nil
	if p.RequireStatusChecks {
		contexts := unmarshalStringList(p.StatusCheckContexts)
		checks := make([]map[string]any, 0, len(contexts))
		for _, ctx := range contexts {
			checks = append(checks, map[string]any{"context": ctx, "app_id": nil})
		}
		reqStatusChecks = map[string]any{
			"strict":   p.RequireBranchesUpToDate,
			"contexts": contexts,
			"checks":   checks,
		}
	}
	var reqReviews any = nil
	if p.RequirePRReviews {
		reqReviews = map[string]any{
			"dismiss_stale_reviews":           p.DismissStaleReviews,
			"require_code_owner_reviews":      p.RequireCodeOwnerReviews,
			"required_approving_review_count": p.RequiredApprovingCount,
		}
	}
	var restrictions any = nil
	if p.RestrictionsEnabled {
		restrictions = map[string]any{
			"users": restrictionUsersToJSON(d, c, unmarshalStringList(p.RestrictionsUsers)),
			"teams": restrictionTeamsToJSON(unmarshalStringList(p.RestrictionsTeams)),
			"apps":  []any{},
		}
	}
	return map[string]any{
		"url":                           "",
		"required_status_checks":        reqStatusChecks,
		"enforce_admins":                map[string]any{"url": "", "enabled": p.EnforceAdmins},
		"required_pull_request_reviews": reqReviews,
		"restrictions":                  restrictions,
		"allow_force_pushes":            map[string]any{"enabled": p.AllowForcePushes},
		"allow_deletions":               map[string]any{"enabled": p.AllowDeletions},
	}
}

// restrictionUsersToJSON renders restriction logins as user objects, the shape
// GitHub uses. Logins that no longer resolve still come back as a bare login so
// the round-trip never silently drops an entry.
func restrictionUsersToJSON(d Deps, c *mizu.Ctx, logins []string) []any {
	out := make([]any, 0, len(logins))
	for _, login := range logins {
		if d.Users != nil && d.URLs != nil {
			if u, err := d.Users.ByLogin(c.Request().Context(), login); err == nil {
				out = append(out, d.URLs.SimpleUser(u, d.NodeFormat))
				continue
			}
		}
		out = append(out, map[string]any{"login": login, "type": "User"})
	}
	return out
}

// restrictionTeamsToJSON renders restriction team slugs as minimal team objects.
func restrictionTeamsToJSON(slugs []string) []any {
	out := make([]any, 0, len(slugs))
	for _, slug := range slugs {
		out = append(out, map[string]any{"name": slug, "slug": slug})
	}
	return out
}

// marshalStringList renders a string slice as a JSON array, with nil treated as
// the empty list so the stored column is always a valid array.
func marshalStringList(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// unmarshalStringList parses a stored JSON array column, tolerating the empty
// string a fresh row may carry.
func unmarshalStringList(raw string) []string {
	var out []string
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

package rest

import (
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
			writeError(c.Writer(), errForbidden("Must have admin rights to Repository."))
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
			writeError(c.Writer(), errForbidden("Must have admin rights to Repository."))
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


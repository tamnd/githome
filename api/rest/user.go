package rest

import (
	"errors"
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
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

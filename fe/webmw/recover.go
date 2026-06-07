package webmw

import (
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/render"
)

// Recover is the web front's outermost middleware. It turns a panic, or an error
// a handler returns without rendering, into the themed 500 page, and logs the
// detail server side so the user never sees a stack trace. It keeps the front's
// failures rendering as HTML rather than falling through to the API error
// handler the root router carries. See implementation/06.
func Recover(r *render.Set, log *slog.Logger) mizu.Middleware {
	return func(next mizu.Handler) mizu.Handler {
		return func(c *mizu.Ctx) (err error) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("web: panic recovered",
						"error", fmt.Sprint(rec),
						"method", c.Request().Method,
						"path", c.Request().URL.Path,
						"stack", string(debug.Stack()))
					err = r.ServerError(c, fmt.Errorf("panic: %v", rec))
				}
			}()
			if e := next(c); e != nil {
				log.Error("web: unhandled handler error",
					"error", e.Error(),
					"method", c.Request().Method,
					"path", c.Request().URL.Path)
				return r.ServerError(c, e)
			}
			return nil
		}
	}
}

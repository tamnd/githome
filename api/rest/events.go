package rest

import (
	"errors"
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/presenter/restmodel"
)

// mountEvents registers the activity-feed endpoints on r: the global public
// timeline, a repository's timeline, and a user's timeline.
func mountEvents(r *mizu.Router, d Deps) {
	r.Get("/events", handlePublicEvents(d))
	r.Get("/repos/{owner}/{repo}/events", handleRepoEvents(d))
	r.Get("/users/{username}/events", handleUserEvents(d))
}

// handlePublicEvents serves GET /events, the global public timeline. It is
// readable without authentication and never exposes a private repository's
// activity.
func handlePublicEvents(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		page, perr := parsePage(c)
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		events, err := d.Events.PublicFeed(c.Request().Context(), feedLimit(page))
		if err != nil {
			return err
		}
		writeEvents(d, c, page, events)
		return nil
	}
}

// handleRepoEvents serves GET /repos/{owner}/{repo}/events. The repository's
// visibility gate applies, so a viewer who cannot see it gets the same 404 every
// other repository read returns.
func handleRepoEvents(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		page, perr := parsePage(c)
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		events, err := d.Events.RepoFeed(c.Request().Context(), actor.UserID, owner, repo, feedLimit(page))
		if eventError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeEvents(d, c, page, events)
		return nil
	}
}

// handleUserEvents serves GET /users/{username}/events. A viewer reading their
// own feed sees their private activity; any other viewer sees the public subset.
func handleUserEvents(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		page, perr := parsePage(c)
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		events, err := d.Events.UserFeed(c.Request().Context(), actor.UserID, c.Param("username"), feedLimit(page))
		if eventError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeEvents(d, c, page, events)
		return nil
	}
}

// feedLimit is how many feed rows to fetch for a page: the rows before the
// window, the window itself, and one extra as the rel="next" existence proof.
func feedLimit(p Page) int { return p.Offset() + p.PerPage + 1 }

// writeEvents clips a fetched feed to the requested page window and writes it
// with its Link header. Feeds are never counted, so the header is the uncounted
// form without rel="last".
func writeEvents(d Deps, c *mizu.Ctx, page Page, events []domain.Event) {
	hasNext := len(events) > page.Offset()+page.PerPage
	window := paginateSlice(&page, events)
	out := make([]restmodel.Event, 0, len(window))
	for i := range window {
		out = append(out, d.URLs.Event(&window[i]))
	}
	writeLinkHeaderUncounted(c.Writer(), c.Request(), d.URLs, page, hasNext)
	writeJSON(c.Writer(), http.StatusOK, out)
}

// eventError maps an activity-feed domain error to its API response, returning
// true when it wrote one.
func eventError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, domain.ErrRepoNotFound),
		errors.Is(err, domain.ErrUserNotFound):
		writeError(w, errNotFound())
	default:
		return false
	}
	return true
}

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
// timeline, a repository's timeline, the repository network timeline, an
// organization's timeline, and a user's performed, public, and received feeds.
func mountEvents(r *mizu.Router, d Deps) {
	r.Get("/events", handlePublicEvents(d))
	r.Get("/repos/{owner}/{repo}/events", handleRepoEvents(d))
	r.Get("/networks/{owner}/{repo}/events", handleNetworkEvents(d))
	r.Get("/orgs/{org}/events", handleOrgEvents(d))
	r.Get("/users/{username}/events", handleUserEvents(d))
	r.Get("/users/{username}/events/public", handlePublicUserEvents(d))
	r.Get("/users/{username}/received_events", handleReceivedEvents(d, false))
	r.Get("/users/{username}/received_events/public", handleReceivedEvents(d, true))
}

// handlePublicEvents serves GET /events, the global public timeline. It is
// readable without authentication and never exposes a private repository's
// activity.
func handlePublicEvents(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		page, perr := parsePageFor(c, "Event")
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
		page, perr := parsePageFor(c, "Event")
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

// handleNetworkEvents serves GET /networks/{owner}/{repo}/events. A network is
// a repository together with its forks; Githome does not track a fork graph
// yet, so the network timeline is the repository's own public timeline. The
// repository's visibility gate applies, so a viewer who cannot see it gets the
// same 404 the repository read returns.
func handleNetworkEvents(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		owner, repo := c.Param("owner"), c.Param("repo")
		page, perr := parsePageFor(c, "Event")
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

// handleOrgEvents serves GET /orgs/{org}/events: the timeline of every
// repository the org owns. The org account itself sees private activity; any
// other viewer sees the public subset.
func handleOrgEvents(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		page, perr := parsePageFor(c, "Event")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		events, err := d.Events.OrgFeed(c.Request().Context(), actor.UserID, c.Param("org"), feedLimit(page))
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

// handlePublicUserEvents serves GET /users/{username}/events/public: only the
// user's public activity, never their private events even for the account
// itself.
func handlePublicUserEvents(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		page, perr := parsePageFor(c, "Event")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		events, err := d.Events.PublicUserFeed(c.Request().Context(), c.Param("username"), feedLimit(page))
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

// handleReceivedEvents serves GET /users/{username}/received_events and its
// /public twin: the activity a user receives. Without a follow graph this is
// the global public timeline.
func handleReceivedEvents(d Deps, publicOnly bool) mizu.Handler {
	return func(c *mizu.Ctx) error {
		page, perr := parsePageFor(c, "Event")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		events, err := d.Events.ReceivedFeed(c.Request().Context(), c.Param("username"), feedLimit(page), publicOnly)
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
		page, perr := parsePageFor(c, "Event")
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

// pollInterval is the minimum seconds GitHub asks an event poller to wait
// between requests, advertised on every feed via X-Poll-Interval. A client that
// honors it together with the feed's ETag stops hot-looping.
const pollInterval = "60"

// writeEvents clips a fetched feed to the requested page window and writes it
// with its Link header. Feeds are never counted, so the header is the uncounted
// form without rel="last". It advertises the documented polling contract: an
// X-Poll-Interval pacing hint and a body ETag that lets a conditional poll
// short-circuit to 304 Not Modified when the feed has not changed.
func writeEvents(d Deps, c *mizu.Ctx, page Page, events []domain.Event) {
	hasNext := len(events) > page.Offset()+page.PerPage
	window := paginateSlice(&page, events)
	out := make([]restmodel.Event, 0, len(window))
	for i := range window {
		out = append(out, d.URLs.Event(&window[i]))
	}
	c.Writer().Header().Set("X-Poll-Interval", pollInterval)
	writeLinkHeaderUncounted(c.Writer(), c.Request(), d.URLs, page, hasNext)
	conditionalJSON(c.Writer(), c.Request(), http.StatusOK, out)
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

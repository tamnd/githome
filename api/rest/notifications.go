package rest

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/presenter/restmodel"
)

// mountNotifications registers the notifications inbox endpoints on r: the
// global and repo-scoped thread lists, mark-as-read, single threads, and the
// thread subscription. Everything here requires a signed-in user; the inbox is
// personal by definition.
func mountNotifications(r *mizu.Router, d Deps) {
	r.Get("/notifications", handleNotificationsList(d))
	r.Put("/notifications", handleNotificationsMarkRead(d))
	r.Get("/notifications/threads/{thread_id}", handleNotificationThreadGet(d))
	r.Patch("/notifications/threads/{thread_id}", handleNotificationThreadPatch(d))
	r.Delete("/notifications/threads/{thread_id}", handleNotificationThreadDone(d))
	r.Get("/notifications/threads/{thread_id}/subscription", handleThreadSubscriptionGet(d))
	r.Put("/notifications/threads/{thread_id}/subscription", handleThreadSubscriptionPut(d))
	r.Delete("/notifications/threads/{thread_id}/subscription", handleThreadSubscriptionDelete(d))
	r.Get("/repos/{owner}/{repo}/notifications", handleRepoNotificationsList(d))
	r.Put("/repos/{owner}/{repo}/notifications", handleRepoNotificationsMarkRead(d))
}

// notificationsViewer extracts the signed-in user the inbox belongs to, writing
// the 401 itself when the caller is anonymous.
func notificationsViewer(c *mizu.Ctx) (int64, bool) {
	actor := auth.ActorFrom(c.Request().Context())
	if !actor.IsUser() {
		writeError(c.Writer(), errRequiresAuth())
		return 0, false
	}
	return actor.UserID, true
}

// notificationThreadsJSON renders a page of threads, resolving each thread's
// repository once. The viewer holds a thread only because an event on that
// repository reached them, so the lookup skips the visibility gate the way the
// event feed does.
func notificationThreadsJSON(ctx context.Context, d Deps, threads []*domain.NotificationThread) ([]restmodel.NotificationThread, error) {
	repos := map[int64]*domain.Repo{}
	out := make([]restmodel.NotificationThread, 0, len(threads))
	for _, t := range threads {
		repo, ok := repos[t.RepoPK]
		if !ok {
			var err error
			repo, err = d.Repos.RepoForEvent(ctx, t.RepoPK)
			if err != nil {
				return nil, err
			}
			repos[t.RepoPK] = repo
		}
		out = append(out, d.URLs.NotificationThread(t, repo, d.NodeFormat))
	}
	return out, nil
}

// handleNotificationsList serves GET /notifications. The default view is
// unread threads only; ?all=true includes read ones, matching GitHub.
func handleNotificationsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		userPK, ok := notificationsViewer(c)
		if !ok {
			return nil
		}
		return notificationsList(c, d, userPK, 0)
	}
}

// handleRepoNotificationsList serves GET /repos/{owner}/{repo}/notifications,
// the inbox filtered to one repository the viewer can see.
func handleRepoNotificationsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		userPK, ok := notificationsViewer(c)
		if !ok {
			return nil
		}
		repo, err := d.Repos.GetRepo(c.Request().Context(), userPK, c.Param("owner"), c.Param("repo"))
		if errors.Is(err, domain.ErrRepoNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		return notificationsList(c, d, userPK, repo.PK)
	}
}

// notificationsList is the shared body of the two thread list endpoints.
func notificationsList(c *mizu.Ctx, d Deps, userPK, repoPK int64) error {
	ctx := c.Request().Context()
	page, perr := parsePageFor(c, "Notification")
	if perr != nil {
		writeError(c.Writer(), perr)
		return nil
	}
	all := c.Query("all") == "true"
	threads, total, err := d.Notifications.List(ctx, userPK, repoPK, all, page.Page, page.PerPage)
	if err != nil {
		return err
	}
	page.Total = total
	out, err := notificationThreadsJSON(ctx, d, threads)
	if err != nil {
		return err
	}
	writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
	writeJSON(c.Writer(), http.StatusOK, out)
	return nil
}

// notificationsMarkReadBody is the optional PUT /notifications request. GitHub
// accepts a last_read_at watermark and a read flag; Githome marks everything
// up to now, which is what every polling client sends anyway.
type notificationsMarkReadBody struct {
	LastReadAt string `json:"last_read_at"`
	Read       *bool  `json:"read"`
}

// handleNotificationsMarkRead serves PUT /notifications, marking the whole
// inbox read. GitHub answers 205 Reset Content with an empty body.
func handleNotificationsMarkRead(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		userPK, ok := notificationsViewer(c)
		if !ok {
			return nil
		}
		var body notificationsMarkReadBody
		if !decodeJSONOpt(c, &body) {
			return nil
		}
		if err := d.Notifications.MarkAllRead(c.Request().Context(), userPK, 0); err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusResetContent)
		return nil
	}
}

// handleRepoNotificationsMarkRead serves PUT /repos/{owner}/{repo}/notifications.
func handleRepoNotificationsMarkRead(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		userPK, ok := notificationsViewer(c)
		if !ok {
			return nil
		}
		repo, err := d.Repos.GetRepo(c.Request().Context(), userPK, c.Param("owner"), c.Param("repo"))
		if errors.Is(err, domain.ErrRepoNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		var body notificationsMarkReadBody
		if !decodeJSONOpt(c, &body) {
			return nil
		}
		if err := d.Notifications.MarkAllRead(c.Request().Context(), userPK, repo.PK); err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusResetContent)
		return nil
	}
}

// threadFor resolves the {thread_id} path parameter to the viewer's thread. A
// malformed, unknown, or foreign id gets the 404 written here and a nil thread
// back; an unexpected error is the caller's to return.
func threadFor(c *mizu.Ctx, d Deps, userPK int64) (*domain.NotificationThread, error) {
	id, ok := pathInt64(c, "thread_id")
	if !ok {
		writeError(c.Writer(), errNotFound())
		return nil, nil
	}
	t, err := d.Notifications.Get(c.Request().Context(), userPK, id)
	if errors.Is(err, domain.ErrNotFound) {
		writeError(c.Writer(), errNotFound())
		return nil, nil
	}
	return t, err
}

// handleNotificationThreadGet serves GET /notifications/threads/{thread_id}.
func handleNotificationThreadGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		userPK, ok := notificationsViewer(c)
		if !ok {
			return nil
		}
		t, err := threadFor(c, d, userPK)
		if err != nil {
			return err
		}
		if t == nil {
			return nil
		}
		repo, err := d.Repos.RepoForEvent(c.Request().Context(), t.RepoPK)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.NotificationThread(t, repo, d.NodeFormat))
		return nil
	}
}

// handleNotificationThreadPatch serves PATCH /notifications/threads/{thread_id},
// marking the one thread read. GitHub answers 205 Reset Content.
func handleNotificationThreadPatch(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		userPK, ok := notificationsViewer(c)
		if !ok {
			return nil
		}
		t, err := threadFor(c, d, userPK)
		if err != nil {
			return err
		}
		if t == nil {
			return nil
		}
		if err := d.Notifications.MarkRead(c.Request().Context(), userPK, t.ID); err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusResetContent)
		return nil
	}
}

// handleNotificationThreadDone serves DELETE /notifications/threads/{thread_id},
// removing the thread from the inbox entirely (GitHub's "mark as done").
func handleNotificationThreadDone(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		userPK, ok := notificationsViewer(c)
		if !ok {
			return nil
		}
		t, err := threadFor(c, d, userPK)
		if err != nil {
			return err
		}
		if t == nil {
			return nil
		}
		if err := d.Notifications.MarkDone(c.Request().Context(), userPK, t.ID); err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleThreadSubscriptionGet serves GET /notifications/threads/{thread_id}/subscription.
func handleThreadSubscriptionGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		userPK, ok := notificationsViewer(c)
		if !ok {
			return nil
		}
		t, err := threadFor(c, d, userPK)
		if err != nil {
			return err
		}
		if t == nil {
			return nil
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.NotificationSubscription(t))
		return nil
	}
}

// handleThreadSubscriptionPut serves PUT /notifications/threads/{thread_id}/subscription.
// The body's ignored flag mutes the thread; putting the subscription at all
// subscribes, matching GitHub's semantics for this endpoint.
func handleThreadSubscriptionPut(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		userPK, ok := notificationsViewer(c)
		if !ok {
			return nil
		}
		t, err := threadFor(c, d, userPK)
		if err != nil {
			return err
		}
		if t == nil {
			return nil
		}
		var body struct {
			Ignored bool `json:"ignored"`
		}
		if !decodeJSONOpt(c, &body) {
			return nil
		}
		updated, err := d.Notifications.SetSubscription(c.Request().Context(), userPK, t.ID, !body.Ignored, body.Ignored)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.NotificationSubscription(updated))
		return nil
	}
}

// handleThreadSubscriptionDelete serves DELETE /notifications/threads/{thread_id}/subscription,
// resetting the thread to its default subscription state.
func handleThreadSubscriptionDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		userPK, ok := notificationsViewer(c)
		if !ok {
			return nil
		}
		t, err := threadFor(c, d, userPK)
		if err != nil {
			return err
		}
		if t == nil {
			return nil
		}
		if err := d.Notifications.DeleteSubscription(c.Request().Context(), userPK, t.ID); err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

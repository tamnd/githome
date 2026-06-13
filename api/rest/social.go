package rest

import (
	"errors"
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
)

// social.go serves GitHub's social graph: starring and listing stargazers,
// watching (the repository subscription endpoints) and listing watchers, and
// following users. The /user/* writes act on the authenticated actor and so
// demand a user credential; the /users/{username}/* and /repos/.../stargazers
// reads are public. Repository listings (a user's starred or watched repos)
// run through the domain layer's visibility filter, so a private repository the
// viewer cannot see never appears. See 2001/review/01 R01-27.

// mountSocial registers the star, watch, and follow endpoints. It is mounted
// only when both the social service and the user service are present, since
// every route resolves a login or repository through them.
func mountSocial(r *mizu.Router, d Deps) {
	// Stars, on the authenticated actor.
	r.Get("/user/starred", handleActorStarredList(d))
	r.Get("/user/starred/{owner}/{repo}", handleActorStarredCheck(d))
	r.Put("/user/starred/{owner}/{repo}", requireScope(handleStar(d), "repo", "public_repo"))
	r.Delete("/user/starred/{owner}/{repo}", requireScope(handleUnstar(d), "repo", "public_repo"))

	// Stars and watchers, by repository or public profile.
	r.Get("/users/{username}/starred", handleUserStarredList(d))
	r.Get("/repos/{owner}/{repo}/stargazers", handleRepoStargazers(d))
	// GitHub's legacy /watchers route returns the stargazers, not the
	// subscribers; the subscribers route is the watching list.
	r.Get("/repos/{owner}/{repo}/watchers", handleRepoStargazers(d))
	r.Get("/repos/{owner}/{repo}/subscribers", handleRepoSubscribers(d))

	// Watching (repository subscriptions), on the authenticated actor.
	r.Get("/user/subscriptions", handleActorSubscriptionsList(d))
	r.Get("/users/{username}/subscriptions", handleUserSubscriptionsList(d))
	r.Get("/repos/{owner}/{repo}/subscription", handleRepoSubscriptionGet(d))
	r.Put("/repos/{owner}/{repo}/subscription", requireScope(handleRepoSubscriptionPut(d), "repo", "public_repo"))
	r.Delete("/repos/{owner}/{repo}/subscription", requireScope(handleRepoSubscriptionDelete(d), "repo", "public_repo"))

	// Follows.
	r.Get("/user/followers", handleActorFollowersList(d))
	r.Get("/user/following", handleActorFollowingList(d))
	r.Get("/user/following/{username}", handleActorFollowingCheck(d))
	r.Put("/user/following/{username}", requireScope(handleFollow(d), "user", "user:follow"))
	r.Delete("/user/following/{username}", requireScope(handleUnfollow(d), "user", "user:follow"))
	r.Get("/users/{username}/followers", handleUserFollowersList(d))
	r.Get("/users/{username}/following", handleUserFollowingList(d))
	r.Get("/users/{username}/following/{target}", handleUserFollowsCheck(d))
}

// --- stars ---

// handleActorStarredList serves GET /user/starred.
func handleActorStarredList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		repos, err := d.Social.StarredByActor(ctx, actor.UserID)
		if err != nil {
			return err
		}
		return writeRepoListPage(c, d, actor, repos)
	}
}

// handleUserStarredList serves GET /users/{username}/starred.
func handleUserStarredList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		repos, err := d.Social.StarredByLogin(ctx, actor.UserID, c.Param("username"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		return writeRepoListPage(c, d, actor, repos)
	}
}

// handleRepoStargazers serves GET /repos/{owner}/{repo}/stargazers (and the
// legacy /watchers alias).
func handleRepoStargazers(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		users, err := d.Social.Stargazers(c.Request().Context(), repo.Owner.Login, repo.Name)
		if err != nil {
			return err
		}
		return writeUserListPage(c, d, users)
	}
}

// handleActorStarredCheck serves GET /user/starred/{owner}/{repo}: 204 when the
// actor has starred the repository, 404 otherwise.
func handleActorStarredCheck(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		starred, err := d.Social.IsStarred(ctx, actor.UserID, c.Param("owner"), c.Param("repo"))
		if errors.Is(err, domain.ErrRepoNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		if !starred {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleStar serves PUT /user/starred/{owner}/{repo}: 204 on success, idempotent.
func handleStar(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		err := d.Social.StarRepo(ctx, actor.UserID, c.Param("owner"), c.Param("repo"))
		if errors.Is(err, domain.ErrRepoNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleUnstar serves DELETE /user/starred/{owner}/{repo}: 204, idempotent.
func handleUnstar(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		err := d.Social.UnstarRepo(ctx, actor.UserID, c.Param("owner"), c.Param("repo"))
		if errors.Is(err, domain.ErrRepoNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// --- watching (subscriptions) ---

// handleActorSubscriptionsList serves GET /user/subscriptions.
func handleActorSubscriptionsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		repos, err := d.Social.SubscriptionsByActor(ctx, actor.UserID)
		if err != nil {
			return err
		}
		return writeRepoListPage(c, d, actor, repos)
	}
}

// handleUserSubscriptionsList serves GET /users/{username}/subscriptions.
func handleUserSubscriptionsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		repos, err := d.Social.SubscriptionsByLogin(ctx, actor.UserID, c.Param("username"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		return writeRepoListPage(c, d, actor, repos)
	}
}

// handleRepoSubscribers serves GET /repos/{owner}/{repo}/subscribers, the users
// watching the repository.
func handleRepoSubscribers(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		users, err := d.Social.Watchers(c.Request().Context(), repo.Owner.Login, repo.Name)
		if err != nil {
			return err
		}
		return writeUserListPage(c, d, users)
	}
}

// handleRepoSubscriptionGet serves GET /repos/{owner}/{repo}/subscription: the
// actor's subscription, or 404 when the actor watches via no explicit row.
func handleRepoSubscriptionGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		sub, err := d.Social.Subscription(ctx, actor.UserID, repo.Owner.Login, repo.Name)
		if errors.Is(err, domain.ErrNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.RepoSubscription(sub))
		return nil
	}
}

// handleRepoSubscriptionPut serves PUT /repos/{owner}/{repo}/subscription. The
// body's subscribed and ignored flags set the whole subscription; an absent
// body subscribes, the default GitHub applies.
func handleRepoSubscriptionPut(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		body := struct {
			Subscribed *bool `json:"subscribed"`
			Ignored    bool  `json:"ignored"`
		}{}
		if !decodeJSONOpt(c, &body) {
			return nil
		}
		// A missing subscribed flag defaults to true, the way GitHub treats a
		// PUT with only ignored set: ignoring still records a subscription row.
		subscribed := true
		if body.Subscribed != nil {
			subscribed = *body.Subscribed
		}
		sub, err := d.Social.SetSubscription(ctx, actor.UserID, repo.Owner.Login, repo.Name, subscribed, body.Ignored)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.RepoSubscription(sub))
		return nil
	}
}

// handleRepoSubscriptionDelete serves DELETE /repos/{owner}/{repo}/subscription:
// 204, idempotent.
func handleRepoSubscriptionDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		if err := d.Social.DeleteSubscription(ctx, actor.UserID, repo.Owner.Login, repo.Name); err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// --- follows ---

// handleActorFollowersList serves GET /user/followers.
func handleActorFollowersList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		users, err := d.Social.FollowersOfActor(ctx, actor.UserID)
		if err != nil {
			return err
		}
		return writeUserListPage(c, d, users)
	}
}

// handleActorFollowingList serves GET /user/following.
func handleActorFollowingList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		users, err := d.Social.FollowingOfActor(ctx, actor.UserID)
		if err != nil {
			return err
		}
		return writeUserListPage(c, d, users)
	}
}

// handleActorFollowingCheck serves GET /user/following/{username}: 204 when the
// actor follows the named user, 404 otherwise.
func handleActorFollowingCheck(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		following, err := d.Social.ActorFollows(ctx, actor.UserID, c.Param("username"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		if !following {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleFollow serves PUT /user/following/{username}: 204, idempotent.
func handleFollow(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		err := d.Social.Follow(ctx, actor.UserID, c.Param("username"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if errors.Is(err, domain.ErrForbidden) {
			// Following oneself: GitHub rejects with 422.
			writeError(c.Writer(), errUnprocessable("You cannot follow yourself."))
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleUnfollow serves DELETE /user/following/{username}: 204, idempotent.
func handleUnfollow(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		err := d.Social.Unfollow(ctx, actor.UserID, c.Param("username"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleUserFollowersList serves GET /users/{username}/followers.
func handleUserFollowersList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		users, err := d.Social.FollowersOfLogin(c.Request().Context(), c.Param("username"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		return writeUserListPage(c, d, users)
	}
}

// handleUserFollowingList serves GET /users/{username}/following.
func handleUserFollowingList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		users, err := d.Social.FollowingOfLogin(c.Request().Context(), c.Param("username"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		return writeUserListPage(c, d, users)
	}
}

// handleUserFollowsCheck serves GET /users/{username}/following/{target}: 204
// when username follows target, 404 otherwise.
func handleUserFollowsCheck(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		following, err := d.Social.UserFollows(c.Request().Context(), c.Param("username"), c.Param("target"))
		if errors.Is(err, domain.ErrUserNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		if !following {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// --- shared rendering ---

// writeUserListPage pages a user list and writes it as SimpleUser objects with
// the matching Link header.
func writeUserListPage(c *mizu.Ctx, d Deps, users []*domain.User) error {
	page, perr := parsePageFor(c, "User")
	if perr != nil {
		writeError(c.Writer(), perr)
		return nil
	}
	users = paginateSlice(&page, users)
	out := make([]any, 0, len(users))
	for _, u := range users {
		out = append(out, d.URLs.SimpleUser(u, d.NodeFormat))
	}
	writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
	writeJSON(c.Writer(), http.StatusOK, out)
	return nil
}

// writeRepoListPage pages a repository list and writes it as Repository objects
// with the matching Link header. The actor's permission block rides on each
// repository, the same shape GET /user/repos returns.
func writeRepoListPage(c *mizu.Ctx, d Deps, actor *auth.Actor, repos []*domain.Repo) error {
	page, perr := parsePageFor(c, "Repository")
	if perr != nil {
		writeError(c.Writer(), perr)
		return nil
	}
	repos = paginateSlice(&page, repos)
	ctx := c.Request().Context()
	out := make([]any, 0, len(repos))
	for _, r := range repos {
		perm, err := repoPermissions(ctx, d, actor, r)
		if err != nil {
			return err
		}
		out = append(out, d.URLs.Repository(r, d.NodeFormat, perm))
	}
	writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
	writeJSON(c.Writer(), http.StatusOK, out)
	return nil
}

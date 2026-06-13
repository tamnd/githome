package domain

import (
	"context"
	"errors"
	"time"

	"github.com/tamnd/githome/store"
)

// social_service.go backs GitHub's social graph: starring a repository,
// watching (subscribing to) a repository, and following a user. The three sit
// together because they share a shape: a viewer-scoped relationship that a PUT
// creates idempotently and a DELETE removes idempotently, plus listings that
// page. Writes that touch /user/* are always the authenticated actor's own, so
// the service takes the actor pk and never trusts a login for a write target.
// Repository listings (starred, subscriptions) filter by the viewer's
// visibility the way the rest of the repository lists do; user listings
// (stargazers, watchers, followers, following) are public. See
// 2001/review/01 R01-27.

// SocialStore is the slice of the store the SocialService depends on.
type SocialStore interface {
	UserByLogin(ctx context.Context, login string) (*store.UserRow, error)
	UserByPK(ctx context.Context, pk int64) (*store.UserRow, error)
	RepoByOwnerName(ctx context.Context, owner, name string) (*store.RepoRow, error)
	CollaboratorByRepo(ctx context.Context, repoPK, userPK int64) (*store.CollaboratorRow, error)

	InsertStar(ctx context.Context, userPK, repoPK int64) error
	DeleteStar(ctx context.Context, userPK, repoPK int64) error
	IsStarred(ctx context.Context, userPK, repoPK int64) (bool, error)
	StarCount(ctx context.Context, repoPK int64) (int, error)
	StargazersByRepo(ctx context.Context, repoPK int64, limit, offset int) ([]*store.UserRow, error)
	StarredByUser(ctx context.Context, userPK int64, limit, offset int) ([]*store.RepoRow, error)

	UpsertSubscription(ctx context.Context, userPK, repoPK int64, subscribed, ignored bool) error
	DeleteSubscription(ctx context.Context, userPK, repoPK int64) error
	SubscriptionByRepo(ctx context.Context, userPK, repoPK int64) (*store.SubscriptionRow, error)
	WatcherCount(ctx context.Context, repoPK int64) (int, error)
	WatchersByRepo(ctx context.Context, repoPK int64, limit, offset int) ([]*store.UserRow, error)
	SubscriptionsByUser(ctx context.Context, userPK int64, limit, offset int) ([]*store.RepoRow, error)

	InsertFollow(ctx context.Context, followerPK, targetPK int64) error
	DeleteFollow(ctx context.Context, followerPK, targetPK int64) error
	IsFollowing(ctx context.Context, followerPK, targetPK int64) (bool, error)
	FollowerCount(ctx context.Context, targetPK int64) (int, error)
	FollowingCount(ctx context.Context, followerPK int64) (int, error)
	FollowersByUser(ctx context.Context, targetPK int64, limit, offset int) ([]*store.UserRow, error)
	FollowingByUser(ctx context.Context, followerPK int64, limit, offset int) ([]*store.UserRow, error)
}

// SocialService manages stars, watches, and follows.
type SocialService struct {
	store SocialStore
}

// NewSocialService builds a SocialService over the store.
func NewSocialService(st SocialStore) *SocialService { return &SocialService{store: st} }

// Subscription is the domain view of a repository subscription, the input for
// the REST subscription model.
type Subscription struct {
	Subscribed bool
	Ignored    bool
	CreatedAt  time.Time
	RepoOwner  string
	RepoName   string
}

// --- stars ---

// StarRepo records that the actor starred owner/name. A repeated star is a
// no-op, matching the idempotent PUT.
func (s *SocialService) StarRepo(ctx context.Context, actorPK int64, owner, name string) error {
	repo, err := s.repoByName(ctx, owner, name)
	if err != nil {
		return err
	}
	return s.store.InsertStar(ctx, actorPK, repo.PK)
}

// UnstarRepo removes the actor's star on owner/name. Removing an absent star is
// a no-op.
func (s *SocialService) UnstarRepo(ctx context.Context, actorPK int64, owner, name string) error {
	repo, err := s.repoByName(ctx, owner, name)
	if err != nil {
		return err
	}
	return s.store.DeleteStar(ctx, actorPK, repo.PK)
}

// IsStarred reports whether the actor has starred owner/name.
func (s *SocialService) IsStarred(ctx context.Context, actorPK int64, owner, name string) (bool, error) {
	repo, err := s.repoByName(ctx, owner, name)
	if err != nil {
		return false, err
	}
	return s.store.IsStarred(ctx, actorPK, repo.PK)
}

// Stargazers lists the users who starred owner/name. The handler pages the
// returned slice.
func (s *SocialService) Stargazers(ctx context.Context, owner, name string) ([]*User, error) {
	repo, err := s.repoByName(ctx, owner, name)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.StargazersByRepo(ctx, repo.PK, 0, 0)
	if err != nil {
		return nil, err
	}
	return usersFromRows(rows), nil
}

// StarredByLogin lists the repositories the named user starred, filtered by the
// viewer's visibility. The handler pages the returned slice.
func (s *SocialService) StarredByLogin(ctx context.Context, viewerPK int64, login string) ([]*Repo, error) {
	target, err := s.userByLogin(ctx, login)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.StarredByUser(ctx, target.PK, 0, 0)
	if err != nil {
		return nil, err
	}
	return s.visibleRepos(ctx, viewerPK, rows)
}

// StarredByActor lists the repositories the authenticated actor starred. The
// actor sees their own private starred repositories.
func (s *SocialService) StarredByActor(ctx context.Context, actorPK int64) ([]*Repo, error) {
	rows, err := s.store.StarredByUser(ctx, actorPK, 0, 0)
	if err != nil {
		return nil, err
	}
	return s.visibleRepos(ctx, actorPK, rows)
}

// --- watching (subscriptions) ---

// Subscription returns the actor's subscription to owner/name, or ErrNotFound
// when the actor has set none.
func (s *SocialService) Subscription(ctx context.Context, actorPK int64, owner, name string) (*Subscription, error) {
	repo, err := s.repoByName(ctx, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.SubscriptionByRepo(ctx, actorPK, repo.PK)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &Subscription{Subscribed: row.Subscribed, Ignored: row.Ignored, CreatedAt: row.CreatedAt, RepoOwner: owner, RepoName: name}, nil
}

// SetSubscription sets the actor's subscription to owner/name, creating or
// replacing it, the way PUT /repos/{o}/{r}/subscription does.
func (s *SocialService) SetSubscription(ctx context.Context, actorPK int64, owner, name string, subscribed, ignored bool) (*Subscription, error) {
	repo, err := s.repoByName(ctx, owner, name)
	if err != nil {
		return nil, err
	}
	if err := s.store.UpsertSubscription(ctx, actorPK, repo.PK, subscribed, ignored); err != nil {
		return nil, err
	}
	// Read the row back so created_at reflects the stored timestamp, the value
	// GitHub returns on the subscription it just wrote.
	row, err := s.store.SubscriptionByRepo(ctx, actorPK, repo.PK)
	if err != nil {
		return nil, err
	}
	return &Subscription{Subscribed: row.Subscribed, Ignored: row.Ignored, CreatedAt: row.CreatedAt, RepoOwner: owner, RepoName: name}, nil
}

// DeleteSubscription removes the actor's subscription to owner/name. Removing an
// absent subscription is a no-op.
func (s *SocialService) DeleteSubscription(ctx context.Context, actorPK int64, owner, name string) error {
	repo, err := s.repoByName(ctx, owner, name)
	if err != nil {
		return err
	}
	return s.store.DeleteSubscription(ctx, actorPK, repo.PK)
}

// Watchers lists the users watching owner/name. The handler pages the returned
// slice.
func (s *SocialService) Watchers(ctx context.Context, owner, name string) ([]*User, error) {
	repo, err := s.repoByName(ctx, owner, name)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.WatchersByRepo(ctx, repo.PK, 0, 0)
	if err != nil {
		return nil, err
	}
	return usersFromRows(rows), nil
}

// SubscriptionsByLogin lists the repositories the named user watches, filtered
// by the viewer's visibility. The handler pages the returned slice.
func (s *SocialService) SubscriptionsByLogin(ctx context.Context, viewerPK int64, login string) ([]*Repo, error) {
	target, err := s.userByLogin(ctx, login)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.SubscriptionsByUser(ctx, target.PK, 0, 0)
	if err != nil {
		return nil, err
	}
	return s.visibleRepos(ctx, viewerPK, rows)
}

// SubscriptionsByActor lists the repositories the authenticated actor watches.
func (s *SocialService) SubscriptionsByActor(ctx context.Context, actorPK int64) ([]*Repo, error) {
	rows, err := s.store.SubscriptionsByUser(ctx, actorPK, 0, 0)
	if err != nil {
		return nil, err
	}
	return s.visibleRepos(ctx, actorPK, rows)
}

// --- follows ---

// Follow records that the actor follows the named user. Following oneself is
// rejected, matching GitHub, and a repeated follow is a no-op.
func (s *SocialService) Follow(ctx context.Context, actorPK int64, login string) error {
	target, err := s.userByLogin(ctx, login)
	if err != nil {
		return err
	}
	if target.PK == actorPK {
		return ErrForbidden
	}
	return s.store.InsertFollow(ctx, actorPK, target.PK)
}

// Unfollow removes the actor's follow of the named user. Removing an absent
// follow is a no-op.
func (s *SocialService) Unfollow(ctx context.Context, actorPK int64, login string) error {
	target, err := s.userByLogin(ctx, login)
	if err != nil {
		return err
	}
	return s.store.DeleteFollow(ctx, actorPK, target.PK)
}

// ActorFollows reports whether the actor follows the named user.
func (s *SocialService) ActorFollows(ctx context.Context, actorPK int64, login string) (bool, error) {
	target, err := s.userByLogin(ctx, login)
	if err != nil {
		return false, err
	}
	return s.store.IsFollowing(ctx, actorPK, target.PK)
}

// UserFollows reports whether the named follower follows the named target. It
// backs GET /users/{u}/following/{target}.
func (s *SocialService) UserFollows(ctx context.Context, followerLogin, targetLogin string) (bool, error) {
	follower, err := s.userByLogin(ctx, followerLogin)
	if err != nil {
		return false, err
	}
	target, err := s.userByLogin(ctx, targetLogin)
	if err != nil {
		return false, err
	}
	return s.store.IsFollowing(ctx, follower.PK, target.PK)
}

// FollowersOfLogin lists the users following the named user. The handler pages
// the returned slice.
func (s *SocialService) FollowersOfLogin(ctx context.Context, login string) ([]*User, error) {
	target, err := s.userByLogin(ctx, login)
	if err != nil {
		return nil, err
	}
	return s.followersOf(ctx, target.PK)
}

// FollowersOfActor lists the users following the authenticated actor.
func (s *SocialService) FollowersOfActor(ctx context.Context, actorPK int64) ([]*User, error) {
	return s.followersOf(ctx, actorPK)
}

// FollowingOfLogin lists the users the named user follows. The handler pages the
// returned slice.
func (s *SocialService) FollowingOfLogin(ctx context.Context, login string) ([]*User, error) {
	target, err := s.userByLogin(ctx, login)
	if err != nil {
		return nil, err
	}
	return s.followingOf(ctx, target.PK)
}

// FollowingOfActor lists the users the authenticated actor follows.
func (s *SocialService) FollowingOfActor(ctx context.Context, actorPK int64) ([]*User, error) {
	return s.followingOf(ctx, actorPK)
}

func (s *SocialService) followersOf(ctx context.Context, targetPK int64) ([]*User, error) {
	rows, err := s.store.FollowersByUser(ctx, targetPK, 0, 0)
	if err != nil {
		return nil, err
	}
	return usersFromRows(rows), nil
}

func (s *SocialService) followingOf(ctx context.Context, followerPK int64) ([]*User, error) {
	rows, err := s.store.FollowingByUser(ctx, followerPK, 0, 0)
	if err != nil {
		return nil, err
	}
	return usersFromRows(rows), nil
}

// --- shared helpers ---

// repoByName resolves owner/name to a repository row, mapping the not-found
// store error to ErrRepoNotFound.
func (s *SocialService) repoByName(ctx context.Context, owner, name string) (*store.RepoRow, error) {
	row, err := s.store.RepoByOwnerName(ctx, owner, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrRepoNotFound
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

// userByLogin resolves a login to a user row, mapping the not-found store error
// to ErrUserNotFound.
func (s *SocialService) userByLogin(ctx context.Context, login string) (*store.UserRow, error) {
	row, err := s.store.UserByLogin(ctx, login)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

// visibleRepos keeps the rows the viewer can see and resolves each owner, the
// same visibility rule the cross-owner repository lists use: public to anyone,
// private only to the owner or a collaborator.
func (s *SocialService) visibleRepos(ctx context.Context, viewerPK int64, rows []*store.RepoRow) ([]*Repo, error) {
	out := make([]*Repo, 0, len(rows))
	for _, r := range rows {
		ok, err := s.viewerCanSee(ctx, r, viewerPK)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		ownerRow, err := s.store.UserByPK(ctx, r.OwnerPK)
		if err != nil {
			continue
		}
		out = append(out, repoFromRow(r, userFromRow(ownerRow)))
	}
	return out, nil
}

// viewerCanSee mirrors RepoService.viewerCanSee: a public repository is visible
// to anyone, a private one only to the owner or a collaborator.
func (s *SocialService) viewerCanSee(ctx context.Context, row *store.RepoRow, viewerPK int64) (bool, error) {
	if !row.Private || (viewerPK != 0 && row.OwnerPK == viewerPK) {
		return true, nil
	}
	if viewerPK == 0 {
		return false, nil
	}
	_, err := s.store.CollaboratorByRepo(ctx, row.PK, viewerPK)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// usersFromRows maps store user rows to domain users.
func usersFromRows(rows []*store.UserRow) []*User {
	out := make([]*User, 0, len(rows))
	for _, r := range rows {
		out = append(out, userFromRow(r))
	}
	return out
}

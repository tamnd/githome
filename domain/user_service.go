package domain

import (
	"context"
	"errors"

	"github.com/tamnd/githome/store"
)

// ErrUserNotFound is returned when no account matches the lookup.
var ErrUserNotFound = errors.New("domain: user not found")

// UserStore is the slice of the store the user service needs.
type UserStore interface {
	UserByPK(ctx context.Context, pk int64) (*store.UserRow, error)
	UserByLogin(ctx context.Context, login string) (*store.UserRow, error)
}

// UserService resolves accounts into domain users. The REST layer holds it and
// passes the authenticated actor's user id to Viewer for GET /user.
type UserService struct {
	store UserStore
}

// NewUserService builds a UserService over the store.
func NewUserService(st UserStore) *UserService { return &UserService{store: st} }

// Viewer resolves the authenticated user behind GET /user. The caller has
// already established that the actor is a user and passes its internal pk.
func (s *UserService) Viewer(ctx context.Context, userPK int64) (*User, error) {
	row, err := s.store.UserByPK(ctx, userPK)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return userFromRow(row), nil
}

// ByLogin resolves a public profile by login.
func (s *UserService) ByLogin(ctx context.Context, login string) (*User, error) {
	row, err := s.store.UserByLogin(ctx, login)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return userFromRow(row), nil
}

func userFromRow(r *store.UserRow) *User {
	return &User{
		ID:              r.DBID,
		Login:           r.Login,
		Type:            r.Type,
		SiteAdmin:       r.SiteAdmin,
		Name:            r.Name,
		Company:         r.Company,
		Blog:            r.Blog,
		Location:        r.Location,
		Email:           r.Email,
		Hireable:        r.Hireable,
		Bio:             r.Bio,
		TwitterUsername: r.TwitterUsername,
		PublicRepos:     r.PublicRepos,
		PublicGists:     r.PublicGists,
		Followers:       r.Followers,
		Following:       r.Following,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}
}

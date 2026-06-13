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
	UpdateProfile(ctx context.Context, userPK int64, u store.ProfileUpdate) error
	ListUsers(ctx context.Context, sinceDBID int64, limit int) ([]*store.UserRow, error)
}

// ProfileFields are the account profile fields the settings page can update.
type ProfileFields struct {
	Name            string
	Email           string
	Bio             string
	Location        string
	Company         string
	Blog            string
	TwitterUsername string
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

// PKByLogin returns the internal (store) primary key of the user with the given
// login, or ErrUserNotFound if no such user exists. This is used by REST handlers
// that need to call services accepting internal PKs.
func (s *UserService) PKByLogin(ctx context.Context, login string) (int64, error) {
	row, err := s.store.UserByLogin(ctx, login)
	if errors.Is(err, store.ErrNotFound) {
		return 0, ErrUserNotFound
	}
	if err != nil {
		return 0, err
	}
	return row.PK, nil
}

// ListUsers returns up to limit accounts with an id greater than since,
// ordered by id, backing GitHub's GET /users id-cursor pagination.
func (s *UserService) ListUsers(ctx context.Context, since int64, limit int) ([]*User, error) {
	rows, err := s.store.ListUsers(ctx, since, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*User, 0, len(rows))
	for _, r := range rows {
		out = append(out, userFromRow(r))
	}
	return out, nil
}

// UpdateProfile updates the authenticated viewer's profile fields. Only the
// fields named in fields are written; empty strings clear their column value.
func (s *UserService) UpdateProfile(ctx context.Context, viewerPK int64, fields ProfileFields) error {
	name := fields.Name
	email := fields.Email
	bio := fields.Bio
	loc := fields.Location
	co := fields.Company
	blog := fields.Blog
	tw := fields.TwitterUsername
	return s.store.UpdateProfile(ctx, viewerPK, store.ProfileUpdate{
		Name:            &name,
		Email:           &email,
		Bio:             &bio,
		Location:        &loc,
		Company:         &co,
		Blog:            &blog,
		TwitterUsername: &tw,
	})
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

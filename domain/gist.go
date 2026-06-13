package domain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/tamnd/githome/store"
)

// ErrGistNotFound is returned when a gist cannot be found.
var ErrGistNotFound = errors.New("domain: gist not found")

// GistStore is the store slice the gist service needs.
type GistStore interface {
	UserByPK(ctx context.Context, pk int64) (*store.UserRow, error)
	UserByLogin(ctx context.Context, login string) (*store.UserRow, error)
	InsertGist(ctx context.Context, g *store.GistRow) error
	GetGistByID(ctx context.Context, gistID string) (*store.GistRow, error)
	ListGistsByOwner(ctx context.Context, ownerPK int64, page, perPage int) ([]*store.GistRow, int, error)
	ListPublicGists(ctx context.Context, page, perPage int) ([]*store.GistRow, int, error)
	ListStarredGists(ctx context.Context, userPK int64, page, perPage int) ([]*store.GistRow, int, error)
	UpdateGist(ctx context.Context, gistPK int64, description *string, files map[string]*string) error
	DeleteGist(ctx context.Context, gistID string) error
	StarGist(ctx context.Context, gistPK, userPK int64) error
	UnstarGist(ctx context.Context, gistPK, userPK int64) error
	IsGistStarred(ctx context.Context, gistPK, userPK int64) (bool, error)
	InsertGistComment(ctx context.Context, c *store.GistCommentRow) error
	ListGistComments(ctx context.Context, gistPK int64) ([]store.GistCommentRow, error)
}

// GistService manages gists and their files.
type GistService struct {
	store GistStore
}

// NewGistService builds a GistService.
func NewGistService(st GistStore) *GistService {
	return &GistService{store: st}
}

// GistInput is the create payload for a gist.
type GistInput struct {
	Description string
	Public      bool
	Files       map[string]string // filename → content
}

// GistUpdateInput is the update payload for a gist.
type GistUpdateInput struct {
	Description *string
	Files       map[string]*GistFileUpdate // filename → change (nil = delete file)
}

// GistFileUpdate is one file entry in a gist update. Content replaces the
// file body; NewName renames the file, keeping its content unless Content
// is also given.
type GistFileUpdate struct {
	Content *string
	NewName *string
}

// CreateGist creates a new gist owned by userPK.
func (s *GistService) CreateGist(ctx context.Context, userPK int64, in GistInput) (*store.GistRow, error) {
	gistID, err := randGistID()
	if err != nil {
		return nil, err
	}
	g := &store.GistRow{
		GistID:      gistID,
		OwnerPK:     userPK,
		Description: in.Description,
		Public:      in.Public,
	}
	for fn, content := range in.Files {
		g.Files = append(g.Files, store.GistFileRow{Filename: fn, Content: content})
	}
	if err := s.store.InsertGist(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}

// GetGist loads a gist by its hex ID, checking visibility for callerPK.
// callerPK == 0 means anonymous.
func (s *GistService) GetGist(ctx context.Context, gistID string, callerPK int64) (*store.GistRow, error) {
	g, err := s.store.GetGistByID(ctx, gistID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrGistNotFound
	}
	if err != nil {
		return nil, err
	}
	if !g.Public && g.OwnerPK != callerPK {
		return nil, ErrGistNotFound
	}
	return g, nil
}

// ListUserGists returns gists for owner login. If callerPK != ownerPK,
// only public gists are returned.
func (s *GistService) ListUserGists(ctx context.Context, ownerLogin string, callerPK int64, page, perPage int) ([]*store.GistRow, int, error) {
	owner, err := s.store.UserByLogin(ctx, ownerLogin)
	if err != nil {
		return nil, 0, ErrUserNotFound
	}
	all, total, err := s.store.ListGistsByOwner(ctx, owner.PK, page, perPage)
	if err != nil {
		return nil, 0, err
	}
	if callerPK == owner.PK {
		return all, total, nil
	}
	var out []*store.GistRow
	for _, g := range all {
		if g.Public {
			out = append(out, g)
		}
	}
	return out, len(out), nil
}

// ListAuthUserGists returns all gists (public+private) for the authenticated user.
func (s *GistService) ListAuthUserGists(ctx context.Context, userPK int64, page, perPage int) ([]*store.GistRow, int, error) {
	return s.store.ListGistsByOwner(ctx, userPK, page, perPage)
}

// ListPublicGists returns public gists in update order.
func (s *GistService) ListPublicGists(ctx context.Context, page, perPage int) ([]*store.GistRow, int, error) {
	return s.store.ListPublicGists(ctx, page, perPage)
}

// ListStarredGists returns gists starred by userPK.
func (s *GistService) ListStarredGists(ctx context.Context, userPK int64, page, perPage int) ([]*store.GistRow, int, error) {
	return s.store.ListStarredGists(ctx, userPK, page, perPage)
}

// UpdateGist modifies a gist's description and/or files.
func (s *GistService) UpdateGist(ctx context.Context, gistID string, callerPK int64, in GistUpdateInput) (*store.GistRow, error) {
	g, err := s.store.GetGistByID(ctx, gistID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrGistNotFound
	}
	if err != nil {
		return nil, err
	}
	if g.OwnerPK != callerPK {
		return nil, ErrForbidden
	}
	existing := make(map[string]string, len(g.Files))
	for _, f := range g.Files {
		existing[f.Filename] = f.Content
	}
	files := make(map[string]*string, len(in.Files))
	for fn, f := range in.Files {
		if f == nil {
			files[fn] = nil
			continue
		}
		target := fn
		if f.NewName != nil && *f.NewName != "" {
			target = *f.NewName
		}
		content := f.Content
		if content == nil {
			old, ok := existing[fn]
			if !ok {
				return nil, ErrValidation
			}
			content = &old
		}
		if target != fn {
			files[fn] = nil
		}
		files[target] = content
	}
	if err := s.store.UpdateGist(ctx, g.PK, in.Description, files); err != nil {
		return nil, err
	}
	return s.store.GetGistByID(ctx, gistID)
}

// DeleteGist removes a gist owned by callerPK.
func (s *GistService) DeleteGist(ctx context.Context, gistID string, callerPK int64) error {
	g, err := s.store.GetGistByID(ctx, gistID)
	if errors.Is(err, store.ErrNotFound) {
		return ErrGistNotFound
	}
	if err != nil {
		return err
	}
	if g.OwnerPK != callerPK {
		return ErrForbidden
	}
	return s.store.DeleteGist(ctx, gistID)
}

// StarGist stars a gist for callerPK.
func (s *GistService) StarGist(ctx context.Context, gistID string, callerPK int64) error {
	g, err := s.store.GetGistByID(ctx, gistID)
	if errors.Is(err, store.ErrNotFound) {
		return ErrGistNotFound
	}
	if err != nil {
		return err
	}
	return s.store.StarGist(ctx, g.PK, callerPK)
}

// UnstarGist removes the star.
func (s *GistService) UnstarGist(ctx context.Context, gistID string, callerPK int64) error {
	g, err := s.store.GetGistByID(ctx, gistID)
	if errors.Is(err, store.ErrNotFound) {
		return ErrGistNotFound
	}
	if err != nil {
		return err
	}
	return s.store.UnstarGist(ctx, g.PK, callerPK)
}

// IsGistStarred reports whether callerPK has starred gistID.
func (s *GistService) IsGistStarred(ctx context.Context, gistID string, callerPK int64) (bool, error) {
	g, err := s.store.GetGistByID(ctx, gistID)
	if errors.Is(err, store.ErrNotFound) {
		return false, ErrGistNotFound
	}
	if err != nil {
		return false, err
	}
	return s.store.IsGistStarred(ctx, g.PK, callerPK)
}

// CreateGistComment adds a comment to a gist.
func (s *GistService) CreateGistComment(ctx context.Context, gistID string, callerPK int64, body string) (*store.GistCommentRow, error) {
	g, err := s.store.GetGistByID(ctx, gistID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrGistNotFound
	}
	if err != nil {
		return nil, err
	}
	if !g.Public && g.OwnerPK != callerPK {
		return nil, ErrGistNotFound
	}
	c := &store.GistCommentRow{GistPK: g.PK, UserPK: callerPK, Body: body}
	if err := s.store.InsertGistComment(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// ListGistComments returns all comments for a gist.
func (s *GistService) ListGistComments(ctx context.Context, gistID string, callerPK int64) ([]store.GistCommentRow, error) {
	g, err := s.GetGist(ctx, gistID, callerPK)
	if err != nil {
		return nil, err
	}
	return s.store.ListGistComments(ctx, g.PK)
}

// randGistID generates a 20-byte random hex string matching GitHub's gist ID format.
func randGistID() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

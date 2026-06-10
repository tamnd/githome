package auth

// pat.go holds the personal access token lifecycle the settings tokens page
// drives: mint a classic ghp_ token, list a user's live tokens, and delete
// one. The plaintext exists only in the CreatePAT return value; the store only
// ever sees the sha256 and the last eight characters for display.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tamnd/githome/store"
)

// ErrPATNotFound reports a delete aimed at a token the user does not have.
var ErrPATNotFound = errors.New("auth: personal access token not found")

// PATInfo is the displayable summary of a personal access token: everything
// the settings page shows, nothing that authenticates.
type PATInfo struct {
	ID         int64
	Note       string
	Scopes     string // header form, e.g. "gist, repo"
	LastEight  string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// CreatePAT mints a classic personal access token for userPK with the given
// note and scopes, persists its hash, and returns the one-time plaintext.
// Unknown scopes are dropped and implied children folded into their parent,
// the same normalization every other mint path applies.
func (s *Service) CreatePAT(ctx context.Context, userPK int64, note string, scopes []string) (string, error) {
	g, err := GenerateToken(PrefixClassicPAT)
	if err != nil {
		return "", err
	}
	in := make(Scopes, 0, len(scopes))
	for _, sc := range scopes {
		in = append(in, Scope(sc))
	}
	hash := g.Hash
	row := &store.TokenRow{
		UserPK:      &userPK,
		TokenHash:   hash[:],
		TokenPrefix: g.Prefix,
		LastEight:   g.Last8,
		Kind:        "pat",
		Scopes:      NormalizeScopes(in).Header(),
		Note:        note,
	}
	if err := s.store.InsertToken(ctx, row); err != nil {
		return "", err
	}
	return g.Plaintext, nil
}

// ListPATs returns the user's live personal access tokens, newest first.
func (s *Service) ListPATs(ctx context.Context, userPK int64) ([]PATInfo, error) {
	rows, err := s.store.TokensForUser(ctx, userPK)
	if err != nil {
		return nil, err
	}
	out := make([]PATInfo, 0, len(rows))
	for _, t := range rows {
		out = append(out, PATInfo{
			ID:         t.PK,
			Note:       t.Note,
			Scopes:     strings.TrimSpace(t.Scopes),
			LastEight:  t.LastEight,
			CreatedAt:  t.CreatedAt,
			LastUsedAt: t.LastUsedAt,
		})
	}
	return out, nil
}

// DeletePAT removes one of the user's personal access tokens. A pk the user
// does not own answers ErrPATNotFound, indistinguishable from one that never
// existed.
func (s *Service) DeletePAT(ctx context.Context, userPK, id int64) error {
	err := s.store.DeleteUserToken(ctx, id, userPK)
	if errors.Is(err, store.ErrNotFound) {
		return ErrPATNotFound
	}
	return err
}

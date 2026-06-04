package domain

import (
	"context"
	"errors"
	"strings"

	"github.com/tamnd/githome/store"
)

// LabelInput is the create/update payload for a label.
type LabelInput struct {
	Name        string
	Color       string
	Description *string
}

// ListLabels returns a repository's labels in name order.
func (s *IssueService) ListLabels(ctx context.Context, viewerPK int64, owner, name string) ([]*Label, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListLabels(ctx, repo.PK)
	if err != nil {
		return nil, err
	}
	return labelsFromRows(rows), nil
}

// GetLabel resolves a single label by name.
func (s *IssueService) GetLabel(ctx context.Context, viewerPK int64, owner, name, label string) (*Label, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetLabel(ctx, repo.PK, label)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrLabelNotFound
	}
	if err != nil {
		return nil, err
	}
	return labelFromRow(row), nil
}

// CreateLabel adds a label to a repository. Write access is required. A name
// that already exists (case-insensitively) is a conflict.
func (s *IssueService) CreateLabel(ctx context.Context, actorPK int64, owner, name string, in LabelInput) (*Label, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, ErrValidation
	}
	if _, err := s.store.GetLabel(ctx, repo.PK, in.Name); err == nil {
		return nil, ErrLabelExists
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	row := &store.LabelRow{
		RepoPK:      repo.PK,
		Name:        in.Name,
		Color:       normalizeColor(in.Color),
		Description: in.Description,
	}
	if err := s.store.InsertLabel(ctx, row); err != nil {
		return nil, err
	}
	return labelFromRow(row), nil
}

// UpdateLabel edits a label resolved by its current name. A rename onto another
// existing label's name is a conflict.
func (s *IssueService) UpdateLabel(ctx context.Context, actorPK int64, owner, name, current string, in LabelInput) (*Label, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetLabel(ctx, repo.PK, current)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrLabelNotFound
	}
	if err != nil {
		return nil, err
	}
	if in.Name != "" && !strings.EqualFold(in.Name, row.Name) {
		if _, err := s.store.GetLabel(ctx, repo.PK, in.Name); err == nil {
			return nil, ErrLabelExists
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		row.Name = in.Name
	}
	if in.Color != "" {
		row.Color = normalizeColor(in.Color)
	}
	if in.Description != nil {
		row.Description = in.Description
	}
	if err := s.store.UpdateLabel(ctx, row); err != nil {
		return nil, err
	}
	return labelFromRow(row), nil
}

// DeleteLabel removes a label by name.
func (s *IssueService) DeleteLabel(ctx context.Context, actorPK int64, owner, name, label string) error {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return err
	}
	row, err := s.store.GetLabel(ctx, repo.PK, label)
	if errors.Is(err, store.ErrNotFound) {
		return ErrLabelNotFound
	}
	if err != nil {
		return err
	}
	return s.store.DeleteLabel(ctx, row.PK)
}

// normalizeColor strips a leading hash and lowercases a label color, leaving an
// empty value for the store's default.
func normalizeColor(c string) string {
	return strings.ToLower(strings.TrimPrefix(c, "#"))
}

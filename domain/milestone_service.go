package domain

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tamnd/githome/store"
)

// MilestoneInput is the create/update payload for a milestone. A nil field on
// update leaves the value unchanged; State moves between open and closed.
type MilestoneInput struct {
	Title       *string
	Description *string
	State       *string
	DueOn       *time.Time
	ClearDueOn  bool
}

// ListMilestones returns a repository's milestones filtered by state
// ("open"|"closed"|"all").
func (s *IssueService) ListMilestones(ctx context.Context, viewerPK int64, owner, name, state string) ([]*Milestone, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListMilestones(ctx, repo.PK, state)
	if err != nil {
		return nil, err
	}
	out := make([]*Milestone, 0, len(rows))
	for i := range rows {
		m, err := s.assembleMilestone(ctx, &rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// GetMilestone resolves a single milestone by number.
func (s *IssueService) GetMilestone(ctx context.Context, viewerPK int64, owner, name string, number int64) (*Milestone, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetMilestoneByNumber(ctx, repo.PK, number)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrMilestoneNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.assembleMilestone(ctx, row)
}

// CreateMilestone adds a milestone to a repository. Write access is required.
func (s *IssueService) CreateMilestone(ctx context.Context, actorPK int64, owner, name string, in MilestoneInput) (*Milestone, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	if in.Title == nil || strings.TrimSpace(*in.Title) == "" {
		return nil, ErrValidation
	}
	state := "open"
	if in.State != nil {
		if *in.State != "open" && *in.State != "closed" {
			return nil, ErrValidation
		}
		state = *in.State
	}
	row := &store.MilestoneRow{
		RepoPK:      repo.PK,
		Title:       strings.TrimSpace(*in.Title),
		Description: in.Description,
		State:       state,
		DueOn:       in.DueOn,
		CreatorPK:   &actorPK,
	}
	if state == "closed" {
		now := nowUTC()
		row.ClosedAt = &now
	}
	if err := s.store.InsertMilestone(ctx, row); err != nil {
		return nil, err
	}
	return s.assembleMilestone(ctx, row)
}

// UpdateMilestone edits a milestone resolved by number, including its open and
// closed state transition.
func (s *IssueService) UpdateMilestone(ctx context.Context, actorPK int64, owner, name string, number int64, in MilestoneInput) (*Milestone, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetMilestoneByNumber(ctx, repo.PK, number)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrMilestoneNotFound
	}
	if err != nil {
		return nil, err
	}
	if in.Title != nil {
		if strings.TrimSpace(*in.Title) == "" {
			return nil, ErrValidation
		}
		row.Title = strings.TrimSpace(*in.Title)
	}
	if in.Description != nil {
		row.Description = in.Description
	}
	if in.ClearDueOn {
		row.DueOn = nil
	} else if in.DueOn != nil {
		row.DueOn = in.DueOn
	}
	if in.State != nil && *in.State != row.State {
		switch *in.State {
		case "closed":
			row.State = "closed"
			now := nowUTC()
			row.ClosedAt = &now
		case "open":
			row.State = "open"
			row.ClosedAt = nil
		default:
			return nil, ErrValidation
		}
	}
	if err := s.store.UpdateMilestone(ctx, row); err != nil {
		return nil, err
	}
	return s.assembleMilestone(ctx, row)
}

// DeleteMilestone removes a milestone by number; issues pointing at it have
// their milestone cleared by the store's foreign key.
func (s *IssueService) DeleteMilestone(ctx context.Context, actorPK int64, owner, name string, number int64) error {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return err
	}
	row, err := s.store.GetMilestoneByNumber(ctx, repo.PK, number)
	if errors.Is(err, store.ErrNotFound) {
		return ErrMilestoneNotFound
	}
	if err != nil {
		return err
	}
	return s.store.DeleteMilestone(ctx, row.PK)
}

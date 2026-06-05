package domain

import (
	"context"
	"errors"

	"github.com/tamnd/githome/store"
)

// CreateIssueReaction adds the actor's reaction to an issue. The content must be
// one of GitHub's eight reaction names. Reacting twice with the same content is
// idempotent: the existing reaction comes back rather than a duplicate.
func (s *IssueService) CreateIssueReaction(ctx context.Context, actorPK int64, owner, name string, number int64, content string) (*Reaction, error) {
	repo, err := s.repos.GetRepo(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	if actorPK == 0 {
		return nil, ErrForbidden
	}
	if !reactionContents[content] {
		return nil, ErrValidation
	}
	row, err := s.store.GetIssueByNumber(ctx, repo.PK, number)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrIssueNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.addReaction(ctx, actorPK, "issue", row.PK, content)
}

// ListIssueReactions returns an issue's reactions, oldest first.
func (s *IssueService) ListIssueReactions(ctx context.Context, viewerPK int64, owner, name string, number int64) ([]*Reaction, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetIssueByNumber(ctx, repo.PK, number)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrIssueNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.listReactions(ctx, "issue", row.PK)
}

// DeleteIssueReaction removes a reaction from an issue by its public id.
func (s *IssueService) DeleteIssueReaction(ctx context.Context, actorPK int64, owner, name string, number, reactionID int64) error {
	repo, err := s.repos.GetRepo(ctx, actorPK, owner, name)
	if err != nil {
		return err
	}
	if actorPK == 0 {
		return ErrForbidden
	}
	row, err := s.store.GetIssueByNumber(ctx, repo.PK, number)
	if errors.Is(err, store.ErrNotFound) {
		return ErrIssueNotFound
	}
	if err != nil {
		return err
	}
	return s.store.DeleteReaction(ctx, "issue", row.PK, reactionID)
}

// CreateCommentReaction adds the actor's reaction to an issue comment.
func (s *IssueService) CreateCommentReaction(ctx context.Context, actorPK int64, owner, name string, commentID int64, content string) (*Reaction, error) {
	repo, err := s.repos.GetRepo(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	if actorPK == 0 {
		return nil, ErrForbidden
	}
	if !reactionContents[content] {
		return nil, ErrValidation
	}
	row, err := s.commentInRepo(ctx, repo, commentID)
	if err != nil {
		return nil, err
	}
	return s.addReaction(ctx, actorPK, "comment", row.PK, content)
}

// ListCommentReactions returns a comment's reactions, oldest first.
func (s *IssueService) ListCommentReactions(ctx context.Context, viewerPK int64, owner, name string, commentID int64) ([]*Reaction, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.commentInRepo(ctx, repo, commentID)
	if err != nil {
		return nil, err
	}
	return s.listReactions(ctx, "comment", row.PK)
}

// DeleteCommentReaction removes a reaction from a comment by its public id.
func (s *IssueService) DeleteCommentReaction(ctx context.Context, actorPK int64, owner, name string, commentID, reactionID int64) error {
	repo, err := s.repos.GetRepo(ctx, actorPK, owner, name)
	if err != nil {
		return err
	}
	if actorPK == 0 {
		return ErrForbidden
	}
	row, err := s.commentInRepo(ctx, repo, commentID)
	if err != nil {
		return err
	}
	return s.store.DeleteReaction(ctx, "comment", row.PK, reactionID)
}

func (s *IssueService) addReaction(ctx context.Context, actorPK int64, subjectType string, subjectPK int64, content string) (*Reaction, error) {
	r := &store.ReactionRow{SubjectType: subjectType, SubjectPK: subjectPK, UserPK: actorPK, Content: content}
	if _, err := s.store.InsertReaction(ctx, r); err != nil {
		return nil, err
	}
	user, err := s.userByPK(ctx, actorPK)
	if err != nil {
		return nil, err
	}
	return &Reaction{ID: r.DBID, User: user, Content: r.Content, CreatedAt: r.CreatedAt}, nil
}

func (s *IssueService) listReactions(ctx context.Context, subjectType string, subjectPK int64) ([]*Reaction, error) {
	rows, err := s.store.ListReactions(ctx, subjectType, subjectPK)
	if err != nil {
		return nil, err
	}
	out := make([]*Reaction, 0, len(rows))
	for _, r := range rows {
		user, err := s.userByPK(ctx, r.UserPK)
		if err != nil {
			return nil, err
		}
		out = append(out, &Reaction{ID: r.DBID, User: user, Content: r.Content, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

package domain

import (
	"context"
	"errors"
	"strings"

	"github.com/tamnd/githome/store"
)

// CreateComment adds a comment to an issue. Any authenticated viewer who can see
// the repository may comment; the post-receive write rule does not apply, so a
// reader of a public repository can join the discussion. The comment bumps the
// issue's cached count in the same transaction the insert runs in.
func (s *IssueService) CreateComment(ctx context.Context, actorPK int64, owner, name string, number int64, body string) (*Comment, error) {
	repo, err := s.repos.GetRepo(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	if actorPK == 0 {
		return nil, ErrForbidden
	}
	if strings.TrimSpace(body) == "" {
		return nil, ErrValidation
	}
	row, err := s.store.GetIssueByNumber(ctx, repo.PK, number)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrIssueNotFound
	}
	if err != nil {
		return nil, err
	}

	c := &store.CommentRow{IssuePK: row.PK, UserPK: actorPK, Body: body}
	if err := s.store.WithTx(ctx, func(tx *store.Tx) error {
		return tx.InsertComment(ctx, c)
	}); err != nil {
		return nil, err
	}
	s.enqueueIssueEvent(ctx, "commented", repo, row.Number)
	return s.assembleComment(ctx, c)
}

// ListComments returns a page of an issue's comments, oldest first.
func (s *IssueService) ListComments(ctx context.Context, viewerPK int64, owner, name string, number, page, perPage int64) ([]*Comment, error) {
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
	rows, err := s.store.ListIssueComments(ctx, row.PK, int(perPage), offsetFor(int(page), int(perPage)))
	if err != nil {
		return nil, err
	}
	out := make([]*Comment, 0, len(rows))
	for i := range rows {
		c, err := s.assembleComment(ctx, &rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// GetComment resolves a single comment by its public id, gating on the
// repository the comment's issue belongs to being visible to the viewer.
func (s *IssueService) GetComment(ctx context.Context, viewerPK int64, owner, name string, commentID int64) (*Comment, error) {
	repo, err := s.repos.GetRepo(ctx, viewerPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.commentInRepo(ctx, repo, commentID)
	if err != nil {
		return nil, err
	}
	return s.assembleComment(ctx, row)
}

// EditComment changes a comment's body. The author or a user with write access
// to the repository may edit.
func (s *IssueService) EditComment(ctx context.Context, actorPK int64, owner, name string, commentID int64, body string) (*Comment, error) {
	repo, err := s.repos.GetRepo(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.commentInRepo(ctx, repo, commentID)
	if err != nil {
		return nil, err
	}
	if !s.canModifyComment(repo, actorPK, row) {
		return nil, ErrForbidden
	}
	if strings.TrimSpace(body) == "" {
		return nil, ErrValidation
	}
	row.Body = body
	if err := s.store.UpdateComment(ctx, row); err != nil {
		return nil, err
	}
	return s.assembleComment(ctx, row)
}

// DeleteComment removes a comment. The author or a user with write access may
// delete; the issue's cached count is decremented in the store.
func (s *IssueService) DeleteComment(ctx context.Context, actorPK int64, owner, name string, commentID int64) error {
	repo, err := s.repos.GetRepo(ctx, actorPK, owner, name)
	if err != nil {
		return err
	}
	row, err := s.commentInRepo(ctx, repo, commentID)
	if err != nil {
		return err
	}
	if !s.canModifyComment(repo, actorPK, row) {
		return ErrForbidden
	}
	return s.store.DeleteComment(ctx, row.PK)
}

// commentInRepo resolves a comment by id and confirms it belongs to an issue in
// the given repository, so a comment id cannot be read across repositories.
func (s *IssueService) commentInRepo(ctx context.Context, repo *Repo, commentID int64) (*store.CommentRow, error) {
	row, err := s.store.GetComment(ctx, commentID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrCommentNotFound
	}
	if err != nil {
		return nil, err
	}
	iss, err := s.store.GetIssueByPK(ctx, row.IssuePK)
	if err != nil || iss.RepoPK != repo.PK {
		return nil, ErrCommentNotFound
	}
	return row, nil
}

// canModifyComment reports whether the actor may edit or delete the comment:
// its author (comments.user_pk is the author's internal pk, compared directly
// against the actor's), or a user with write access to the repository.
func (s *IssueService) canModifyComment(repo *Repo, actorPK int64, row *store.CommentRow) bool {
	if actorPK == 0 {
		return false
	}
	return row.UserPK == actorPK || canWrite(repo, actorPK)
}

func (s *IssueService) assembleComment(ctx context.Context, row *store.CommentRow) (*Comment, error) {
	author, err := s.userByPK(ctx, row.UserPK)
	if err != nil {
		return nil, err
	}
	roll, err := s.store.ReactionRollupFor(ctx, "comment", row.PK)
	if err != nil {
		return nil, err
	}
	return &Comment{
		ID:        row.DBID,
		IssuePK:   row.IssuePK,
		User:      author,
		Body:      row.Body,
		Reactions: rollup(roll),
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}, nil
}

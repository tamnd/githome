package domain

import (
	"context"

	"github.com/tamnd/githome/store"
)

// batcherStore is the subset of the store the Batcher needs. It matches the
// methods on *store.Store that batch-load rows by primary key.
type batcherStore interface {
	UsersByPKs(ctx context.Context, pks []int64) (map[int64]*store.UserRow, error)
	LabelsByIssuePKs(ctx context.Context, pks []int64) (map[int64][]store.LabelRow, error)
	AssigneesByIssuePKs(ctx context.Context, pks []int64) (map[int64][]int64, error)
	MilestonesByPKs(ctx context.Context, pks []int64) (map[int64]*store.MilestoneRow, error)
	ReactionRollupsBySubjectPKs(ctx context.Context, subjectType string, pks []int64) (map[int64]store.ReactionRollup, error)
	CommentsByIssuePKs(ctx context.Context, issuePKs []int64, perIssue int) (map[int64][]store.CommentRow, error)
}

// Batcher provides cross-domain batch loading for the GraphQL dataloaders. It
// is stateless; per-request memoization is handled by the dataloaders. The
// methods return domain types so the GraphQL layer never imports the store.
type Batcher struct {
	store batcherStore
}

// NewBatcher returns a Batcher backed by s.
func NewBatcher(s batcherStore) *Batcher { return &Batcher{store: s} }

// Users loads users by primary key and returns a map from PK to domain User.
// PKs that have no live row are absent from the map (ghost authors).
func (b *Batcher) Users(ctx context.Context, pks []int64) (map[int64]*User, error) {
	rows, err := b.store.UsersByPKs(ctx, pks)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]*User, len(rows))
	for pk, row := range rows {
		out[pk] = userFromRow(row)
	}
	return out, nil
}

// LabelsByIssues loads labels for the given issue PKs and returns a map from
// issue PK to the ordered slice of domain Labels.
func (b *Batcher) LabelsByIssues(ctx context.Context, issuePKs []int64) (map[int64][]*Label, error) {
	rows, err := b.store.LabelsByIssuePKs(ctx, issuePKs)
	if err != nil {
		return nil, err
	}
	out := make(map[int64][]*Label, len(rows))
	for pk, labRows := range rows {
		labels := make([]*Label, 0, len(labRows))
		for i := range labRows {
			labels = append(labels, labelFromRow(&labRows[i]))
		}
		out[pk] = labels
	}
	return out, nil
}

// CommentsPreviewByIssues loads the first limit comments of every listed
// issue, chronological within each, with authors and reaction rollups
// batch-loaded alongside. It backs the GraphQL comment-preview dataloader, so
// a 50-issue connection selecting comments(first: 5) per node costs three
// queries instead of fifty list calls each with its own author lookups. The
// returned comments carry a zero IssueNumber: the caller asked per issue and
// fills it in before rendering.
func (b *Batcher) CommentsPreviewByIssues(ctx context.Context, issuePKs []int64, limit int) (map[int64][]*Comment, error) {
	rowMap, err := b.store.CommentsByIssuePKs(ctx, issuePKs, limit)
	if err != nil {
		return nil, err
	}
	userPKSet := map[int64]struct{}{}
	var commentPKs []int64
	for _, rows := range rowMap {
		for i := range rows {
			userPKSet[rows[i].UserPK] = struct{}{}
			commentPKs = append(commentPKs, rows[i].PK)
		}
	}
	userPKs := make([]int64, 0, len(userPKSet))
	for pk := range userPKSet {
		userPKs = append(userPKs, pk)
	}
	userRows, err := b.store.UsersByPKs(ctx, userPKs)
	if err != nil {
		return nil, err
	}
	rollups, err := b.store.ReactionRollupsBySubjectPKs(ctx, "comment", commentPKs)
	if err != nil {
		return nil, err
	}
	out := make(map[int64][]*Comment, len(rowMap))
	for issuePK, rows := range rowMap {
		comments := make([]*Comment, 0, len(rows))
		for i := range rows {
			row := &rows[i]
			var author *User
			if u, ok := userRows[row.UserPK]; ok {
				author = userFromRow(u)
			}
			comments = append(comments, &Comment{
				ID:        row.DBID,
				IssuePK:   row.IssuePK,
				User:      author,
				Body:      row.Body,
				Reactions: rollup(rollups[row.PK]),
				CreatedAt: row.CreatedAt,
				UpdatedAt: row.UpdatedAt,
			})
		}
		out[issuePK] = comments
	}
	return out, nil
}

// AssigneesByIssues loads assignees for the given issue PKs. It resolves
// assignee user PKs in one additional batch load. Returns a map from issue PK
// to the ordered slice of domain Users.
func (b *Batcher) AssigneesByIssues(ctx context.Context, issuePKs []int64) (map[int64][]*User, error) {
	assigneeMap, err := b.store.AssigneesByIssuePKs(ctx, issuePKs)
	if err != nil {
		return nil, err
	}
	userPKSet := make(map[int64]struct{})
	for _, uPKs := range assigneeMap {
		for _, pk := range uPKs {
			userPKSet[pk] = struct{}{}
		}
	}
	userPKs := make([]int64, 0, len(userPKSet))
	for pk := range userPKSet {
		userPKs = append(userPKs, pk)
	}
	userRows, err := b.store.UsersByPKs(ctx, userPKs)
	if err != nil {
		return nil, err
	}
	out := make(map[int64][]*User, len(issuePKs))
	for issuePK, uPKs := range assigneeMap {
		users := make([]*User, 0, len(uPKs))
		for _, uPK := range uPKs {
			if row, ok := userRows[uPK]; ok {
				users = append(users, userFromRow(row))
			}
		}
		out[issuePK] = users
	}
	return out, nil
}

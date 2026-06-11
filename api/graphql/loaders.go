package graphql

import (
	"context"
	"net/http"
	"time"

	"github.com/tamnd/githome/api/graphql/dataloader"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/presenter/gqlmodel"
)

const loaderWait = time.Millisecond

type loadersKey struct{}

// commentsPreviewKey identifies one comment-preview load: the issue and how
// many leading comments the selection asked for. Keys with the same limit
// batch into one window-function query; mixed limits in one wave (rare; it
// takes two different first: arguments in one document) run one query each.
type commentsPreviewKey struct {
	IssuePK int64
	Limit   int
}

// Loaders holds the per-request batch loaders. One instance is created per
// HTTP request by loadersMiddleware and stored on the request context.
type Loaders struct {
	// Users loads a gqlmodel.User by user primary key.
	Users *dataloader.Loader[int64, *gqlmodel.User]
	// LabelsByIssue loads the label slice for an issue by its primary key.
	LabelsByIssue *dataloader.Loader[int64, []*gqlmodel.Label]
	// AssigneesByIssue loads the assignee slice for an issue by its primary key.
	AssigneesByIssue *dataloader.Loader[int64, []*gqlmodel.User]
	// CommentsByIssue loads the first-page comment preview for an issue. The
	// value is the domain slice, not the rendered model: the renderer needs
	// the issue number and repository coordinates only the caller holds.
	CommentsByIssue *dataloader.Loader[commentsPreviewKey, []*domain.Comment]
}

// newLoaders constructs a fresh Loaders set for one request. batch provides
// the underlying batch-store methods; urls and format render domain types into
// the gqlmodel shapes the resolvers return.
func newLoaders(batch *domain.Batcher, urls *presenter.URLBuilder, format nodeid.Format) *Loaders {
	return &Loaders{
		Users: dataloader.New(func(ctx context.Context, pks []int64) (map[int64]*gqlmodel.User, error) {
			users, err := batch.Users(ctx, pks)
			if err != nil {
				return nil, err
			}
			out := make(map[int64]*gqlmodel.User, len(users))
			for pk, u := range users {
				out[pk] = urls.GQLUser(u, format)
			}
			return out, nil
		}, loaderWait),

		LabelsByIssue: dataloader.New(func(ctx context.Context, issuePKs []int64) (map[int64][]*gqlmodel.Label, error) {
			lmap, err := batch.LabelsByIssues(ctx, issuePKs)
			if err != nil {
				return nil, err
			}
			out := make(map[int64][]*gqlmodel.Label, len(lmap))
			for pk, labels := range lmap {
				nodes := make([]*gqlmodel.Label, 0, len(labels))
				for _, l := range labels {
					nodes = append(nodes, &gqlmodel.Label{
						ID:          nodeid.Encode(nodeid.KindLabel, l.ID, format),
						Name:        l.Name,
						Color:       l.Color,
						Description: l.Description,
					})
				}
				out[pk] = nodes
			}
			return out, nil
		}, loaderWait),

		AssigneesByIssue: dataloader.New(func(ctx context.Context, issuePKs []int64) (map[int64][]*gqlmodel.User, error) {
			amap, err := batch.AssigneesByIssues(ctx, issuePKs)
			if err != nil {
				return nil, err
			}
			out := make(map[int64][]*gqlmodel.User, len(amap))
			for pk, assignees := range amap {
				users := make([]*gqlmodel.User, 0, len(assignees))
				for _, u := range assignees {
					users = append(users, urls.GQLUser(u, format))
				}
				out[pk] = users
			}
			return out, nil
		}, loaderWait),

		CommentsByIssue: dataloader.New(func(ctx context.Context, keys []commentsPreviewKey) (map[commentsPreviewKey][]*domain.Comment, error) {
			// Group by limit so each distinct first: argument is one query.
			byLimit := map[int][]int64{}
			for _, k := range keys {
				byLimit[k.Limit] = append(byLimit[k.Limit], k.IssuePK)
			}
			out := make(map[commentsPreviewKey][]*domain.Comment, len(keys))
			for limit, pks := range byLimit {
				cmap, err := batch.CommentsPreviewByIssues(ctx, pks, limit)
				if err != nil {
					return nil, err
				}
				for pk, comments := range cmap {
					out[commentsPreviewKey{IssuePK: pk, Limit: limit}] = comments
				}
			}
			return out, nil
		}, loaderWait),
	}
}

// withLoaders stores l on ctx and returns the annotated context.
func withLoaders(ctx context.Context, l *Loaders) context.Context {
	return context.WithValue(ctx, loadersKey{}, l)
}

// loadersFrom retrieves the per-request Loaders from ctx. It returns nil when
// the middleware was not installed; callers must handle the nil case.
func loadersFrom(ctx context.Context) *Loaders {
	l, _ := ctx.Value(loadersKey{}).(*Loaders)
	return l
}

// loadersMiddleware creates a fresh Loaders instance for each request and
// stores it on the request context before calling next. The loaders render
// node ids, so they pick up the per-request format the X-Github-Next-Global-ID
// middleware stored on the context, falling back to the configured default.
func loadersMiddleware(batch *domain.Batcher, urls *presenter.URLBuilder, format nodeid.Format, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := withLoaders(r.Context(), newLoaders(batch, urls, idFormat(r.Context(), format)))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

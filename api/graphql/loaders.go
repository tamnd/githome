package graphql

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/tamnd/githome/api/graphql/dataloader"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/presenter/gqlmodel"
)

const loaderWait = time.Millisecond

type loadersKey struct{}

// Loaders holds the per-request batch loaders. One instance is created per
// HTTP request by loadersMiddleware and stored on the request context.
type Loaders struct {
	// Users loads a gqlmodel.Actor by user primary key.
	Users *dataloader.Loader[int64, *gqlmodel.Actor]
	// LabelsByIssue loads the label slice for an issue by its primary key.
	LabelsByIssue *dataloader.Loader[int64, []*gqlmodel.Label]
	// AssigneesByIssue loads the assignee slice for an issue by its primary key.
	AssigneesByIssue *dataloader.Loader[int64, []*gqlmodel.Actor]
}

// newLoaders constructs a fresh Loaders set for one request. batch provides
// the underlying batch-store methods; urls and format render domain types into
// the gqlmodel shapes the resolvers return.
func newLoaders(batch *domain.Batcher, urls *presenter.URLBuilder, format nodeid.Format) *Loaders {
	return &Loaders{
		Users: dataloader.New(func(ctx context.Context, pks []int64) (map[int64]*gqlmodel.Actor, error) {
			users, err := batch.Users(ctx, pks)
			if err != nil {
				return nil, err
			}
			out := make(map[int64]*gqlmodel.Actor, len(users))
			for pk, u := range users {
				out[pk] = &gqlmodel.Actor{
					Login:     u.Login,
					URL:       gqlmodel.URI(urls.UserHTML(u.Login)),
					AvatarURL: gqlmodel.URI(urls.HTML("avatars", "u", strconv.FormatInt(u.ID, 10))),
				}
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

		AssigneesByIssue: dataloader.New(func(ctx context.Context, issuePKs []int64) (map[int64][]*gqlmodel.Actor, error) {
			amap, err := batch.AssigneesByIssues(ctx, issuePKs)
			if err != nil {
				return nil, err
			}
			out := make(map[int64][]*gqlmodel.Actor, len(amap))
			for pk, assignees := range amap {
				actors := make([]*gqlmodel.Actor, 0, len(assignees))
				for _, u := range assignees {
					actors = append(actors, &gqlmodel.Actor{
						Login:     u.Login,
						URL:       gqlmodel.URI(urls.UserHTML(u.Login)),
						AvatarURL: gqlmodel.URI(urls.HTML("avatars", "u", strconv.FormatInt(u.ID, 10))),
					})
				}
				out[pk] = actors
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
// stores it on the request context before calling next.
func loadersMiddleware(batch *domain.Batcher, urls *presenter.URLBuilder, format nodeid.Format, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := withLoaders(r.Context(), newLoaders(batch, urls, format))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

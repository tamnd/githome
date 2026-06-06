package graphql

import (
	"net/http"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/go-mizu/mizu"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/tamnd/githome/api/graphql/generated"
	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
)

// Deps are the dependencies the GraphQL surface needs to mount: the auth service
// that resolves the request actor, the domain services resolvers fetch through,
// the presenter that renders the wire shapes, the node-ID format, and the
// Batcher that backs the per-request dataloaders.
type Deps struct {
	Auth       *auth.Service
	Repos      *domain.RepoService
	Issues     *domain.IssueService
	Pulls      *domain.PRService
	Reviews    *domain.ReviewService
	Checks     *domain.ChecksService
	Batch      *domain.Batcher
	URLs       *presenter.URLBuilder
	NodeFormat nodeid.Format
}

// maxQueryComplexity is the maximum allowed query-complexity score. GitHub's
// public API uses 5000; Githome matches that value.
const maxQueryComplexity = 5000

// maxQueryDepth is the maximum nesting depth allowed before the server rejects
// the document. GitHub enforces 10 levels.
const maxQueryDepth = 10

// NewHandler builds the GraphQL HTTP handler: the gqlgen executable schema over
// the root resolver, the POST and GET transports gh and octokit use, a parsed
// query cache, the auth middleware that mirrors the REST surface, the
// per-request dataloader middleware, and complexity + depth guards.
func NewHandler(d Deps) http.Handler {
	es := generated.NewExecutableSchema(generated.Config{
		Resolvers:  &Resolver{
			Repos:      d.Repos,
			Issues:     d.Issues,
			Pulls:      d.Pulls,
			Reviews:    d.Reviews,
			Checks:     d.Checks,
			URLs:       d.URLs,
			NodeFormat: d.NodeFormat,
		},
		Complexity: buildComplexityRoot(),
	})
	srv := handler.New(es)
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.GET{})
	srv.SetQueryCache(lru.New[*ast.QueryDocument](256))
	srv.Use(extension.Introspection{})
	srv.Use(extension.FixedComplexityLimit(maxQueryComplexity))
	srv.Use(depthLimitExtension(maxQueryDepth))
	var h http.Handler = srv
	if d.Batch != nil {
		h = loadersMiddleware(d.Batch, d.URLs, d.NodeFormat, h)
	}
	return authenticate(d.Auth, h)
}

// Mount registers the GraphQL endpoint at both the GHES-style /api/graphql and
// the github.com-style /graphql, sharing one handler.
func Mount(root *mizu.Router, d Deps) {
	h := NewHandler(d)
	root.Compat.Handle("/graphql", h)
	root.Compat.Handle("/api/graphql", h)
}

// authenticate resolves the Authorization header into an actor and stores it on
// the request context, the same contract as the REST auth middleware: a missing
// credential flows through as anonymous, while a credential that is present but
// invalid is a 401.
func authenticate(svc *auth.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if svc != nil {
			actor, err := svc.Authenticate(r.Context(), r.Header.Get("Authorization"))
			if err != nil {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"Bad credentials","documentation_url":"https://docs.github.com/rest"}`))
				return
			}
			r = r.WithContext(auth.WithActor(r.Context(), actor))
		}
		next.ServeHTTP(w, r)
	})
}

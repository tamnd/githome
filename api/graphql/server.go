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
	Users      *domain.UserService
	Search     *domain.SearchService
	Releases   *domain.ReleaseService
	Batch      *domain.Batcher
	URLs       *presenter.URLBuilder
	NodeFormat nodeid.Format
}

// maxQueryComplexity is the maximum allowed query-complexity score. GitHub's
// public API uses 5000; Githome matches that value.
const maxQueryComplexity = 5000

// maxQueryDepth is the maximum nesting depth allowed before the server rejects
// the document. gh's statusCheckRollup expansion nests 13 levels deep, so the
// cap sits well above the deepest document a real client sends.
const maxQueryDepth = 25

// NewHandler builds the GraphQL HTTP handler: the gqlgen executable schema over
// the root resolver, the POST and GET transports gh and octokit use, a parsed
// query cache, the auth middleware that mirrors the REST surface, the
// per-request dataloader middleware, and complexity + depth guards.
func NewHandler(d Deps) http.Handler {
	es := generated.NewExecutableSchema(generated.Config{
		Resolvers: &Resolver{
			Repos:      d.Repos,
			Issues:     d.Issues,
			Pulls:      d.Pulls,
			Reviews:    d.Reviews,
			Checks:     d.Checks,
			Users:      d.Users,
			SearchSvc:  d.Search,
			Releases:   d.Releases,
			URLs:       d.URLs,
			NodeFormat: d.NodeFormat,
		},
		Complexity: buildComplexityRoot(),
	})
	srv := handler.New(es)
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.GET{})
	srv.SetQueryCache(lru.New[*ast.QueryDocument](256))
	srv.SetErrorPresenter(presentError)
	srv.Use(extension.Introspection{})
	srv.Use(extension.FixedComplexityLimit(maxQueryComplexity))
	srv.Use(depthLimitExtension(maxQueryDepth))
	var h http.Handler = liftErrorTypes(srv)
	if d.Batch != nil {
		h = loadersMiddleware(d.Batch, d.URLs, d.NodeFormat, h)
	}
	// The format middleware sits outside the loaders middleware so the
	// per-request dataloaders render node ids in the header-selected format.
	h = idFormatMiddleware(d.NodeFormat, h)
	return authenticate(d.Auth, h)
}

// Mount registers the GraphQL endpoint at both the GHES-style /api/graphql and
// the github.com-style /graphql, sharing one handler. The routes are scoped to
// the two methods the handler serves, GET and POST, rather than registered for
// every method. That is not only tighter (a PUT to the endpoint is a clean 405
// instead of reaching the gql transport), it is also what keeps /api/graphql
// from colliding with the web front's greedy GET /{owner}/{repo} route: a
// method-less pattern matches more methods but a more general path is also in
// play, which the Go 1.22 mux cannot order and so rejects at registration.
// Pinning the method makes GET /api/graphql win cleanly on path specificity.
func Mount(root *mizu.Router, d Deps) {
	h := NewHandler(d)
	for _, p := range []string{"/graphql", "/api/graphql"} {
		root.Compat.HandleMethod(http.MethodGet, p, h)
		root.Compat.HandleMethod(http.MethodPost, p, h)
	}
}

// authenticate resolves the Authorization header into an actor and stores it on
// the request context. Unlike the REST surface, where a missing credential flows
// through as anonymous, the GraphQL endpoint requires authentication outright:
// GitHub 401s unauthenticated GraphQL requests at the transport, before any
// execution, and viewer: User! could not answer for an anonymous actor anyway.
// A credential that is present but invalid is the usual bad-credentials 401.
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
			if actor.UserID == 0 {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"This endpoint requires you to be authenticated.","documentation_url":"https://docs.github.com/graphql/guides/forming-calls-with-graphql#authenticating-with-graphql","status":"401"}`))
				return
			}
			r = r.WithContext(auth.WithActor(r.Context(), actor))
		}
		next.ServeHTTP(w, r)
	})
}

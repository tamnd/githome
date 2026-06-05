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
// the presenter that renders the wire shapes, and the node-ID format.
type Deps struct {
	Auth       *auth.Service
	Repos      *domain.RepoService
	Issues     *domain.IssueService
	URLs       *presenter.URLBuilder
	NodeFormat nodeid.Format
}

// NewHandler builds the GraphQL HTTP handler: the gqlgen executable schema over
// the root resolver, the POST and GET transports gh and octokit use, a parsed
// query cache, and the auth middleware that mirrors the REST surface.
func NewHandler(d Deps) http.Handler {
	es := generated.NewExecutableSchema(generated.Config{Resolvers: &Resolver{
		Repos:      d.Repos,
		Issues:     d.Issues,
		URLs:       d.URLs,
		NodeFormat: d.NodeFormat,
	}})
	srv := handler.New(es)
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.GET{})
	srv.SetQueryCache(lru.New[*ast.QueryDocument](256))
	srv.Use(extension.Introspection{})
	return authenticate(d.Auth, srv)
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

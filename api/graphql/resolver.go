package graphql

import (
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
)

// Resolver is the GraphQL root resolver. It holds the domain services the
// resolvers fetch through and the presenter that renders domain values into the
// gqlmodel wire shapes. Resolvers never touch the store or git directly, the
// same rule the REST handlers follow.
type Resolver struct {
	Repos      *domain.RepoService
	Issues     *domain.IssueService
	URLs       *presenter.URLBuilder
	NodeFormat nodeid.Format
}

// Package graphql implements Githome's GraphQL API v4. The schema under schema/
// is the source of truth; gqlgen generates the executable schema into generated/
// and the resolver stubs into this package, which are then filled in by hand.
//
//go:generate go run github.com/99designs/gqlgen generate
package graphql

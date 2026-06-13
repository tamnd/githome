package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	gqlruntime "github.com/99designs/gqlgen/graphql"
)

// GitHub does not score a GraphQL document by a fixed per-field complexity. It
// walks the requested connections, multiplies each by the first/last page size
// of itself and its ancestors, and gates on two numbers: the total node count a
// document could return (capped at 500,000) and the points it costs (the sum of
// the per-connection requests divided by 100, charged against an hourly budget).
// This file implements that walk: nodeLimit rejects a document over the node
// cap, and queryCost computes the node count and point cost the rateLimit
// resolver reports.

// maxNodeLimit is GitHub's published ceiling on the number of nodes a single
// GraphQL document may request. A document that could return more is rejected
// before execution.
const maxNodeLimit = 500_000

// rateLimitBudget is the hourly point budget GitHub grants an authenticated
// caller; Githome reports against it but does not deduct from it.
const rateLimitBudget = 5000

// costWalk walks a selection set and returns the node count and request count it
// represents. parentNodes is the number of parent rows a connection at this
// level is fetched against (1 at the root). A connection field contributes
// parentNodes*size nodes and parentNodes requests, and its children inherit
// parentNodes*size as their own parent count; a plain field passes parentNodes
// through unchanged. Inline fragments and named fragment spreads are followed so
// the walk sees the document the server executes.
func costWalk(sel ast.SelectionSet, parentNodes int, vars map[string]any) (nodes, requests int) {
	for _, s := range sel {
		switch f := s.(type) {
		case *ast.Field:
			childParent := parentNodes
			if size, isConn := paginationSize(f, vars); isConn {
				nodes += parentNodes * size
				requests += parentNodes
				childParent = parentNodes * size
			}
			n, r := costWalk(f.SelectionSet, childParent, vars)
			nodes += n
			requests += r
		case *ast.InlineFragment:
			n, r := costWalk(f.SelectionSet, parentNodes, vars)
			nodes += n
			requests += r
		case *ast.FragmentSpread:
			if f.Definition != nil {
				n, r := costWalk(f.Definition.SelectionSet, parentNodes, vars)
				nodes += n
				requests += r
			}
		}
	}
	return nodes, requests
}

// paginationSize reports the page size a connection field requests and whether
// the field is a connection at all. A field carrying first or last is a
// connection; the larger of the two bounds the page. A field with neither is a
// plain object or scalar, which contributes no nodes of its own.
func paginationSize(f *ast.Field, vars map[string]any) (int, bool) {
	size, isConn := 0, false
	for _, arg := range f.Arguments {
		if arg.Name != "first" && arg.Name != "last" {
			continue
		}
		isConn = true
		if v, ok := argInt(arg.Value, vars); ok && v > size {
			size = v
		}
	}
	return size, isConn
}

// argInt resolves an Int argument to its value, following a variable reference
// into the operation's variables. A missing or non-integer value reports false.
func argInt(v *ast.Value, vars map[string]any) (int, bool) {
	if v == nil {
		return 0, false
	}
	if v.Kind == ast.Variable {
		raw, ok := vars[v.Raw]
		if !ok {
			return 0, false
		}
		switch n := raw.(type) {
		case int:
			return n, true
		case int32:
			return int(n), true
		case int64:
			return int(n), true
		case float64:
			return int(n), true
		case json.Number:
			i, err := n.Int64()
			if err != nil {
				return 0, false
			}
			return int(i), true
		case string:
			i, err := strconv.Atoi(n)
			if err != nil {
				return 0, false
			}
			return i, true
		}
		return 0, false
	}
	n, err := strconv.Atoi(v.Raw)
	if err != nil {
		return 0, false
	}
	return n, true
}

// pointCost maps a request count to GitHub's point cost: the requests divided by
// 100, rounded up, with a floor of one point per call.
func pointCost(requests int) int {
	cost := (requests + 99) / 100
	if cost < 1 {
		cost = 1
	}
	return cost
}

// queryCost computes the node count and point cost of the operation on ctx. It
// is the value the rateLimit resolver reports; a request with no operation in
// flight costs the one-point minimum.
func queryCost(ctx context.Context) (nodeCount, cost int) {
	opCtx := gqlruntime.GetOperationContext(ctx)
	if opCtx == nil || opCtx.Operation == nil {
		return 0, 1
	}
	nodes, requests := costWalk(opCtx.Operation.SelectionSet, 1, opCtx.Variables)
	return nodes, pointCost(requests)
}

// nodeLimit is a gqlgen extension that rejects a document whose node count
// exceeds the configured maximum, GitHub's node-count gate. It replaces a fixed
// per-field complexity limit, which rejected documents GitHub serves without
// charge.
type nodeLimit struct {
	max int
}

var _ interface {
	gqlruntime.HandlerExtension
	gqlruntime.OperationContextMutator
} = nodeLimit{}

func (nodeLimit) ExtensionName() string                      { return "NodeCountLimit" }
func (nodeLimit) Validate(gqlruntime.ExecutableSchema) error { return nil }

// MutateOperationContext walks the operation's connections and rejects the
// document before execution if its node count exceeds the cap.
func (n nodeLimit) MutateOperationContext(_ context.Context, opCtx *gqlruntime.OperationContext) *gqlerror.Error {
	if opCtx.Operation == nil {
		return nil
	}
	nodes, _ := costWalk(opCtx.Operation.SelectionSet, 1, opCtx.Variables)
	if nodes > n.max {
		return &gqlerror.Error{
			Message: fmt.Sprintf("Query has node count of %d, which exceeds the maximum of %d", nodes, n.max),
			Extensions: map[string]any{
				"code": "MAX_NODE_LIMIT_EXCEEDED",
			},
		}
	}
	return nil
}

// nodeLimitExtension returns a gqlgen extension that rejects queries requesting
// more than max nodes.
func nodeLimitExtension(max int) gqlruntime.HandlerExtension {
	return nodeLimit{max: max}
}

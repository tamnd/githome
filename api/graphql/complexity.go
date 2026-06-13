package graphql

import (
	"context"
	"fmt"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	gqlruntime "github.com/99designs/gqlgen/graphql"
)

// depthLimit is a gqlgen HandlerExtension + OperationContextMutator that
// rejects documents whose nesting depth exceeds the configured maximum.
type depthLimit struct {
	max int
}

var _ interface {
	gqlruntime.HandlerExtension
	gqlruntime.OperationContextMutator
} = depthLimit{}

func (depthLimit) ExtensionName() string                      { return "QueryDepthLimit" }
func (depthLimit) Validate(gqlruntime.ExecutableSchema) error { return nil }

func (d depthLimit) MutateOperationContext(_ context.Context, opCtx *gqlruntime.OperationContext) *gqlerror.Error {
	op := opCtx.Operation
	if op == nil {
		return nil
	}
	depth := selectionDepth(op.SelectionSet, 0)
	if depth > d.max {
		return &gqlerror.Error{
			Message: fmt.Sprintf("Query has depth of %d, which exceeds max depth of %d", depth, d.max),
			Extensions: map[string]any{
				"code": "MAX_QUERY_DEPTH",
			},
		}
	}
	return nil
}

// depthLimitExtension returns a gqlgen extension that rejects queries deeper
// than max selection-set levels.
func depthLimitExtension(limit int) gqlruntime.HandlerExtension {
	return depthLimit{max: limit}
}

// selectionDepth returns the maximum selection-set nesting depth, counting
// from current.
func selectionDepth(sel ast.SelectionSet, current int) int {
	if len(sel) == 0 {
		return current
	}
	deepest := current
	for _, s := range sel {
		var child ast.SelectionSet
		switch f := s.(type) {
		case *ast.Field:
			child = f.SelectionSet
		case *ast.InlineFragment:
			child = f.SelectionSet
		case *ast.FragmentSpread:
			if f.Definition != nil {
				child = f.Definition.SelectionSet
			}
		}
		if d := selectionDepth(child, current+1); d > deepest {
			deepest = d
		}
	}
	return deepest
}

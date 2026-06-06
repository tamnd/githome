package graphql

import (
	"context"
	"fmt"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	gqlruntime "github.com/99designs/gqlgen/graphql"

	"github.com/tamnd/githome/api/graphql/generated"
	"github.com/tamnd/githome/presenter/gqlmodel"
)

// buildComplexityRoot returns a ComplexityRoot that assigns a cost of 1 to
// every scalar field and multiplies by the first/last argument for connection
// fields, matching GitHub's published complexity model.
func buildComplexityRoot() generated.ComplexityRoot {
	var c generated.ComplexityRoot

	// multFirst multiplies childComplexity by the first argument value (default 1).
	multFirst := func(first *int32, childComplexity int) int {
		n := 1
		if first != nil && *first > 0 {
			n = int(*first)
		}
		return n * childComplexity
	}

	c.Repository.Issues = func(childComplexity int, first *int32, _ *string, _ *int32, _ *string, _ []gqlmodel.IssueState) int {
		return multFirst(first, childComplexity)
	}
	c.Repository.PullRequests = func(childComplexity int, first *int32, _ *string, _ *int32, _ *string, _ []gqlmodel.PullRequestState) int {
		return multFirst(first, childComplexity)
	}
	c.Issue.Comments = func(childComplexity int, first *int32, _ *string) int {
		return multFirst(first, childComplexity)
	}
	c.Issue.Labels = func(childComplexity int, first *int32, _ *string) int {
		return multFirst(first, childComplexity)
	}
	c.PullRequest.Commits = func(childComplexity int, first *int32, _ *string) int {
		return multFirst(first, childComplexity)
	}
	c.PullRequest.Files = func(childComplexity int, first *int32, _ *string) int {
		return multFirst(first, childComplexity)
	}
	c.PullRequest.ReviewThreads = func(childComplexity int, first *int32, _ *string) int {
		return multFirst(first, childComplexity)
	}
	c.PullRequestReviewThread.Comments = func(childComplexity int, first *int32, _ *string) int {
		return multFirst(first, childComplexity)
	}

	return c
}

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

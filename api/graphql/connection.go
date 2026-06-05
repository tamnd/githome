package graphql

import (
	"fmt"

	"github.com/tamnd/githome/presenter/gqlmodel"
)

// The issues connection pages forward over the domain's offset-based listing.
// first is the page size (default 30, capped at 100); after carries the absolute
// offset already consumed. Backward pagination (last/before) is not yet served.
const (
	defaultPageSize = 30
	maxPageSize     = 100
)

// issuePage is the resolved forward window: how many rows to read and the
// absolute offset to start at.
type issuePage struct {
	limit  int
	offset int
}

// page is the one-based page number for the domain's page/per-page listing. The
// cursor offset is always a multiple of the page size while a client pages with
// a constant first, so the division is exact on the common path.
func (p issuePage) page() int { return p.offset/p.limit + 1 }

// issuePageArgs validates the Relay page arguments and resolves the forward
// window. It mirrors GitHub's wording for the over-limit and unsupported cases.
func issuePageArgs(first *int32, after *string, last *int32, before *string) (issuePage, error) {
	if last != nil || before != nil {
		return issuePage{}, fmt.Errorf("backward pagination with `last`/`before` is not supported on this connection.")
	}
	p := issuePage{limit: defaultPageSize}
	if first != nil {
		n := int(*first)
		if n < 0 {
			return p, fmt.Errorf("`first` must be a non-negative integer.")
		}
		if n > maxPageSize {
			return p, fmt.Errorf("Requesting %d records on this connection exceeds the `first` limit of %d records.", n, maxPageSize)
		}
		p.limit = n
	}
	if after != nil {
		off, err := decodeCursor(*after)
		if err != nil {
			return p, err
		}
		p.offset = off
	}
	return p, nil
}

// emptyIssueConnection is the connection a not-found or invisible repository
// resolves to: no nodes, a zero total, and a page-info with no further pages.
func emptyIssueConnection() *gqlmodel.IssueConnection {
	return &gqlmodel.IssueConnection{
		Edges:    []*gqlmodel.IssueEdge{},
		Nodes:    []*gqlmodel.Issue{},
		PageInfo: &gqlmodel.PageInfo{},
	}
}

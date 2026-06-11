package graphql

import (
	"fmt"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/presenter/gqlmodel"
)

// The connections page over the domain's offset-based listings. first/last is
// the page size (default 30, capped at 100); after/before carry the absolute
// offset already consumed. A backward window (last/before, the way gh selects
// comments(last: 1) and commits(last: 1)) resolves against the total once the
// caller knows it, via window.
const (
	defaultPageSize = 30
	maxPageSize     = 100
)

// issuePage is the resolved page window. Forward windows carry the limit and
// the absolute offset to start at. Backward windows set backward and anchor at
// the end of the list, optionally bounded by a before cursor.
type issuePage struct {
	limit    int
	offset   int
	backward bool
	before   int // exclusive end offset from a before: cursor, -1 when absent
}

// page is the one-based page number for the domain's page/per-page listing. The
// cursor offset is always a multiple of the page size while a client pages with
// a constant first, so the division is exact on the common path.
func (p issuePage) page() int { return p.offset/p.limit + 1 }

// window resolves the absolute row range [start, end) against a known total.
// Forward windows start at the offset; backward windows take the last rows
// before the before cursor (or the end of the list).
func (p issuePage) window(total int) (start, end int) {
	if p.backward {
		end = total
		if p.before >= 0 && p.before < end {
			end = p.before
		}
		start = end - p.limit
		if start < 0 {
			start = 0
		}
		return start, end
	}
	start = p.offset
	if start > total {
		start = total
	}
	end = start + p.limit
	if end > total {
		end = total
	}
	return start, end
}

// issuePageArgs validates the Relay page arguments and resolves the window. It
// mirrors GitHub's wording for the over-limit cases.
func issuePageArgs(first *int32, after *string, last *int32, before *string) (issuePage, error) {
	p := issuePage{limit: defaultPageSize, before: -1}
	if first != nil {
		n := int(*first)
		if n < 0 {
			return p, gqlError{"`first` must be a non-negative integer."}
		}
		if n > maxPageSize {
			return p, gqlError{fmt.Sprintf("Requesting %d records on this connection exceeds the `first` limit of %d records.", n, maxPageSize)}
		}
		p.limit = n
	}
	if last != nil {
		n := int(*last)
		if n < 0 {
			return p, gqlError{"`last` must be a non-negative integer."}
		}
		if n > maxPageSize {
			return p, gqlError{fmt.Sprintf("Requesting %d records on this connection exceeds the `last` limit of %d records.", n, maxPageSize)}
		}
		p.backward = true
		if first == nil {
			p.limit = n
		}
	}
	if after != nil {
		off, err := decodeCursor(*after)
		if err != nil {
			return p, err
		}
		p.offset = off
	}
	if before != nil {
		off, err := decodeCursor(*before)
		if err != nil {
			return p, err
		}
		p.before = off
		p.backward = true
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

// buildPullRequestConnection renders a page of domain pull requests into the
// GraphQL connection, cursoring each edge at its absolute offset so a follow-up
// after: cursor resumes past it, the same forward window the issues connection
// pages over.
func (r *Resolver) buildPullRequestConnection(owner, name string, prs []*domain.PullRequest, total, offset int) *gqlmodel.PullRequestConnection {
	nodes := make([]*gqlmodel.PullRequest, 0, len(prs))
	edges := make([]*gqlmodel.PullRequestEdge, 0, len(prs))
	for i, pr := range prs {
		node := r.URLs.GQLPullRequest(owner, name, pr, r.NodeFormat)
		nodes = append(nodes, node)
		edges = append(edges, &gqlmodel.PullRequestEdge{Cursor: encodeCursor(offset + i + 1), Node: node})
	}
	info := &gqlmodel.PageInfo{HasNextPage: offset+len(prs) < total}
	if len(edges) > 0 {
		start, end := edges[0].Cursor, edges[len(edges)-1].Cursor
		info.StartCursor = &start
		info.EndCursor = &end
		info.HasPreviousPage = offset > 0
	}
	return &gqlmodel.PullRequestConnection{
		Nodes:      nodes,
		Edges:      edges,
		PageInfo:   info,
		TotalCount: int32(total),
	}
}

// emptyPullRequestConnection is the connection a not-found or invisible
// repository resolves to: no nodes, a zero total, and no further pages.
func emptyPullRequestConnection() *gqlmodel.PullRequestConnection {
	return &gqlmodel.PullRequestConnection{
		Edges:    []*gqlmodel.PullRequestEdge{},
		Nodes:    []*gqlmodel.PullRequest{},
		PageInfo: &gqlmodel.PageInfo{},
	}
}

// pageInfoFor builds the Relay page info for a window of count rows starting
// at the absolute offset start, out of total rows. The cursors are the same
// absolute-offset cursors the edges carry.
func pageInfoFor(start, count, total int) *gqlmodel.PageInfo {
	info := &gqlmodel.PageInfo{
		HasNextPage:     start+count < total,
		HasPreviousPage: start > 0,
	}
	if count > 0 {
		s, e := encodeCursor(start+1), encodeCursor(start+count)
		info.StartCursor = &s
		info.EndCursor = &e
	}
	return info
}

package graphql

import (
	"context"
	"fmt"

	"github.com/99designs/gqlgen/graphql"
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
	// seek is the stable id (issue/pull number, comment id) of the item the
	// after: cursor points at, when the cursor carried one. A forward window
	// with a seek can resume with a keyset query instead of an OFFSET scan.
	seek int64
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
// mirrors GitHub's wording: a connection must be paginated with exactly one of
// first or last, the messages name the connection field, and the over-limit
// cases name the offending argument.
func issuePageArgs(ctx context.Context, first *int32, after *string, last *int32, before *string) (issuePage, error) {
	p := issuePage{limit: defaultPageSize, before: -1}
	name := connectionName(ctx)
	if first == nil && last == nil {
		return p, gqlError{fmt.Sprintf("You must provide a `first` or `last` value to properly paginate the `%s` connection.", name)}
	}
	if first != nil && last != nil {
		return p, gqlError{fmt.Sprintf("You must provide either `first` or `last` for the `%s` connection, not both.", name)}
	}
	if first != nil {
		n := int(*first)
		if n < 0 {
			return p, gqlError{"`first` must be a non-negative integer."}
		}
		if n > maxPageSize {
			return p, gqlError{fmt.Sprintf("Requesting %d records on the `%s` connection exceeds the `first` limit of %d records.", n, name, maxPageSize)}
		}
		p.limit = n
	}
	if last != nil {
		n := int(*last)
		if n < 0 {
			return p, gqlError{"`last` must be a non-negative integer."}
		}
		if n > maxPageSize {
			return p, gqlError{fmt.Sprintf("Requesting %d records on the `%s` connection exceeds the `last` limit of %d records.", n, name, maxPageSize)}
		}
		p.backward = true
		p.limit = n
	}
	if after != nil {
		off, seek, err := decodeCursorSeek(*after)
		if err != nil {
			return p, err
		}
		p.offset = off
		p.seek = seek
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

// connectionName is the field name of the executing connection, the name
// GitHub's pagination errors quote. Outside an operation (a helper called
// directly in a test) it falls back to a generic placeholder.
func connectionName(ctx context.Context) string {
	if graphql.HasOperationContext(ctx) {
		if fc := graphql.GetFieldContext(ctx); fc != nil && fc.Field.Name != "" {
			return fc.Field.Name
		}
	}
	return "nodes"
}

// totalCountSelected reports whether the executing connection field's
// selection set asks for totalCount. The keyset list paths return the page
// without a COUNT, so a query that does not select totalCount (gh issue list,
// gh pr list) pays no count cost at all. Outside an operation, or when the
// field context is unavailable, it errs on computing the count.
func totalCountSelected(ctx context.Context) bool {
	if !graphql.HasOperationContext(ctx) {
		return true
	}
	fc := graphql.GetFieldContext(ctx)
	if fc == nil {
		return true
	}
	for _, f := range graphql.CollectFields(graphql.GetOperationContext(ctx), fc.Field.Selections, nil) {
		if f.Name == "totalCount" {
			return true
		}
	}
	return false
}

// countOnlySelection reports whether the executing connection field selects
// nothing beyond totalCount and pageInfo, the badge-count shape
// pullRequests { nodes { files { totalCount } } } sends. Those connections
// answer from the cached count columns without listing, and in the commits
// and files cases without forking git per node. It returns false when the
// selection cannot be inspected, so the caller falls back to the full path.
func countOnlySelection(ctx context.Context) bool {
	if !graphql.HasOperationContext(ctx) {
		return false
	}
	fc := graphql.GetFieldContext(ctx)
	if fc == nil {
		return false
	}
	fields := graphql.CollectFields(graphql.GetOperationContext(ctx), fc.Field.Selections, nil)
	if len(fields) == 0 {
		return false
	}
	for _, f := range fields {
		if f.Name != "totalCount" && f.Name != "pageInfo" && f.Name != "__typename" {
			return false
		}
	}
	return true
}

// lazyTotal synthesizes the total buildIssueConnection and friends derive
// hasNextPage from, when the real count was skipped: one past the window when
// a further page exists, exactly the window's end otherwise. The totalCount
// field built from it is never serialized, because it was not selected.
func lazyTotal(offset, count int, hasMore bool) int {
	if hasMore {
		return offset + count + 1
	}
	return offset + count
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
// GraphQL connection. Each edge's cursor carries its absolute offset plus the
// pull request's number, so a follow-up after: cursor resumes past it with a
// keyset seek, the same forward window the issues connection pages over.
func (r *Resolver) buildPullRequestConnection(ctx context.Context, owner, name string, prs []*domain.PullRequest, total, offset int) *gqlmodel.PullRequestConnection {
	nodes := make([]*gqlmodel.PullRequest, 0, len(prs))
	edges := make([]*gqlmodel.PullRequestEdge, 0, len(prs))
	for i, pr := range prs {
		node := r.URLs.GQLPullRequest(owner, name, pr, r.format(ctx))
		nodes = append(nodes, node)
		edges = append(edges, &gqlmodel.PullRequestEdge{Cursor: encodeCursorSeek(offset+i+1, pr.Number), Node: node})
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

// pageInfoSeek is pageInfoFor with the window's first and last item ids riding
// on the cursors, so an after: built from endCursor resumes with a keyset
// query. The comment connections use it; their nodes carry no edges, so the
// page-info cursors are the only ones a client can hand back.
func pageInfoSeek(start, count, total int, firstID, lastID int64) *gqlmodel.PageInfo {
	info := &gqlmodel.PageInfo{
		HasNextPage:     start+count < total,
		HasPreviousPage: start > 0,
	}
	if count > 0 {
		s, e := encodeCursorSeek(start+1, firstID), encodeCursorSeek(start+count, lastID)
		info.StartCursor = &s
		info.EndCursor = &e
	}
	return info
}

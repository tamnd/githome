package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tamnd/githome/store"
)

// TestIssueKeysetIndexIsUsed proves migration 0010 created the issue-list keyset
// index and that the planner uses it for the seek the deep-page list runs,
// rather than scanning and sorting the whole repository. The plan assertion is
// SQLite-only (EXPLAIN QUERY PLAN); on Postgres it asserts only that the index
// exists, since the two planners describe plans differently.
func TestIssueKeysetIndexIsUsed(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if st.Dialect() != store.DialectSQLite {
			// Existence check is enough for the Postgres leg; the plan format
			// differs and is asserted on SQLite below.
			var n int
			err := st.DB().QueryRowContext(ctx,
				`SELECT count(*) FROM pg_indexes WHERE indexname = 'issues_repo_created_number_idx'`).Scan(&n)
			if err != nil {
				t.Fatalf("pg_indexes: %v", err)
			}
			if n != 1 {
				t.Fatalf("keyset index missing on postgres: count=%d", n)
			}
			return
		}

		var name string
		err := st.DB().QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='index' AND name='issues_repo_created_number_idx'`).Scan(&name)
		if err != nil {
			t.Fatalf("keyset index not created: %v", err)
		}

		// The seek the keyset list runs: repo_pk fixed, soft-delete excluded,
		// ordered newest-first with the (created_at, number) seek predicate. With
		// the index present this is a SEARCH using it, not a full SCAN + sort.
		const seek = `EXPLAIN QUERY PLAN
			SELECT i.pk FROM issues i
			WHERE i.repo_pk = ? AND i.deleted_at IS NULL
			  AND (i.created_at < ? OR (i.created_at = ? AND i.number < ?))
			ORDER BY i.created_at DESC, i.number DESC LIMIT ?`
		rows, err := st.DB().QueryContext(ctx, seek, 1, "2020-01-01 00:00:00", "2020-01-01 00:00:00", 100, 30)
		if err != nil {
			t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
		}
		defer func() { _ = rows.Close() }()
		var plan strings.Builder
		for rows.Next() {
			var id, parent, notUsed int
			var detail string
			if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
				t.Fatalf("scan plan: %v", err)
			}
			plan.WriteString(detail)
			plan.WriteByte('\n')
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("plan rows: %v", err)
		}
		got := plan.String()
		if !strings.Contains(got, "issues_repo_created_number_idx") {
			t.Errorf("keyset seek does not use the index; plan was:\n%s", got)
		}
		if strings.Contains(got, "USE TEMP B-TREE") {
			t.Errorf("keyset seek still sorts (temp b-tree); plan was:\n%s", got)
		}
	})
}

// explainPlan runs EXPLAIN QUERY PLAN over the query and returns the joined
// detail lines, for asserting which index a list path lands on.
func explainPlan(t *testing.T, st *store.Store, query string, args ...any) string {
	t.Helper()
	rows, err := st.DB().QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	return plan.String()
}

// TestPullKeysetPlanIsFlat proves the pull-request keyset list stays a seek:
// the repository filter sits on i.repo_pk, so the issues (repo_pk, number)
// unique index serves the filter, the number seek, and the descending order
// in one pass, with the pull row joined by its unique issue_pk. A regression
// back to filtering on pr.repo_pk shows up here as a temp b-tree sort.
func TestPullKeysetPlanIsFlat(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if st.Dialect() != store.DialectSQLite {
			t.Skip("plan assertion is sqlite-only")
		}
		// The query ListPullsPage runs with a cursor present.
		const seek = `
			SELECT pr.pk FROM pull_requests pr
			JOIN issues i ON i.pk = pr.issue_pk
			WHERE i.repo_pk = ? AND i.deleted_at IS NULL AND i.state = 'open'
			  AND i.number < ?
			ORDER BY i.number DESC LIMIT ?`
		got := explainPlan(t, st, seek, 1, 1000, 31)
		if !strings.Contains(got, "issues_repo_number_uq") {
			t.Errorf("pull keyset seek does not use the (repo_pk, number) index; plan was:\n%s", got)
		}
		if strings.Contains(got, "USE TEMP B-TREE") {
			t.Errorf("pull keyset seek still sorts (temp b-tree); plan was:\n%s", got)
		}
	})
}

// TestSortIndexesAreUsed proves migration 0021's indexes carry the list orders
// that previously scanned and sorted: ?sort=updated and ?sort=comments on the
// issue list, and the chronological comment page with its (created_at, pk)
// tie-breaker.
func TestSortIndexesAreUsed(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if st.Dialect() != store.DialectSQLite {
			t.Skip("plan assertion is sqlite-only")
		}
		cases := []struct {
			name  string
			query string
			args  []any
			index string
		}{
			{
				name: "issues sort=updated",
				query: `SELECT i.pk FROM issues i
					WHERE i.repo_pk = ? AND i.deleted_at IS NULL
					ORDER BY i.updated_at DESC, i.number DESC LIMIT ?`,
				args:  []any{1, 30},
				index: "issues_repo_updated_number_idx",
			},
			{
				name: "issues sort=comments",
				query: `SELECT i.pk FROM issues i
					WHERE i.repo_pk = ? AND i.deleted_at IS NULL
					ORDER BY i.comments_count DESC, i.number DESC LIMIT ?`,
				args:  []any{1, 30},
				index: "issues_repo_comments_number_idx",
			},
			{
				name: "comment page",
				query: `SELECT pk FROM issue_comments
					WHERE issue_pk = ? AND deleted_at IS NULL
					ORDER BY created_at, pk LIMIT ?`,
				args:  []any{1, 30},
				index: "issue_comments_issue_created_pk_idx",
			},
		}
		for _, tc := range cases {
			got := explainPlan(t, st, tc.query, tc.args...)
			if !strings.Contains(got, tc.index) {
				t.Errorf("%s does not use %s; plan was:\n%s", tc.name, tc.index, got)
			}
			if strings.Contains(got, "USE TEMP B-TREE") {
				t.Errorf("%s still sorts (temp b-tree); plan was:\n%s", tc.name, got)
			}
		}
	})
}

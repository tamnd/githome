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

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/tamnd/githome/store"
)

func TestCursorRoundTrip(t *testing.T) {
	ts := time.Unix(1700000000, 12345678).UTC()
	c := store.IssueCursor{CreatedAt: ts, Number: 42}
	encoded := store.EncodeCursor(c)
	if encoded == "" {
		t.Fatal("EncodeCursor returned empty string")
	}
	got, err := store.DecodeCursor(encoded)
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if !got.CreatedAt.Equal(c.CreatedAt) {
		t.Errorf("CreatedAt mismatch: got %v, want %v", got.CreatedAt, c.CreatedAt)
	}
	if got.Number != c.Number {
		t.Errorf("Number mismatch: got %d, want %d", got.Number, c.Number)
	}
}

func TestDecodeCursorBadInput(t *testing.T) {
	cases := []string{"", "!!", "aGVsbG8=", "bm90Y29sb24"}
	for _, s := range cases {
		if _, err := store.DecodeCursor(s); err == nil {
			t.Errorf("DecodeCursor(%q) expected error, got nil", s)
		}
	}
}

// TestListIssuesKeysetPaging verifies that cursor-based pagination returns the
// same rows as offset-based pagination for the default sort, with no gaps and
// no duplicates.
func TestListIssuesKeysetPaging(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "keyset"})
		const total = 7
		for i := 0; i < total; i++ {
			seedIssue(t, st, repo.PK, repo.OwnerPK, "issue")
		}

		// Fetch all 7 issues via offset to get the expected ordering.
		allByOffset, err := st.ListIssues(ctx, repo.PK, store.IssueFilter{Limit: total})
		if err != nil {
			t.Fatalf("ListIssues (offset): %v", err)
		}
		if len(allByOffset) != total {
			t.Fatalf("want %d issues, got %d", total, len(allByOffset))
		}

		// Walk the same list with keyset paging (page size 3).
		const pageSize = 3
		var keysetRows []store.IssueRow
		var cursor *store.IssueCursor
		for {
			f := store.IssueFilter{Limit: pageSize, Cursor: cursor}
			page, err := st.ListIssues(ctx, repo.PK, f)
			if err != nil {
				t.Fatalf("ListIssues (keyset): %v", err)
			}
			keysetRows = append(keysetRows, page...)
			if len(page) < pageSize {
				break
			}
			last := page[len(page)-1]
			c := store.IssueCursor{CreatedAt: last.CreatedAt, Number: last.Number}
			cursor = &c
		}

		if len(keysetRows) != total {
			t.Fatalf("keyset walk: got %d rows, want %d", len(keysetRows), total)
		}
		for i := range allByOffset {
			if allByOffset[i].PK != keysetRows[i].PK {
				t.Errorf("row %d: offset PK=%d, keyset PK=%d", i, allByOffset[i].PK, keysetRows[i].PK)
			}
		}
	})
}

// TestListIssuesKeysetFallsBackForNonDefaultSort verifies that the keyset path
// is not activated when the caller uses a non-created sort, so the cursor is
// silently ignored.
func TestListIssuesKeysetFallsBackForNonDefaultSort(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "fallback"})
		for i := 0; i < 5; i++ {
			seedIssue(t, st, repo.PK, repo.OwnerPK, "issue")
		}

		// A cursor with Sort="updated" should fall back to offset (no keyset seek).
		ts := time.Now().UTC()
		cur := store.IssueCursor{CreatedAt: ts, Number: 9999}
		f := store.IssueFilter{
			Sort:   "updated",
			Limit:  5,
			Cursor: &cur, // should be ignored for non-created sort
		}
		rows, err := st.ListIssues(ctx, repo.PK, f)
		if err != nil {
			t.Fatalf("ListIssues: %v", err)
		}
		// With OFFSET=0 and the cursor ignored, we still get all 5 rows.
		if len(rows) != 5 {
			t.Errorf("want 5 rows, got %d", len(rows))
		}
	})
}

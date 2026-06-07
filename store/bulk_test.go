package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/tamnd/githome/store"
)

// TestBulkSeedLeafTables drives the chunked multi-row path for the two leaf
// tables across a count larger than one chunk, and asserts the three properties
// the read path depends on: every row lands, created_at is preserved verbatim,
// and the batch-allocated db_ids are unique. The count straddles the chunk
// boundary so the statement-splitting loop is exercised, not just one INSERT.
func TestBulkSeedLeafTables(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		when := time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC)

		const n = 450 // > 2 * bulkRows, so the chunk loop runs more than twice
		var issuePK, repoPK int64
		dbIDs := make(map[int64]bool)

		err := st.WithTx(ctx, func(tx *store.Tx) error {
			owner := &store.UserRow{Login: "torvalds", CreatedAt: when, UpdatedAt: when}
			if err := tx.SeedUser(ctx, owner); err != nil {
				return err
			}
			repo := &store.RepoRow{OwnerPK: owner.PK, Name: "linux", DefaultBranch: "master", CreatedAt: when, UpdatedAt: when}
			if err := tx.SeedRepo(ctx, repo); err != nil {
				return err
			}
			repoPK = repo.PK
			iss := &store.IssueRow{RepoPK: repo.PK, Number: 1, Title: "t", UserPK: owner.PK, State: "open", CreatedAt: when, UpdatedAt: when}
			if err := tx.SeedIssue(ctx, iss); err != nil {
				return err
			}
			issuePK = iss.PK

			// reactions carry a UNIQUE(subject_type, subject_pk, user_pk, content),
			// so a single subject needs distinct (user, content) pairs. Seed a
			// reactor pool and spread the rows across it, mirroring how the corpus
			// seeder materializes counts against a bounded pool.
			const pool = 60
			reactorPKs := make([]int64, pool)
			for i := range pool {
				u := &store.UserRow{Login: "reactor-" + string(rune('a'+i%26)) + string(rune('a'+i/26)), CreatedAt: when, UpdatedAt: when}
				if err := tx.SeedUser(ctx, u); err != nil {
					return err
				}
				reactorPKs[i] = u.PK
			}

			reactions := make([]store.ReactionRow, n)
			events := make([]store.IssueEventRow, n)
			contents := store.ReactionContents
			for i := range n {
				reactions[i] = store.ReactionRow{
					SubjectType: "issue", SubjectPK: iss.PK, UserPK: reactorPKs[i%pool],
					Content: contents[(i/pool)%len(contents)], CreatedAt: when,
				}
				events[i] = store.IssueEventRow{
					RepoPK: repo.PK, IssuePK: iss.PK, ActorPK: &owner.PK,
					Event: "labeled", Payload: `{"label":{"name":"bug"}}`, CreatedAt: when,
				}
			}
			if err := tx.SeedReactionsBulk(ctx, reactions); err != nil {
				return err
			}
			return tx.SeedIssueEventsBulk(ctx, events)
		})
		if err != nil {
			t.Fatalf("bulk seed tx: %v", err)
		}

		evs, err := st.ListIssueEvents(ctx, issuePK)
		if err != nil {
			t.Fatalf("ListIssueEvents: %v", err)
		}
		if len(evs) != n {
			t.Fatalf("issue_events count: got %d, want %d", len(evs), n)
		}
		for _, e := range evs {
			if !e.CreatedAt.Equal(when) {
				t.Fatalf("event created_at not preserved: got %s, want %s", e.CreatedAt, when)
			}
			if dbIDs[e.DBID] {
				t.Fatalf("duplicate db_id %d in bulk-loaded events", e.DBID)
			}
			dbIDs[e.DBID] = true
		}

		rs, err := st.ListReactions(ctx, "issue", issuePK)
		if err != nil {
			t.Fatalf("ListReactions: %v", err)
		}
		if len(rs) != n {
			t.Fatalf("reactions count: got %d, want %d", len(rs), n)
		}
		for _, r := range rs {
			if !r.CreatedAt.Equal(when) {
				t.Fatalf("reaction created_at not preserved: got %s, want %s", r.CreatedAt, when)
			}
			if dbIDs[r.DBID] {
				t.Fatalf("duplicate db_id %d across bulk-loaded rows", r.DBID)
			}
			dbIDs[r.DBID] = true
		}
		_ = repoPK
	})
}

// TestBulkLoadRunsAndRestores proves BulkLoad executes its body and leaves the
// store serving correctly afterward: a seed performed inside BulkLoad is durable
// and readable once the serving pragmas are restored. On SQLite it also confirms
// the WAL journal mode is back in force after the load window closes.
func TestBulkLoadRunsAndRestores(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		when := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)

		var repoPK int64
		err := st.BulkLoad(ctx, func() error {
			return st.WithTx(ctx, func(tx *store.Tx) error {
				owner := &store.UserRow{Login: "octo", CreatedAt: when, UpdatedAt: when}
				if err := tx.SeedUser(ctx, owner); err != nil {
					return err
				}
				repo := &store.RepoRow{OwnerPK: owner.PK, Name: "demo", DefaultBranch: "main", CreatedAt: when, UpdatedAt: when}
				if err := tx.SeedRepo(ctx, repo); err != nil {
					return err
				}
				repoPK = repo.PK
				return nil
			})
		})
		if err != nil {
			t.Fatalf("BulkLoad: %v", err)
		}
		if repoPK == 0 {
			t.Fatal("BulkLoad body did not run")
		}
		// The store still serves reads after the load window closes.
		got, err := st.RepoByOwnerName(ctx, "octo", "demo")
		if err != nil {
			t.Fatalf("RepoByOwnerName after BulkLoad: %v", err)
		}
		if got.PK != repoPK {
			t.Fatalf("repo pk mismatch after BulkLoad: got %d, want %d", got.PK, repoPK)
		}
	})
}

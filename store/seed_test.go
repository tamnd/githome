package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/tamnd/githome/store"
)

// TestSeedPreservesNumbersAndTimestamps drives the bulk-seed write path end to
// end on a fresh database and asserts the two properties a corpus depends on:
// per-repo numbers are written verbatim (not reallocated), and created_at is
// stored in the form the read path returns unchanged. It also checks that the
// db_id sequence advances across kinds and that comment-count recomputation and
// the number-allocator reset land.
func TestSeedPreservesNumbersAndTimestamps(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		// A fixed instant in the past so we prove the timestamp is preserved
		// rather than stamped at insert time.
		when := time.Date(2019, 2, 20, 18, 27, 16, 0, time.UTC)

		var ownerPK, repoPK, issuePK int64
		err := st.WithTx(ctx, func(tx *store.Tx) error {
			owner := &store.UserRow{Login: "torvalds", CreatedAt: when, UpdatedAt: when}
			if err := tx.SeedUser(ctx, owner); err != nil {
				return err
			}
			ownerPK = owner.PK

			repo := &store.RepoRow{OwnerPK: owner.PK, Name: "linux", DefaultBranch: "master", CreatedAt: when, UpdatedAt: when}
			if err := tx.SeedRepo(ctx, repo); err != nil {
				return err
			}
			repoPK = repo.PK

			// Seed issue number 9521 directly, the way a corpus preserves a real
			// GitHub number rather than counting up from one.
			iss := &store.IssueRow{
				RepoPK: repo.PK, Number: 9521, Title: "a real issue",
				UserPK: owner.PK, State: "open", CreatedAt: when, UpdatedAt: when,
			}
			if err := tx.SeedIssue(ctx, iss); err != nil {
				return err
			}
			issuePK = iss.PK

			c := &store.CommentRow{IssuePK: iss.PK, UserPK: owner.PK, Body: "first", CreatedAt: when, UpdatedAt: when}
			if err := tx.SeedComment(ctx, c); err != nil {
				return err
			}
			ev := &store.IssueEventRow{RepoPK: repo.PK, IssuePK: iss.PK, ActorPK: &owner.PK, Event: "labeled", Payload: `{"label":{"name":"bug"}}`, CreatedAt: when}
			return tx.SeedIssueEvent(ctx, ev)
		})
		if err != nil {
			t.Fatalf("seed tx: %v", err)
		}
		if ownerPK == 0 || repoPK == 0 || issuePK == 0 {
			t.Fatalf("seed did not fill primary keys: owner=%d repo=%d issue=%d", ownerPK, repoPK, issuePK)
		}

		got, err := st.GetIssueByNumber(ctx, repoPK, 9521)
		if err != nil {
			t.Fatalf("GetIssueByNumber(9521): %v", err)
		}
		if !got.CreatedAt.Equal(when) {
			t.Errorf("created_at not preserved: got %s, want %s", got.CreatedAt, when)
		}
		if got.Title != "a real issue" {
			t.Errorf("title round-trip mismatch: %q", got.Title)
		}

		// db_id is allocated in insertion order, so the issue's id is strictly
		// greater than the owner's and the repo's.
		owner, err := st.UserByPK(ctx, ownerPK)
		if err != nil {
			t.Fatalf("UserByPK: %v", err)
		}
		if owner.DBID >= got.DBID {
			t.Errorf("db_id sequence did not advance across kinds: owner=%d issue=%d", owner.DBID, got.DBID)
		}

		// Recompute the denormalized comment count from the seeded rows.
		if err := st.RecomputeIssueCommentCounts(ctx, repoPK); err != nil {
			t.Fatalf("RecomputeIssueCommentCounts: %v", err)
		}
		got, err = st.GetIssueByNumber(ctx, repoPK, 9521)
		if err != nil {
			t.Fatalf("re-read: %v", err)
		}
		if got.CommentsCount != 1 {
			t.Errorf("comment count not recomputed: got %d, want 1", got.CommentsCount)
		}

		// The number allocator must be pushed past the seeded number so the next
		// live issue does not collide with 9521.
		if err := st.SetNextIssueNumber(ctx, repoPK, 9522); err != nil {
			t.Fatalf("SetNextIssueNumber: %v", err)
		}
		err = st.WithTx(ctx, func(tx *store.Tx) error {
			n, err := tx.AllocIssueNumber(ctx, repoPK)
			if err != nil {
				return err
			}
			if n != 9522 {
				t.Errorf("next allocated number = %d, want 9522", n)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("alloc tx: %v", err)
		}

		events, err := st.ListIssueEvents(ctx, issuePK)
		if err != nil {
			t.Fatalf("ListIssueEvents: %v", err)
		}
		if len(events) != 1 || events[0].Event != "labeled" {
			t.Fatalf("timeline not seeded as expected: %+v", events)
		}
		if !events[0].CreatedAt.Equal(when) {
			t.Errorf("event created_at not preserved: got %s", events[0].CreatedAt)
		}
	})
}

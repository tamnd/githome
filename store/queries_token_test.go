package store_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/tamnd/githome/store"
)

func TestTokensForUserAndDelete(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		userPK, err := st.InsertUserWithPassword(ctx, "octocat", "octo@example.com", "x")
		if err != nil {
			t.Fatalf("insert user: %v", err)
		}
		otherPK, err := st.InsertUserWithPassword(ctx, "hubber", "hub@example.com", "x")
		if err != nil {
			t.Fatalf("insert user: %v", err)
		}

		mint := func(owner int64, note, kind string) *store.TokenRow {
			h := sha256.Sum256([]byte(note))
			row := &store.TokenRow{
				UserPK:      &owner,
				TokenHash:   h[:],
				TokenPrefix: "ghp_",
				LastEight:   "deadbeef",
				Kind:        kind,
				Scopes:      "repo",
				Note:        note,
			}
			if err := st.InsertToken(ctx, row); err != nil {
				t.Fatalf("insert token %q: %v", note, err)
			}
			return row
		}

		first := mint(userPK, "first", "pat")
		second := mint(userPK, "second", "pat")
		mint(userPK, "device", "oauth") // not a PAT, must stay out of the list
		mint(otherPK, "theirs", "pat")  // someone else's, must stay out too

		rows, err := st.TokensForUser(ctx, userPK)
		if err != nil {
			t.Fatalf("TokensForUser: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("got %d tokens, want 2", len(rows))
		}
		// Newest first; created_at ties break on pk.
		if rows[0].PK != second.PK || rows[1].PK != first.PK {
			t.Errorf("order = %d, %d; want %d, %d", rows[0].PK, rows[1].PK, second.PK, first.PK)
		}

		// A user cannot delete a token they do not own.
		if err := st.DeleteUserToken(ctx, second.PK, otherPK); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("cross-user delete = %v, want ErrNotFound", err)
		}
		if err := st.DeleteUserToken(ctx, second.PK, userPK); err != nil {
			t.Fatalf("DeleteUserToken: %v", err)
		}
		if err := st.DeleteUserToken(ctx, second.PK, userPK); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("second delete = %v, want ErrNotFound", err)
		}

		rows, err = st.TokensForUser(ctx, userPK)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].PK != first.PK {
			t.Fatalf("after delete got %d rows, want only pk %d", len(rows), first.PK)
		}
	})
}

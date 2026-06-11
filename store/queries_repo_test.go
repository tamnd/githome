package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamnd/githome/store"
)

// seedRepo inserts an owner and a repository and returns the stored row.
func seedRepo(t *testing.T, st *store.Store, owner string, r *store.RepoRow) *store.RepoRow {
	t.Helper()
	ctx := context.Background()
	u := &store.UserRow{Login: owner, Type: "User"}
	if err := st.InsertUser(ctx, u); err != nil {
		t.Fatalf("InsertUser: %v", err)
	}
	r.OwnerPK = u.PK
	if err := st.InsertRepo(ctx, r); err != nil {
		t.Fatalf("InsertRepo: %v", err)
	}
	return r
}

func TestInsertRepoFillsServerFields(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		desc := "the test repo"
		r := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World", Description: &desc})

		if r.PK == 0 || r.DBID == 0 {
			t.Fatalf("server fields not filled: pk=%d db_id=%d", r.PK, r.DBID)
		}
		if r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
			t.Fatalf("timestamps not filled: created=%v updated=%v", r.CreatedAt, r.UpdatedAt)
		}
		// Defaults from 0003 backfill: feature flags on, state flags off.
		if !r.HasIssues || !r.HasProjects || !r.HasWiki || !r.HasDownloads {
			t.Errorf("feature flags should default on: %+v", r)
		}
		if r.Archived || r.Disabled || r.IsTemplate {
			t.Errorf("state flags should default off: %+v", r)
		}
		if r.DefaultBranch != "main" {
			t.Errorf("default_branch = %q, want main", r.DefaultBranch)
		}
	})
}

func TestRepoByOwnerName(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		pushed := time.Now().UTC().Truncate(time.Second)
		want := seedRepo(t, st, "octocat", &store.RepoRow{
			Name:     "Hello-World",
			Private:  true,
			PushedAt: &pushed,
		})

		// Lookup is case-insensitive on both owner and repo name.
		got, err := st.RepoByOwnerName(ctx, "OCTOCAT", "hello-world")
		if err != nil {
			t.Fatalf("RepoByOwnerName: %v", err)
		}
		if got.PK != want.PK || got.DBID != want.DBID {
			t.Fatalf("got pk=%d db_id=%d, want pk=%d db_id=%d", got.PK, got.DBID, want.PK, want.DBID)
		}
		if !got.Private {
			t.Errorf("private flag not round-tripped")
		}
		if got.PushedAt == nil || !got.PushedAt.Equal(pushed) {
			t.Errorf("pushed_at = %v, want %v", got.PushedAt, pushed)
		}
		if got.Name != "Hello-World" {
			t.Errorf("name = %q, want Hello-World (case preserved)", got.Name)
		}
	})
}

func TestRepoLookupNotFound(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if _, err := st.RepoByOwnerName(ctx, "ghost", "nope"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("RepoByOwnerName(missing): err = %v, want ErrNotFound", err)
		}
		if _, err := st.RepoByPK(ctx, 999999); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("RepoByPK(missing): err = %v, want ErrNotFound", err)
		}
		if _, err := st.RepoByDBID(ctx, 999999); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("RepoByDBID(missing): err = %v, want ErrNotFound", err)
		}
	})
}

func TestRepoByPKAndDBID(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		want := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})

		byPK, err := st.RepoByPK(ctx, want.PK)
		if err != nil {
			t.Fatalf("RepoByPK: %v", err)
		}
		byDBID, err := st.RepoByDBID(ctx, want.DBID)
		if err != nil {
			t.Fatalf("RepoByDBID: %v", err)
		}
		if byPK.DBID != want.DBID || byDBID.PK != want.PK {
			t.Fatalf("pk/db_id lookups disagree: byPK=%+v byDBID=%+v", byPK, byDBID)
		}
	})
}

func TestRepoRedirects(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		r := seedRepo(t, st, "octocat", &store.RepoRow{Name: "New-Name"})

		// No redirect yet: a stale name misses.
		if _, err := st.RepoByRedirect(ctx, "octocat", "old-name"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("before upsert: err = %v, want ErrNotFound", err)
		}

		if err := st.UpsertRepoRedirect(ctx, "Octocat", "Old-Name", r.PK); err != nil {
			t.Fatalf("UpsertRepoRedirect: %v", err)
		}

		// The lookup is case-insensitive on both sides: the keys were stored
		// lowercased and the query lowercases its inputs.
		got, err := st.RepoByRedirect(ctx, "OCTOCAT", "old-name")
		if err != nil {
			t.Fatalf("RepoByRedirect: %v", err)
		}
		if got.PK != r.PK || got.Name != "New-Name" {
			t.Fatalf("redirect resolved to pk=%d name=%q, want pk=%d New-Name", got.PK, got.Name, r.PK)
		}

		// Rebinding the same old name repoints it: the chain collapses to the
		// latest target instead of erroring on the unique key.
		other := &store.RepoRow{OwnerPK: r.OwnerPK, Name: "Other"}
		if err := st.InsertRepo(ctx, other); err != nil {
			t.Fatalf("InsertRepo other: %v", err)
		}
		if err := st.UpsertRepoRedirect(ctx, "octocat", "old-name", other.PK); err != nil {
			t.Fatalf("UpsertRepoRedirect repoint: %v", err)
		}
		got, err = st.RepoByRedirect(ctx, "octocat", "old-name")
		if err != nil {
			t.Fatalf("RepoByRedirect after repoint: %v", err)
		}
		if got.PK != other.PK {
			t.Fatalf("repointed redirect resolved to pk=%d, want %d", got.PK, other.PK)
		}

		// A deleted target makes the redirect miss rather than resolve to a
		// soft-deleted row.
		if err := st.SoftDeleteRepo(ctx, other.PK); err != nil {
			t.Fatalf("SoftDeleteRepo: %v", err)
		}
		if _, err := st.RepoByRedirect(ctx, "octocat", "old-name"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("deleted target: err = %v, want ErrNotFound", err)
		}
	})
}

func TestUpdateRepoReturnsUpdatedRow(t *testing.T) {
	// Regression: the RETURNING clause used the r-aliased column list on an
	// unaliased UPDATE, which SQLite rejected with "no such column: r.pk".
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		r := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})

		desc := "now with a description"
		archived := true
		got, err := st.UpdateRepo(ctx, r.PK, store.RepoPatch{Description: &desc, Archived: &archived})
		if err != nil {
			t.Fatalf("UpdateRepo: %v", err)
		}
		if got.Description == nil || *got.Description != desc {
			t.Errorf("Description = %v, want %q", got.Description, desc)
		}
		if !got.Archived {
			t.Error("Archived not applied")
		}
		if got.Name != "Hello-World" {
			t.Errorf("untouched Name changed: %q", got.Name)
		}
		if !got.UpdatedAt.After(r.UpdatedAt) && !got.UpdatedAt.Equal(r.UpdatedAt) {
			t.Errorf("UpdatedAt went backwards: %v -> %v", r.UpdatedAt, got.UpdatedAt)
		}

		if _, err := st.UpdateRepo(ctx, r.PK+999, store.RepoPatch{Description: &desc}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("missing repo: err = %v, want ErrNotFound", err)
		}
	})
}

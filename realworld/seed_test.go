package realworld

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tamnd/githome/store"
)

// openSeededStore opens a fresh migrated SQLite store for a seed test.
func openSeededStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "githome.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return st
}

func TestSeedCorpusPreservesShape(t *testing.T) {
	ctx := context.Background()
	st := openSeededStore(t)
	c := sampleCorpus()

	reactor := ReactorPool{Size: 8, Seed: 0x6e7e}
	res, err := SeedCorpus(ctx, st, c, reactor)
	if err != nil {
		t.Fatalf("SeedCorpus: %v", err)
	}

	// The two issues, the one pull, and the three timeline events all land.
	if res.Rows["issues"] != 2 || res.Rows["pull_requests"] != 1 || res.Rows["timeline_events"] != 3 {
		t.Fatalf("row counts wrong: %+v", res.Rows)
	}
	if res.Rows["comments"] != 1 || res.Rows["reviews"] != 1 || res.Rows["review_comments"] != 1 {
		t.Fatalf("conversation rows wrong: %+v", res.Rows)
	}
	if res.Rows["commit_statuses"] != 2 {
		t.Fatalf("status rows wrong: %+v", res.Rows)
	}
	if len(res.Dropped) != 0 {
		t.Fatalf("nothing should drop with an 8-reactor pool here: %+v", res.Dropped)
	}

	// The preserved per-repo issue numbers must survive verbatim, and the number
	// allocator must point past them.
	repo, err := st.RepoByOwnerName(ctx, c.Repo.Owner, c.Repo.Name)
	if err != nil {
		t.Fatalf("RepoByOwnerName: %v", err)
	}
	iss, err := st.GetIssueByNumber(ctx, repo.PK, 100)
	if err != nil {
		t.Fatalf("GetIssueByNumber 100: %v", err)
	}
	if !iss.CreatedAt.Equal(c.Issues[0].CreatedAt) {
		t.Errorf("issue created_at not preserved: got %v want %v", iss.CreatedAt, c.Issues[0].CreatedAt)
	}
	if iss.CommentsCount != 1 {
		t.Errorf("comment count not recomputed: got %d", iss.CommentsCount)
	}
}

func TestSeedCorpusCapsReactionsToPool(t *testing.T) {
	ctx := context.Background()
	st := openSeededStore(t)
	c := sampleCorpus()
	// Ask for more +1 reactions than a 2-reactor pool can supply.
	c.Issues[0].Reactions = map[string]int{"+1": 5}

	res, err := SeedCorpus(ctx, st, c, ReactorPool{Size: 2, Seed: 1})
	if err != nil {
		t.Fatalf("SeedCorpus: %v", err)
	}
	var capped *DropNote
	for i := range res.Dropped {
		if res.Dropped[i].What == "reaction" {
			capped = &res.Dropped[i]
		}
	}
	if capped == nil {
		t.Fatalf("a reaction count over the pool size must be recorded as a drop: %+v", res.Dropped)
	}
	if capped.Count != 3 {
		t.Errorf("dropped shortfall wrong: got %d want 3", capped.Count)
	}
}

func TestSeedCorpusIsDeterministic(t *testing.T) {
	ctx := context.Background()
	reactor := ReactorPool{Size: 8, Seed: 0x6e7e}

	run := func() *SeedResult {
		st := openSeededStore(t)
		res, err := SeedCorpus(ctx, st, sampleCorpus(), reactor)
		if err != nil {
			t.Fatalf("SeedCorpus: %v", err)
		}
		return res
	}
	a, b := run(), run()
	if a.RepoPK != b.RepoPK {
		t.Errorf("repo pk not deterministic: %d vs %d", a.RepoPK, b.RepoPK)
	}
	for k, v := range a.Rows {
		if b.Rows[k] != v {
			t.Errorf("row count %q not deterministic: %d vs %d", k, v, b.Rows[k])
		}
	}
}

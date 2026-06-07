package realworld

import (
	"context"
	"testing"
)

// TestSeedSnapshotMatchesSeedCorpus proves the streaming seed path produces the
// same database as the in-memory path: it writes a corpus to a snapshot, seeds
// it both ways into separate stores, and asserts the measured row counts and the
// repo pk match. The streaming path loads one table at a time off disk, so this
// is the guard that bounding memory did not change what lands.
func TestSeedSnapshotMatchesSeedCorpus(t *testing.T) {
	ctx := context.Background()
	reactor := ReactorPool{Size: 8, Seed: 0x6e7e}
	c := sampleCorpus()

	root := t.TempDir()
	if err := WriteCorpus(root, c); err != nil {
		t.Fatalf("WriteCorpus: %v", err)
	}

	memStore := openSeededStore(t)
	memRes, err := SeedCorpus(ctx, memStore, sampleCorpus(), reactor)
	if err != nil {
		t.Fatalf("SeedCorpus: %v", err)
	}

	streamStore := openSeededStore(t)
	streamRes, err := SeedSnapshot(ctx, streamStore, root, c.Repo, reactor)
	if err != nil {
		t.Fatalf("SeedSnapshot: %v", err)
	}

	if memRes.RepoPK != streamRes.RepoPK {
		t.Errorf("repo pk differs: in-memory %d, streaming %d", memRes.RepoPK, streamRes.RepoPK)
	}
	for k, v := range memRes.Rows {
		if streamRes.Rows[k] != v {
			t.Errorf("row count %q differs: in-memory %d, streaming %d", k, v, streamRes.Rows[k])
		}
	}
	for k, v := range streamRes.Rows {
		if memRes.Rows[k] != v {
			t.Errorf("row count %q only in streaming: %d", k, v)
		}
	}

	// A preserved per-repo number must survive the streaming path verbatim, with
	// its real timestamp and recomputed comment count.
	repo, err := streamStore.RepoByOwnerName(ctx, c.Repo.Owner, c.Repo.Name)
	if err != nil {
		t.Fatalf("RepoByOwnerName: %v", err)
	}
	iss, err := streamStore.GetIssueByNumber(ctx, repo.PK, 100)
	if err != nil {
		t.Fatalf("GetIssueByNumber 100: %v", err)
	}
	if !iss.CreatedAt.Equal(c.Issues[0].CreatedAt) {
		t.Errorf("streaming seed lost issue created_at: got %v want %v", iss.CreatedAt, c.Issues[0].CreatedAt)
	}
	if iss.CommentsCount != 1 {
		t.Errorf("streaming seed did not recompute comment count: got %d", iss.CommentsCount)
	}
}

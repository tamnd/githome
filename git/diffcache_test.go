package git

import (
	"context"
	"strings"
	"testing"
)

func TestDiffCachePutGetEvict(t *testing.T) {
	c := newDiffCache(1 << 10)
	a := []FileChange{{Path: "a.txt", Patch: "small"}}
	c.put("k1", a)
	if got := c.get("k1"); len(got) != 1 || got[0].Path != "a.txt" {
		t.Fatalf("get after put: %#v", got)
	}
	if c.hits != 1 {
		t.Fatalf("hits = %d, want 1", c.hits)
	}
	if c.get("absent") != nil {
		t.Fatal("absent key should miss")
	}

	// A new entry bigger than the remaining budget evicts the cold one.
	big := []FileChange{{Path: "big", Patch: strings.Repeat("x", 700)}}
	c.put("k2", big)
	if c.get("k1") != nil {
		t.Fatal("cold entry should have been evicted")
	}
	if c.get("k2") == nil {
		t.Fatal("warm entry should survive")
	}

	// An entry over the per-entry cap is never stored.
	huge := []FileChange{{Path: "huge", Patch: strings.Repeat("x", diffCacheMaxEntryBytes)}}
	c.put("k3", huge)
	if c.get("k3") != nil {
		t.Fatal("over-cap entry must not be cached")
	}
}

func TestIsFullSHA(t *testing.T) {
	full := strings.Repeat("a1", 20)
	if !isFullSHA(full) {
		t.Fatalf("%q should be a full sha", full)
	}
	for _, bad := range []string{"", "main", "abc123", full[:39], full + "0", strings.Repeat("g", 40)} {
		if isFullSHA(bad) {
			t.Fatalf("%q should not be a full sha", bad)
		}
	}
}

// TestChangedFilesServedFromCache proves the hit path short-circuits the git
// subprocess: a seeded entry is returned for a repository that does not even
// exist on disk, so a hit can never have forked.
func TestChangedFilesServedFromCache(t *testing.T) {
	s := NewStore(t.TempDir())
	base := strings.Repeat("a", 40)
	head := strings.Repeat("b", 40)
	want := []FileChange{{Path: "cached.go", Status: "modified", Additions: 1}}
	s.diffs.put(diffKey(99, base, head), want)

	got, err := s.ChangedFiles(context.Background(), 99, base, head)
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if len(got) != 1 || got[0].Path != "cached.go" {
		t.Fatalf("ChangedFiles = %#v, want the cached entry", got)
	}

	// A non-sha ref never reads the cache (and here fails on the missing repo),
	// because a moving name must not key a content-addressed entry.
	if _, err := s.ChangedFiles(context.Background(), 99, "main", head); err == nil {
		t.Fatal("branch-name range must bypass the cache")
	}

	// The direct (two-dot) form answers differently for the same end pair, so
	// it must never serve the three-dot entry: its key carries a prefix, and
	// here the miss falls through to the missing repo and fails.
	if _, err := s.ChangedFilesDirect(context.Background(), 99, base, head); err == nil {
		t.Fatal("direct diff must not serve the three-dot cache entry")
	}
}

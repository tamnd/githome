package realworld

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManifestRoundTrip(t *testing.T) {
	m := NewManifest("rw-smoke", "rev-abc123")
	m.Note = "smoke corpus"
	m.Repos = []RepoManifest{{
		Repo:       RepoRef{Owner: "torvalds", Name: "linux", DefaultBranch: "master", PinnedSHA: "deadbeef"},
		Provenance: Official,
		Rows:       map[string]int{"issues": 3, "timeline_events": 12},
	}}
	m.Drop("timeline_events", "sampled to the smoke range", 9000)

	path := filepath.Join(t.TempDir(), "realworld-manifest.json")
	if err := m.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if got.DatasetRevision != "rev-abc123" || got.FixtureTier != "rw-smoke" {
		t.Errorf("pins not preserved: %+v", got)
	}
	if got.Reactor.Size != DefaultReactorPool.Size {
		t.Errorf("reactor pool default not preserved: %+v", got.Reactor)
	}
	if len(got.Dropped) != 1 || got.Dropped[0].Count != 9000 {
		t.Errorf("drop note not preserved: %+v", got.Dropped)
	}
	if names := got.RepoNames(); len(names) != 1 || names[0] != "torvalds/linux" {
		t.Errorf("RepoNames: %v", names)
	}
}

func TestLoadManifestRejectsWrongSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.json")
	if err := os.WriteFile(path, []byte(`{"schema":999,"dataset_revision":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(path); err == nil {
		t.Fatal("expected schema mismatch to be rejected")
	}
}

func TestCorpusLoginsFirstSeenOrder(t *testing.T) {
	now := time.Now().UTC()
	c := &Corpus{
		Repo: RepoRef{Owner: "torvalds", Name: "linux"},
		Issues: []Issue{
			{Number: 1, Author: "alice", Assignees: []string{"bob", "alice"}, CreatedAt: now, UpdatedAt: now},
		},
		Comments:       []Comment{{ID: 1, IssueNumber: 1, Author: "carol", CreatedAt: now, UpdatedAt: now}},
		TimelineEvents: []TimelineEvent{{ID: 1, IssueNumber: 1, EventType: "labeled", Actor: "k8s-bot", CreatedAt: now}},
	}
	got := c.Logins()
	want := []string{"torvalds", "alice", "bob", "carol", "k8s-bot"}
	if len(got) != len(want) {
		t.Fatalf("Logins = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Logins[%d] = %q, want %q (full %v)", i, got[i], want[i], got)
		}
	}
}

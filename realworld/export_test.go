package realworld

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// sampleCorpus builds a tiny but structurally complete corpus for the export and
// seed tests: one issue, one pull request with a review and an inline comment, a
// dense timeline, and a status, all referencing a handful of logins.
func sampleCorpus() *Corpus {
	t0 := time.Date(2020, 3, 1, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := t0.Add(d); return &v }
	line := int64(42)
	return &Corpus{
		Repo: RepoRef{Owner: "kubernetes", Name: "kubernetes", DefaultBranch: "master", PinnedSHA: "abc123"},
		Issues: []Issue{
			{
				Number: 100, Title: "a bug", Body: "broken", State: "open", Author: "alice",
				Labels: []Label{{Name: "kind/bug", Color: "d73a4a"}}, Assignees: []string{"bob"},
				Reactions: map[string]int{"+1": 3, "heart": 1}, CommentCount: 1,
				CreatedAt: t0, UpdatedAt: *at(time.Hour),
			},
			{
				Number: 101, IsPullRequest: true, Title: "fix the bug", State: "closed", Author: "bob",
				CreatedAt: *at(time.Hour), UpdatedAt: *at(3 * time.Hour), ClosedAt: at(3 * time.Hour),
			},
		},
		PullRequests: []PullRequest{
			{Number: 101, Merged: true, MergedAt: at(3 * time.Hour), MergedBy: "carol",
				MergeCommitSHA: "deadbeef", BaseRef: "master", HeadRef: "fix", HeadSHA: "cafe",
				Additions: 10, Deletions: 2, ChangedFiles: 1},
		},
		Comments: []Comment{
			{ID: 5001, IssueNumber: 100, Author: "carol", Body: "confirmed", CreatedAt: *at(30 * time.Minute), UpdatedAt: *at(30 * time.Minute)},
		},
		Reviews: []Review{
			{ID: 7001, PRNumber: 101, Author: "carol", State: "APPROVED", Body: "lgtm", SubmittedAt: at(2 * time.Hour), CommitID: "cafe"},
		},
		ReviewComments: []ReviewComment{
			{ID: 9001, PRNumber: 101, ReviewID: 7001, Author: "carol", Body: "nit", Path: "main.go", Line: &line, Side: "RIGHT", CreatedAt: *at(2 * time.Hour), UpdatedAt: *at(2 * time.Hour)},
		},
		TimelineEvents: []TimelineEvent{
			{ID: 11001, IssueNumber: 100, EventType: "labeled", Actor: "k8s-ci-bot", LabelName: "kind/bug", LabelColor: "d73a4a", CreatedAt: *at(time.Minute)},
			{ID: 11002, IssueNumber: 100, EventType: "assigned", Actor: "k8s-ci-bot", Assignee: "bob", CreatedAt: *at(2 * time.Minute)},
			{ID: 11003, IssueNumber: 101, EventType: "merged", Actor: "carol", CreatedAt: *at(3 * time.Hour)},
		},
		PRFiles: []PRFile{
			{PRNumber: 101, Path: "main.go", Additions: 10, Deletions: 2, Status: "modified"},
		},
		CommitStatuses: []CommitStatus{
			{SHA: "cafe", Context: "ci/build", State: "success", CreatedAt: *at(2*time.Hour + 30*time.Minute)},
			{SHA: "cafe", Context: "ci/test", State: "success", CreatedAt: *at(2*time.Hour + 31*time.Minute)},
		},
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	root := t.TempDir()
	want := sampleCorpus()
	if err := WriteCorpus(root, want); err != nil {
		t.Fatalf("WriteCorpus: %v", err)
	}
	got, err := ReadCorpus(root, want.Repo)
	if err != nil {
		t.Fatalf("ReadCorpus: %v", err)
	}
	if len(got.Issues) != 2 || len(got.PullRequests) != 1 || len(got.TimelineEvents) != 3 {
		t.Fatalf("row counts changed across round trip: %+v", got.rowCounts())
	}
	if got.Issues[0].Reactions["+1"] != 3 {
		t.Errorf("reaction count lost: %+v", got.Issues[0].Reactions)
	}
	if !got.Issues[1].ClosedAt.Equal(*want.Issues[1].ClosedAt) {
		t.Errorf("closed_at not preserved")
	}
	if got.Repo.PinnedSHA != "abc123" {
		t.Errorf("repo pin lost: %+v", got.Repo)
	}
}

func TestExportToSnapshotWritesManifest(t *testing.T) {
	// Seed a snapshot, then re-export it through the FixtureExporter into a new
	// snapshot, which exercises ExportToSnapshot end to end offline.
	src := t.TempDir()
	c := sampleCorpus()
	if err := WriteCorpus(src, c); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	m := NewManifest("rw-smoke", "fixture")
	ex := FixtureExporter{Root: src}
	if err := ExportToSnapshot(context.Background(), ex, []RepoRef{c.Repo}, m, dst); err != nil {
		t.Fatalf("ExportToSnapshot: %v", err)
	}
	loaded, err := LoadManifest(filepath.Join(dst, ManifestName))
	if err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	if len(loaded.Repos) != 1 || loaded.Repos[0].Rows["timeline_events"] != 3 {
		t.Errorf("manifest row counts wrong: %+v", loaded.Repos)
	}
}

func TestExportToSnapshotRecordsUnreachableSourceAsDrop(t *testing.T) {
	dst := t.TempDir()
	m := NewManifest("rw-smoke", "none")
	if err := ExportToSnapshot(context.Background(), offlineExporter{}, []RepoRef{{Owner: "x", Name: "y"}}, m, dst); err != nil {
		t.Fatalf("ExportToSnapshot should not fail on an unreachable source: %v", err)
	}
	if len(m.Dropped) != 1 {
		t.Fatalf("unreachable source not recorded as a drop: %+v", m.Dropped)
	}
}

type offlineExporter struct{}

func (offlineExporter) Export(context.Context, RepoRef) (*Corpus, error) {
	return nil, ErrRequiresNetwork
}
func (offlineExporter) Source() string { return "offline-test" }

func TestRateBudgetRefusesOverspend(t *testing.T) {
	b := NewRateBudget(10)
	if !b.Spend(7) || b.Remaining() != 3 {
		t.Fatalf("first spend wrong: spent=%d remaining=%d", b.Spent(), b.Remaining())
	}
	if b.Spend(5) {
		t.Fatal("budget should refuse a spend it cannot cover")
	}
	if b.Spent() != 7 {
		t.Errorf("a refused spend must not charge: spent=%d", b.Spent())
	}
}

func TestCheckpointResume(t *testing.T) {
	ref := RepoRef{Owner: "a", Name: "b"}
	ck := NewCheckpoint()
	if ck.IsDone(ref, "issues") {
		t.Fatal("nothing should be done on a fresh checkpoint")
	}
	ck.Mark(ref, "issues")
	if !ck.IsDone(ref, "issues") || ck.IsDone(ref, "comments") {
		t.Fatal("checkpoint did not record the right pair")
	}
}

func TestGitMirrorPlanCommands(t *testing.T) {
	p := GitMirrorPlan{Ref: RepoRef{Owner: "torvalds", Name: "linux", DefaultBranch: "master"}, PinnedSHA: "deadbeef"}
	cmds := p.Commands("/git/store/repo-7.git")
	if cmds[0][0] != "git" || cmds[0][1] != "clone" || cmds[0][2] != "--mirror" {
		t.Fatalf("first command is not a mirror clone: %v", cmds[0])
	}
	last := cmds[len(cmds)-1]
	if last[len(last)-1] != "deadbeef" {
		t.Errorf("pin not applied to the advertised tip: %v", last)
	}
}

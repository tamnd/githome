package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tamnd/githome/realworld"
)

// tinyCorpus builds a one-issue, one-PR corpus to drive the offline pipeline.
func tinyCorpus() *realworld.Corpus {
	t0 := time.Date(2021, 6, 1, 9, 0, 0, 0, time.UTC)
	merged := t0.Add(time.Hour)
	return &realworld.Corpus{
		Repo: realworld.RepoRef{Owner: "octo", Name: "demo", DefaultBranch: "main"},
		Issues: []realworld.Issue{
			{Number: 1, Title: "first", Body: "hi", State: "open", Author: "alice", CreatedAt: t0, UpdatedAt: t0},
			{Number: 2, IsPullRequest: true, Title: "a pr", State: "closed", Author: "bob", CreatedAt: t0, UpdatedAt: merged, ClosedAt: &merged},
		},
		PullRequests: []realworld.PullRequest{
			{Number: 2, Merged: true, MergedAt: &merged, MergedBy: "alice", BaseRef: "main", HeadRef: "f", HeadSHA: "abc"},
		},
		Comments: []realworld.Comment{
			{ID: 10, IssueNumber: 1, Author: "bob", Body: "thanks", CreatedAt: t0, UpdatedAt: t0},
		},
	}
}

func TestPipelineExportThenSeedOffline(t *testing.T) {
	from := t.TempDir()
	if err := realworld.WriteCorpus(from, tinyCorpus()); err != nil {
		t.Fatalf("WriteCorpus: %v", err)
	}

	data := t.TempDir()
	dsn := "sqlite://" + filepath.Join(t.TempDir(), "githome.db")
	args := []string{"-stage", "all", "-data", data, "-from", from, "-db", dsn, "-tier", "rw-smoke"}
	if err := run(args); err != nil {
		t.Fatalf("run: %v", err)
	}

	m, err := realworld.LoadManifest(filepath.Join(data, realworld.ManifestName))
	if err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	if m.FixtureTier != "rw-smoke" || len(m.Repos) != 1 {
		t.Fatalf("manifest wrong: tier=%q repos=%d", m.FixtureTier, len(m.Repos))
	}
	r := m.Repos[0]
	if r.Repo.NWO() != "octo/demo" {
		t.Errorf("wrong repo seeded: %q", r.Repo.NWO())
	}
	if r.Rows["issues"] != 2 || r.Rows["pull_requests"] != 1 || r.Rows["comments"] != 1 {
		t.Errorf("measured rows wrong: %v", r.Rows)
	}
	if r.Provenance != realworld.Official {
		t.Errorf("non-pseudonymized seed should be OFFICIAL, got %q", r.Provenance)
	}
}

func TestSeedRequiresData(t *testing.T) {
	if err := run([]string{"-stage", "seed"}); err == nil {
		t.Fatal("seed without -data should fail")
	}
}

func TestExportNeedsSourceOrNetwork(t *testing.T) {
	err := run([]string{"-stage", "export", "-data", t.TempDir()})
	if err == nil {
		t.Fatal("export without -from should report the network requirement")
	}
}

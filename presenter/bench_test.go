package presenter

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/tamnd/githome/config"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
)

// benchBuilder returns a URLBuilder pointing at a stable test host.
func benchBuilder(b *testing.B) *URLBuilder {
	b.Helper()
	must := func(s string) *url.URL {
		u, err := url.Parse(s)
		if err != nil {
			b.Fatalf("parse %q: %v", s, err)
		}
		return u
	}
	return NewURLBuilder(config.URLs{
		API:     must("https://git.example.com/api/v3"),
		HTML:    must("https://git.example.com"),
		GraphQL: must("https://git.example.com/api/graphql"),
		SSHHost: "git.example.com",
		SSHPort: 22,
	})
}

var (
	benchNow  = time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	benchDesc = "a handy benchmarking tool"
	benchBody = "This is the issue body text used for benchmarking the presenter layer."
)

// sampleUser returns a fully-populated domain user.
func sampleUser(id int64, login string) *domain.User {
	return &domain.User{
		ID:    id,
		Login: login,
		Type:  "User",
	}
}

// sampleIssue returns a fully-populated domain issue with labels, an assignee,
// a milestone, and a non-zero reaction rollup.
func sampleIssue() *domain.Issue {
	return &domain.Issue{
		PK:          1,
		ID:          42,
		RepoPK:      10,
		RepoID:      100,
		Number:      7,
		Title:       "Fix the flaky benchmark in CI",
		Body:        &benchBody,
		State:       "open",
		StateReason: nil,
		Locked:      false,
		User:        sampleUser(1, "octocat"),
		Assignees:   []*domain.User{sampleUser(2, "hubot")},
		Labels: []*domain.Label{
			{ID: 10, Name: "bug", Color: "d73a4a", Default: true, Description: &benchDesc},
			{ID: 11, Name: "enhancement", Color: "a2eeef", Default: false},
		},
		Milestone: &domain.Milestone{
			ID:           5,
			Number:       1,
			Title:        "v1.0",
			Description:  &benchDesc,
			State:        "open",
			Creator:      sampleUser(1, "octocat"),
			OpenIssues:   3,
			ClosedIssues: 1,
			CreatedAt:    benchNow,
			UpdatedAt:    benchNow,
		},
		ClosedBy:      nil,
		CommentsCount: 3,
		Reactions: domain.ReactionRollup{
			TotalCount: 5,
			Counts:     map[string]int{"+1": 3, "heart": 2},
		},
		CreatedAt: benchNow,
		UpdatedAt: benchNow,
	}
}

// samplePR returns a fully-populated domain pull request with head/base refs.
func samplePR() *domain.PullRequest {
	repo := &domain.Repo{
		PK:            10,
		ID:            100,
		Owner:         sampleUser(1, "octocat"),
		Name:          "hello",
		DefaultBranch: "main",
		HasIssues:     true,
		CreatedAt:     benchNow,
		UpdatedAt:     benchNow,
	}
	merged := false
	mergeableBool := true
	rebaseableBool := true
	return &domain.PullRequest{
		PK:            20,
		ID:            99,
		IssueID:       42,
		Number:        3,
		RepoPK:        10,
		Repo:          repo,
		Title:         "Add benchmarks for presenter package",
		Body:          &benchBody,
		State:         "open",
		Locked:        false,
		User:          sampleUser(1, "octocat"),
		Assignees:     []*domain.User{sampleUser(2, "hubot")},
		Labels:        []*domain.Label{{ID: 10, Name: "enhancement", Color: "a2eeef"}},
		CommentsCount: 2,
		Draft:         false,
		Merged:        merged,
		Mergeable:     &mergeableBool,
		MergeableState: "clean",
		Rebaseable:    &rebaseableBool,
		Additions:     12,
		Deletions:     4,
		ChangedFiles:  2,
		CommitsCount:  1,
		Head: domain.GitEndpoint{
			Label: "octocat:bench-presenter",
			Ref:   "bench-presenter",
			SHA:   "deadbeef1234567890abcdef1234567890abcdef",
			Repo:  repo,
			User:  sampleUser(1, "octocat"),
		},
		Base: domain.GitEndpoint{
			Label: "octocat:main",
			Ref:   "main",
			SHA:   "cafebabe1234567890abcdef1234567890abcdef",
			Repo:  repo,
			User:  sampleUser(1, "octocat"),
		},
		CreatedAt: benchNow,
		UpdatedAt: benchNow,
	}
}

// BenchmarkPresentIssue measures rendering one fully-populated issue.
// Target: <= 3 µs/op, <= 25 allocs/op.
func BenchmarkPresentIssue(b *testing.B) {
	builder := benchBuilder(b)
	iss := sampleIssue()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = builder.Issue("octocat", "hello", iss, nodeid.FormatNew)
	}
}

// BenchmarkPresentIssueList_30 measures rendering a page of 30 issues.
// Target: <= 80 µs/op.
func BenchmarkPresentIssueList_30(b *testing.B) {
	builder := benchBuilder(b)
	issues := make([]*domain.Issue, 30)
	for i := range issues {
		iss := sampleIssue()
		iss.ID = int64(i + 1)
		iss.Number = int64(i + 1)
		issues[i] = iss
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, iss := range issues {
			_ = builder.Issue("octocat", "hello", iss, nodeid.FormatNew)
		}
	}
}

// BenchmarkPresentPR measures rendering one fully-populated pull request (detail view).
// Target: <= 5 µs/op.
func BenchmarkPresentPR(b *testing.B) {
	builder := benchBuilder(b)
	pr := samplePR()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = builder.PullRequest("octocat", "hello", pr, nodeid.FormatNew, true)
	}
}

// BenchmarkMarshalIssueList_30 measures json.Marshal on a pre-rendered page of 30 issues.
// Target: <= 120 µs/op.
func BenchmarkMarshalIssueList_30(b *testing.B) {
	builder := benchBuilder(b)
	issues := make([]*domain.Issue, 30)
	for i := range issues {
		iss := sampleIssue()
		iss.ID = int64(i + 1)
		iss.Number = int64(i + 1)
		issues[i] = iss
	}
	rendered := make([]any, 30)
	for i, iss := range issues {
		rendered[i] = builder.Issue("octocat", "hello", iss, nodeid.FormatNew)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, err := json.Marshal(rendered)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkURLBuilder_issue measures URL construction for one issue via RepoAPI + string concat.
// Target: <= 1 µs/op, 0 allocs.
func BenchmarkURLBuilder_issue(b *testing.B) {
	builder := benchBuilder(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		base := builder.RepoAPI("octocat", "hello")
		_ = base + "/issues/42"
	}
}

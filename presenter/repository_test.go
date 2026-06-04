package presenter

import (
	"net/url"
	"testing"
	"time"

	"github.com/tamnd/githome/config"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/nodeid"
)

func testBuilder(t *testing.T) *URLBuilder {
	t.Helper()
	must := func(s string) *url.URL {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return u
	}
	return NewURLBuilder(config.URLs{
		API:     must("https://git.test.internal/api/v3"),
		HTML:    must("https://git.test.internal"),
		GraphQL: must("https://git.test.internal/api/graphql"),
		SSHHost: "git.test.internal",
		SSHPort: 22,
	})
}

func sampleRepo() *domain.Repo {
	desc := "the hello repo"
	pushed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return &domain.Repo{
		PK: 5, ID: 50,
		Owner:         &domain.User{ID: 100, Login: "octocat", Type: "User"},
		Name:          "hello",
		Description:   &desc,
		DefaultBranch: "main",
		HasIssues:     true, HasProjects: true, HasWiki: true, HasDownloads: true,
		OpenIssuesCount: 3,
		PushedAt:        &pushed,
	}
}

func TestRepositoryRender(t *testing.T) {
	b := testBuilder(t)
	r := b.Repository(sampleRepo(), nodeid.FormatNew, OwnerPermissions())

	if r.ID != 50 || r.FullName != "octocat/hello" || r.Owner.Login != "octocat" {
		t.Fatalf("identity = %+v", r)
	}
	if got := r.NodeID[:2]; got != "R_" {
		t.Errorf("node_id prefix = %q, want R_", got)
	}
	if r.URL != "https://git.test.internal/api/v3/repos/octocat/hello" {
		t.Errorf("url = %q", r.URL)
	}
	if r.ContentsURL != "https://git.test.internal/api/v3/repos/octocat/hello/contents/{+path}" {
		t.Errorf("contents_url = %q", r.ContentsURL)
	}
	if r.GitURL != "git://git.test.internal/octocat/hello.git" {
		t.Errorf("git_url = %q", r.GitURL)
	}
	if r.SSHURL != "git@git.test.internal:octocat/hello.git" {
		t.Errorf("ssh_url = %q", r.SSHURL)
	}
	if r.Visibility != "public" || r.Private {
		t.Errorf("visibility = %q private = %v", r.Visibility, r.Private)
	}
	if r.PushedAt == nil {
		t.Error("pushed_at should be set")
	}
	if r.Language != nil || r.License != nil || r.MirrorURL != nil {
		t.Error("language, license, mirror_url should be null")
	}
	if r.OpenIssues != 3 || r.OpenIssuesCount != 3 {
		t.Errorf("open issues counters = %d / %d", r.OpenIssues, r.OpenIssuesCount)
	}
	if len(r.Topics) != 0 {
		t.Errorf("topics should be empty slice, got %v", r.Topics)
	}
	if r.Permissions == nil || !r.Permissions.Pull {
		t.Errorf("permissions = %+v", r.Permissions)
	}

	// A private repo with no permissions block flips visibility and omits perms.
	pr := sampleRepo()
	pr.Private = true
	priv := b.Repository(pr, nodeid.FormatNew, nil)
	if priv.Visibility != "private" || priv.Permissions != nil {
		t.Errorf("private render = visibility %q perms %+v", priv.Visibility, priv.Permissions)
	}
}

func TestContentFileRender(t *testing.T) {
	b := testBuilder(t)
	entry := git.PathEntry{Name: "README.md", Path: "README.md", Type: git.ObjectBlob, Mode: "100644", SHA: "abc123", Size: 8}
	c := b.ContentFile("octocat", "hello", "main", entry, []byte("# Hello\n"))

	if c.Type != "file" || c.Encoding != "base64" {
		t.Fatalf("type/encoding = %q/%q", c.Type, c.Encoding)
	}
	if c.Content != "IyBIZWxsbwo=\n" {
		t.Errorf("content = %q", c.Content)
	}
	if c.URL != "https://git.test.internal/api/v3/repos/octocat/hello/contents/README.md?ref=main" {
		t.Errorf("url = %q", c.URL)
	}
	if c.DownloadURL == nil || *c.DownloadURL != "https://git.test.internal/octocat/hello/raw/main/README.md" {
		t.Errorf("download_url = %v", c.DownloadURL)
	}
	if c.Links.Self != c.URL || c.Links.Git == nil || c.Links.HTML == nil {
		t.Errorf("_links = %+v", c.Links)
	}
}

func TestContentDirRender(t *testing.T) {
	b := testBuilder(t)
	entries := []git.PathEntry{
		{Name: "guide.md", Path: "docs/guide.md", Type: git.ObjectBlob, Mode: "100644", SHA: "f1", Size: 11},
		{Name: "sub", Path: "docs/sub", Type: git.ObjectTree, Mode: "040000", SHA: "t1"},
	}
	dir := b.ContentDir("octocat", "hello", "main", entries)
	if len(dir) != 2 {
		t.Fatalf("dir entries = %d", len(dir))
	}
	if dir[0].Type != "file" || dir[0].Encoding != "" || dir[0].Content != "" || dir[0].DownloadURL == nil {
		t.Errorf("file entry = %+v", dir[0])
	}
	if dir[1].Type != "dir" || dir[1].DownloadURL != nil {
		t.Errorf("dir entry download_url should be null: %+v", dir[1])
	}
}

func TestTreeRender(t *testing.T) {
	b := testBuilder(t)
	tr := b.Tree("octocat", "hello", git.Tree{
		SHA: "root",
		Entries: []git.TreeEntry{
			{Path: "README.md", Mode: "100644", Type: git.ObjectBlob, SHA: "b1", Size: 8},
			{Path: "docs", Mode: "040000", Type: git.ObjectTree, SHA: "t1"},
		},
	})
	if tr.URL != "https://git.test.internal/api/v3/repos/octocat/hello/git/trees/root" {
		t.Errorf("tree url = %q", tr.URL)
	}
	blob := tr.Tree[0]
	if blob.Type != "blob" || blob.Size == nil || *blob.Size != 8 || blob.URL == nil {
		t.Errorf("blob entry = %+v", blob)
	}
	sub := tr.Tree[1]
	if sub.Type != "tree" || sub.Size != nil || sub.URL == nil {
		t.Errorf("tree entry = %+v", sub)
	}
}

func TestGitCommitRender(t *testing.T) {
	b := testBuilder(t)
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	c := b.GitCommit("octocat", "hello", 50, git.Commit{
		SHA:       "deadbeef",
		Tree:      "tree1",
		Parents:   []string{"parent1"},
		Author:    git.Signature{Name: "Octo Cat", Email: "octo@example.com", When: when},
		Committer: git.Signature{Name: "Octo Cat", Email: "octo@example.com", When: when},
		Message:   "initial commit",
	})
	if c.NodeID[:2] != "C_" {
		t.Errorf("commit node_id prefix = %q", c.NodeID[:2])
	}
	if c.Verification.Verified || c.Verification.Reason != "unsigned" {
		t.Errorf("verification = %+v", c.Verification)
	}
	if len(c.Parents) != 1 || c.Parents[0].URL != "https://git.test.internal/api/v3/repos/octocat/hello/git/commits/parent1" {
		t.Errorf("parents = %+v", c.Parents)
	}
}

package graphql_test

import (
	"encoding/json"
	"testing"
)

// repoFieldsQuery selects the Repository fields R02-12 and R02-13 add: the
// scalar/boolean metadata gh repo view reads, the connections (repositoryTopics,
// watchers, languages, milestones), the parent fork pointer, latestRelease, and
// the template lists, plus sshUrl now typed GitSSHRemote. The contract is that
// the whole document resolves with zero errors and the values match the seeded
// repository.
const repoFieldsQuery = `query RepoFields($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    databaseId
    visibility
    sshUrl
    viewerCanAdminister
    viewerDefaultMergeMethod
    hasWikiEnabled
    hasProjectsEnabled
    hasDiscussionsEnabled
    isTemplate
    isMirror
    mirrorUrl
    deleteBranchOnMerge
    parent { nameWithOwner }
    repositoryTopics(first: 10) {
      totalCount
      nodes { topic { name } url }
    }
    watchers(first: 5) { totalCount }
    languages(first: 5) { totalCount totalSize nodes { name } }
    milestones(first: 10) { totalCount nodes { title } }
    latestRelease { tagName }
    issueTemplates { name }
    pullRequestTemplates { filename }
  }
}`

// TestRepositoryFieldPack confirms the expanded Repository field set resolves
// without validation or runtime errors and reports the seeded repository's
// values: a public, non-fork repo with no topics, milestones, or releases.
func TestRepositoryFieldPack(t *testing.T) {
	srv, token := graphqlServer(t)
	got := post(t, srv, token, repoFieldsQuery, map[string]any{"owner": "octocat", "name": "hello"})

	var env struct {
		Data struct {
			Repository struct {
				DatabaseID               int64   `json:"databaseId"`
				Visibility               string  `json:"visibility"`
				SSHURL                   string  `json:"sshUrl"`
				ViewerCanAdminister      bool    `json:"viewerCanAdminister"`
				ViewerDefaultMergeMethod string  `json:"viewerDefaultMergeMethod"`
				HasWikiEnabled           bool    `json:"hasWikiEnabled"`
				IsTemplate               bool    `json:"isTemplate"`
				IsMirror                 bool    `json:"isMirror"`
				MirrorURL                *string `json:"mirrorUrl"`
				Parent                   *struct {
					NameWithOwner string `json:"nameWithOwner"`
				} `json:"parent"`
				RepositoryTopics struct {
					TotalCount int `json:"totalCount"`
				} `json:"repositoryTopics"`
				Watchers struct {
					TotalCount int `json:"totalCount"`
				} `json:"watchers"`
				Languages struct {
					TotalCount int `json:"totalCount"`
					TotalSize  int `json:"totalSize"`
				} `json:"languages"`
				Milestones struct {
					TotalCount int `json:"totalCount"`
				} `json:"milestones"`
				LatestRelease *struct {
					TagName string `json:"tagName"`
				} `json:"latestRelease"`
				IssueTemplates       []any `json:"issueTemplates"`
				PullRequestTemplates []any `json:"pullRequestTemplates"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal: %v, body %s", err, got)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("query returned errors, want none: %v\nbody %s", env.Errors, got)
	}

	repo := env.Data.Repository
	if repo.DatabaseID == 0 {
		t.Errorf("databaseId = 0, want the repo's integer id")
	}
	if repo.Visibility != "PUBLIC" {
		t.Errorf("visibility = %q, want PUBLIC", repo.Visibility)
	}
	if repo.SSHURL == "" {
		t.Errorf("sshUrl is empty")
	}
	if !repo.ViewerCanAdminister {
		t.Errorf("viewerCanAdminister = false, want true for the owner")
	}
	if repo.ViewerDefaultMergeMethod != "MERGE" {
		t.Errorf("viewerDefaultMergeMethod = %q, want MERGE", repo.ViewerDefaultMergeMethod)
	}
	if repo.IsMirror {
		t.Errorf("isMirror = true, want false")
	}
	if repo.MirrorURL != nil {
		t.Errorf("mirrorUrl = %v, want null", *repo.MirrorURL)
	}
	if repo.Parent != nil {
		t.Errorf("parent = %+v, want null for a non-fork", repo.Parent)
	}
	if repo.RepositoryTopics.TotalCount != 0 {
		t.Errorf("repositoryTopics.totalCount = %d, want 0", repo.RepositoryTopics.TotalCount)
	}
	if repo.Watchers.TotalCount != 0 {
		t.Errorf("watchers.totalCount = %d, want 0", repo.Watchers.TotalCount)
	}
	if repo.Languages.TotalCount != 0 || repo.Languages.TotalSize != 0 {
		t.Errorf("languages = %+v, want empty", repo.Languages)
	}
	if repo.Milestones.TotalCount != 0 {
		t.Errorf("milestones.totalCount = %d, want 0", repo.Milestones.TotalCount)
	}
	if repo.LatestRelease != nil {
		t.Errorf("latestRelease = %+v, want null with no releases", repo.LatestRelease)
	}
	if repo.IssueTemplates != nil {
		t.Errorf("issueTemplates = %v, want null", repo.IssueTemplates)
	}
	if repo.PullRequestTemplates != nil {
		t.Errorf("pullRequestTemplates = %v, want null", repo.PullRequestTemplates)
	}
}

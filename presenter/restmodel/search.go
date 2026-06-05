package restmodel

// The search result envelopes. Every search endpoint returns the same shape:
// the total match count, whether the search finished, and the page of items.
// An item embeds the matched resource and adds the relevance score GitHub
// attaches to search hits.

// SearchIssues is the GET /search/issues body.
type SearchIssues struct {
	TotalCount        int               `json:"total_count"`
	IncompleteResults bool              `json:"incomplete_results"`
	Items             []IssueSearchItem `json:"items"`
}

// IssueSearchItem is one issue or pull request hit: the full issue object plus
// its score.
type IssueSearchItem struct {
	Issue
	Score float64 `json:"score"`
}

// SearchRepositories is the GET /search/repositories body.
type SearchRepositories struct {
	TotalCount        int              `json:"total_count"`
	IncompleteResults bool             `json:"incomplete_results"`
	Items             []RepoSearchItem `json:"items"`
}

// RepoSearchItem is one repository hit: the full repository object plus its
// score.
type RepoSearchItem struct {
	Repository
	Score float64 `json:"score"`
}

// SearchCode is the GET /search/code body.
type SearchCode struct {
	TotalCount        int              `json:"total_count"`
	IncompleteResults bool             `json:"incomplete_results"`
	Items             []CodeSearchItem `json:"items"`
}

// CodeSearchItem is one matching file: its name and path within the head tree,
// the blob object id, the API and HTML URLs that address it, the repository it
// lives in, and the score.
type CodeSearchItem struct {
	Name       string     `json:"name"`
	Path       string     `json:"path"`
	SHA        string     `json:"sha"`
	URL        string     `json:"url"`
	GitURL     string     `json:"git_url"`
	HTMLURL    string     `json:"html_url"`
	Repository Repository `json:"repository"`
	Score      float64    `json:"score"`
}

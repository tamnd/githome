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
	Score       float64     `json:"score"`
	TextMatches []TextMatch `json:"text_matches,omitempty"`
}

// TextMatch is one text-match metadata entry GitHub attaches to a search hit
// when the request asks for application/vnd.github.text-match+json. It names
// the matched property, the fragment of it that contained the match, and the
// matched substrings with their rune offsets into the fragment.
type TextMatch struct {
	ObjectURL  string             `json:"object_url"`
	ObjectType *string            `json:"object_type"`
	Property   string             `json:"property"`
	Fragment   string             `json:"fragment"`
	Matches    []TextMatchElement `json:"matches"`
}

// TextMatchElement is one matched substring inside a fragment: the matched text
// and its [start, end) rune offsets.
type TextMatchElement struct {
	Text    string `json:"text"`
	Indices []int  `json:"indices"`
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
	Score       float64     `json:"score"`
	TextMatches []TextMatch `json:"text_matches,omitempty"`
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
	Name        string      `json:"name"`
	Path        string      `json:"path"`
	SHA         string      `json:"sha"`
	URL         string      `json:"url"`
	GitURL      string      `json:"git_url"`
	HTMLURL     string      `json:"html_url"`
	Repository  Repository  `json:"repository"`
	Score       float64     `json:"score"`
	TextMatches []TextMatch `json:"text_matches,omitempty"`
}

// SearchUsers is the GET /search/users body.
type SearchUsers struct {
	TotalCount        int              `json:"total_count"`
	IncompleteResults bool             `json:"incomplete_results"`
	Items             []UserSearchItem `json:"items"`
}

// UserSearchItem is one account hit: the SimpleUser object plus its score.
type UserSearchItem struct {
	SimpleUser
	Score float64 `json:"score"`
}

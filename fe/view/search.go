package view

// search.go holds the search surface view models: the page shell, the result-type
// rail, the sort menu, and the four result-row shapes the domain search layer
// backs (code, repositories, issues, pull requests). It is pure data with every
// URL precomputed in the handler through fe/route; the template prints fields and
// switches on the active type. The result types Githome's domain does not yet
// serve (users, commits) are simply absent from the rail rather than shown
// disabled, so the UI never advertises a capability that is not there. See
// implementation/12 section 2.

// The search type keys. They are the ?type= values and the keys the rail and the
// builder dispatch on. They match the GitHub vocabulary so a bookmarked
// ?type=code keeps working.
const (
	SearchCode   = "code"
	SearchRepos  = "repositories"
	SearchIssues = "issues"
	SearchPulls  = "pullrequests"
)

// SearchScope names whether a search spans the whole host or one repository. The
// scope drives the default type (repositories globally, code in a repo), the rail
// membership (no cross-repo types in a repo), and the implicit repo: qualifier
// the in-repo box injects.
const (
	ScopeGlobal = "global"
	ScopeRepo   = "repo"
)

// SearchTab is one result-type tab in the rail: the type key it selects, its
// label and octicon, the faceted URL that switches to it (q and the sort kept
// intact), whether it is the active type, and an optional match count. Code
// carries no count off the active tab because counting code means walking a tree,
// so HasCount is false there rather than showing a misleading zero.
type SearchTab struct {
	Key      string
	Label    string
	Icon     string
	URL      string
	IsActive bool
	Count    int
	HasCount bool
}

// SearchSortOption is one entry in the sort menu: its label, the faceted URL that
// applies it, and whether it is the active sort. Code search has no sort, so its
// menu is empty and the template hides the control.
type SearchSortOption struct {
	Label    string
	URL      string
	IsActive bool
}

// RepoResultVM is one repository in the repositories results: its full name and
// URL, the owner link, the one-line description, the state badges, and the last
// push time. It carries no star button because the web front's domain repo value
// does not surface a star count yet; the row links to the repo where stars live.
type RepoResultVM struct {
	FullName    string
	URL         string
	OwnerURL    string
	OwnerLogin  string
	Description string
	Private     bool
	Fork        bool
	Archived    bool
	UpdatedAt   string
	UpdatedISO  string
}

// IssueResultVM is one issue or pull request in the cross-repository results: the
// issue-row fields plus the repository-context line a cross-repo result needs,
// since the request path does not imply the repo. The same shape backs both the
// issues and the pull-requests rails; the URL already points at /issues/{n} or
// /pull/{n}, so the template does not branch.
type IssueResultVM struct {
	Number       int64
	Title        string
	URL          string
	State        IssueStateVM
	Author       UserChipVM
	OpenedAt     string
	OpenedISO    string
	CommentCount int
	RepoFullName string
	RepoURL      string
}

// CodeResultVM is one matching file in the code results: its base name, its path
// within the repository, the blob URL that opens it, and the repository-context
// line. The domain code search returns a path and an object id, not a line
// snippet, so the row links to the file rather than showing a fabricated excerpt.
type CodeResultVM struct {
	Name         string
	Path         string
	BlobURL      string
	RepoFullName string
	RepoURL      string
}

// SearchPageVM is the search results page: the query box value, the scope, the
// optional repo header (in-repo search), the type rail, the sort menu, the active
// type's rows, any notes (an incomplete code walk, a missing scope), and the
// pager. Only the slice matching ActiveType is populated; the template switches on
// ActiveType so an unused slice is nil.
type SearchPageVM struct {
	Chrome Chrome
	Scope  string

	// Header and Nav render the repo context bar on an in-repo search; both are
	// zero on a global search and the template omits the bar.
	Header RepoHeaderVM
	Nav    TreeNav
	Repo   RepoRef

	QueryValue string // the raw q the input shows
	Action     string // the search form's GET target (/search or the repo search)
	ActiveType string

	Types []SearchTab
	Sorts []SearchSortOption

	Total  int
	Notes  []string
	Repos  []RepoResultVM
	Issues []IssueResultVM
	Code   []CodeResultVM
	Pager  Pager

	Landing     bool   // true when q is empty: the search landing, no rows, no count
	Empty       bool   // true when a non-empty query matched nothing
	EmptyReason string // the blankslate line for an empty result
}

// SearchTypeOr validates a requested ?type= against the allowed set for the page
// and falls back to def when it is empty or unknown. A bad type never errors, it
// degrades to the default, matching the parser's tolerance for a human's URL.
func SearchTypeOr(raw, def string, allowed []string) string {
	for _, t := range allowed {
		if raw == t {
			return raw
		}
	}
	return def
}

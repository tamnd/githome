package view

// compare.go holds the view models the compare handlers render: the branch
// picker and the range comparison with optional PR creation form. They are flat
// presentation structs with no domain import; the handler package maps domain
// data into them and precomputes every URL through fe/route. See
// implementation/09 section 8.

// CompareBranchVM is the minimal branch identity on a comparison header: the
// name for display and the short SHA for the "View" link anchor.
type CompareBranchVM struct {
	Name     string
	SHA      string
	ShortSHA string
	URL      string // tree at the branch tip
}

// ComparePickerVM is the view model for GET /compare. It shows a two-branch
// selector (base and head) so the viewer can start a comparison or a PR.
type ComparePickerVM struct {
	Chrome   Chrome
	Header   RepoHeaderVM
	Nav      TreeNav
	Branches []string
	Base     string
	Head     string
	Action   string // URL the form GETs (compares {base}...{head})
}

// CompareCommitVM is one commit row on the comparison page.
type CompareCommitVM struct {
	ShortSHA   string
	Title      string
	AuthorName string
	When       string
	WhenISO    string
	URL        string
}

// CompareRangeVM is the view model for GET /compare/{basehead...}. It shows
// the diff between base and head and, when Expanded is true, the PR creation
// form below the diff.
type CompareRangeVM struct {
	Chrome       Chrome
	Header       RepoHeaderVM
	Nav          TreeNav
	Base         CompareBranchVM
	Head         CompareBranchVM
	MergeBase    string // short SHA of the merge base commit
	Commits      []CompareCommitVM
	TotalCommits int // the real range size; > len(Commits) when the list is capped
	Files        []DiffFileVM
	Additions    int
	Deletions    int
	ChangedFiles int
	HasDiff      bool // false when base == head or no changed files
	Expanded     bool // true when ?expand=1, shows PR creation form
	CreateURL    string
	CSRFToken    string
	ExpandURL    string // the same range URL with ?expand=1
}

package view

// DashboardVM is the global, viewer-scoped issues or pull-requests page at the
// reserved /issues and /pulls names: a tab row scoping the list to what the
// viewer created or is assigned, a cross-repo filter box, and the same result
// rows the search page renders (each row carries its repository line, since
// the path implies none). The handler precomputes every URL; the template only
// ranges. See spec doc 09 section 1.6.
type DashboardVM struct {
	Chrome     Chrome
	Heading    string // "Issues" | "Pull requests"
	Icon       string // the heading octicon
	QueryValue string // the literal extra q the filter box shows
	Action     string // the GET form target, /issues or /pulls
	Tab        string // the active tab key, carried as the form's hidden field
	Tabs       []FilterTab
	Rows       []IssueResultVM
	Pager      Pager

	Empty       bool   // true when the scoped list is empty
	EmptyReason string // the blankslate line
}

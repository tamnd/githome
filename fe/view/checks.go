package view

// The checks page view models. Githome backs the commit-level signals Spec 2003
// pins (check runs grouped into suites and the older flat commit statuses) and
// their combined rollup, not the full Actions run engine Spec 2005 doc 11 sketches
// (workflow runs, the needs job graph, live log streaming, artifacts, caches,
// deployments). The page renders exactly the backed surface for one ref: the
// rollup verdict pill, the check-run rows, and the commit-status rows, each with
// the shared StatusToken so the icon and color match every other check surface.
// The domain to VM mapping lives in the fe/web/checks handler, keeping this file
// domain-free like the rest of fe/view.

// ChecksPageVM is the checks page for a ref, /{owner}/{repo}/checks/{ref}. It
// carries the repo context bar so the page sits inside the repository like every
// other repo sub-page, the resolved sha the checks anchor to, the rollup verdict,
// and the two row sets. Empty is true when the ref resolved but nothing has
// reported against it, which renders the blankslate rather than a bare page.
type ChecksPageVM struct {
	Chrome Chrome
	Header RepoHeaderVM
	Nav    TreeNav
	Repo   RepoRef

	Ref      string // the ref as navigated, shown in the heading
	SHA      string
	ShortSHA string

	Rollup      StatusToken
	RollupTitle string
	Total       int

	Runs     []CheckRunRowVM
	Statuses []CommitStatusRowVM

	Empty bool
}

// CheckRunRowVM is one check run in the list: its name, the shared status token,
// the optional one-line output summary, a details link out to the reporting app
// (sanitized by the handler, omitted when absent), and the precomputed time line.
// WhenVerb is "Finished", "Started", or "Queued" depending on how far the run got;
// WhenISO and WhenHuman feed the <relative-time> element the same way the rest of
// the front renders timestamps.
type CheckRunRowVM struct {
	Name       string
	Token      StatusToken
	Summary    string
	DetailsURL string
	HasDetails bool

	WhenVerb  string
	WhenISO   string
	WhenHuman string
}

// CommitStatusRowVM is one external commit status: the context it reported under,
// the shared status token, the optional description, and the optional target URL
// (the external build's page, sanitized by the handler). It is the older, flat
// signal a CI system posts with no check-run output.
type CommitStatusRowVM struct {
	Context     string
	Token       StatusToken
	Description string
	TargetURL   string
	HasTarget   bool
}

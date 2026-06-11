package view

import "html/template"

// pr_checks.go holds the PR Checks tab view models: the tab at
// /{owner}/{repo}/pull/{number}/checks that lists the check runs reported
// against the pull request's head sha, grouped by their check suite, beside the
// older flat commit statuses. The rows carry the same shared StatusToken the
// standalone checks page and the merge box use, so every check surface agrees
// about what a status looks like. Like the rest of fe/view this is pure data:
// the domain to VM mapping lives in the fe/web/pulls handler and every URL is
// precomputed there. See implementation/09 and Spec 2005 doc 11 section 8.

// PRChecksVM is the Checks tab: the shell every PR tab renders inside, the head
// sha the checks anchor to, the rollup verdict pill, the suite groups, and the
// commit-status rows. Empty is true when nothing has reported against the head,
// which renders the blankslate rather than a bare page.
type PRChecksVM struct {
	Chrome Chrome
	Shell  PRShellVM

	SHA      string
	ShortSHA string

	Rollup      StatusToken
	RollupTitle string
	Total       int

	Suites   []PRCheckSuiteVM
	Statuses []CommitStatusRowVM

	// Detail is the selected run's pane, present when ?check_run_id= names a
	// run reported against this head. An id that matches nothing renders the
	// plain list, never an error.
	Detail *PRCheckDetailVM

	Empty bool
}

// PRCheckSuiteVM is one check suite's group in the list: the reporting app's
// slug as the group heading and the suite's check runs.
type PRCheckSuiteVM struct {
	App  string
	Runs []PRCheckRunRowVM
}

// PRCheckRunRowVM is one check run row on the Checks tab: the shared status
// token, the one-line output summary, the run's duration once it has both
// timestamps, the sanitized external details link, and the precomputed time
// line the standalone checks page also shows.
type PRCheckRunRowVM struct {
	ID      int64
	Name    string
	Token   StatusToken
	Summary string

	Duration string // "1m 32s" once started and completed, else empty

	DetailsURL string
	HasDetails bool

	WhenVerb  string
	WhenISO   string
	WhenHuman string

	// SelectURL is the tab URL with ?check_run_id= naming this run, the no-JS
	// link that opens the detail pane; Selected marks the open run's row.
	SelectURL string
	Selected  bool
}

// PRCheckDetailVM is the selected run's detail pane under the list: the run's
// identity and status, its timing, and the reported output. The summary and the
// text render through the shared markup pipeline (a check run's output is
// markdown, the same dialect a comment body speaks); the raw strings ride along
// so an unconfigured markup falls back to escaped text instead of nothing.
type PRCheckDetailVM struct {
	ID    int64
	Name  string
	App   string
	Token StatusToken

	Duration  string
	WhenVerb  string
	WhenISO   string
	WhenHuman string

	DetailsURL string
	HasDetails bool

	Title       string
	Summary     string
	SummaryHTML template.HTML
	Text        string
	TextHTML    template.HTML
	HasOutput   bool
}

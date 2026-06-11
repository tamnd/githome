package view

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
}

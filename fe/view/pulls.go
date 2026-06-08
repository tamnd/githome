package view

// This file holds the pull-request view models the fe/web/pulls handlers render:
// the PR index, the shell the four tabs hang off, the Conversation timeline, the
// Commits tab, the Files-changed diff, and the merge box. Like the issue models
// they are flat presentation structs with no behavior and no domain import: the
// handler package maps domain data into them and precomputes every URL through
// fe/route. The PR surface reuses the issue building blocks where it can: a PR is
// an issue row plus a pull_requests side row, so its Conversation timeline is the
// same CommentVM stream the issue detail renders, and its index filter bar is the
// same FilterTab pair. See implementation/09 sections 2 to 5.
//
// As-built scope. The four-state pill and the merge-box state are derived here,
// once, from the cached domain columns, so the list mini-state and the detail
// header can never disagree (implementation/09 sections 2.4 and 5.1). The live
// mergeability worker fills only the unknown, draft, dirty, behind, and clean
// states; the blocked, unstable, has_hooks, and queue states wait on the review
// and check milestones (F5 and F9) that produce them, so DeriveMergeBoxState names
// them in the enum but never returns them yet. The checks rollup and the review
// rollup are likewise empty until those milestones land; the merge box renders its
// control without them. The deferral is recorded in the spec.

// PRState is the four-way display state of a pull request. REST carries only
// open/closed (merged is closed plus merged:true); the UI splits it four ways for
// the header pill and the list mini-icon, derived once by DerivePRState so the two
// never drift.
type PRState int

const (
	PRStateOpen   PRState = iota // green,  git-pull-request
	PRStateDraft                 // gray,   git-pull-request-draft
	PRStateMerged                // purple, git-merge
	PRStateClosed                // red,    git-pull-request-closed
)

// DerivePRState maps the domain columns onto the four-way state. It takes the raw
// open/closed state string and the merged and draft flags rather than a domain
// value so fe/view stays free of a domain import. Merged wins over closed because
// a merged PR is stored closed; draft only applies to an otherwise-open PR.
func DerivePRState(state string, merged, draft bool) PRState {
	switch {
	case merged:
		return PRStateMerged
	case state == "closed":
		return PRStateClosed
	case draft:
		return PRStateDraft
	default:
		return PRStateOpen
	}
}

// Label is the human word for the state, shown in the pill.
func (s PRState) Label() string {
	switch s {
	case PRStateDraft:
		return "Draft"
	case PRStateMerged:
		return "Merged"
	case PRStateClosed:
		return "Closed"
	default:
		return "Open"
	}
}

// Icon is the octicon name for the state. Every name is registered in the icon
// set (the coverage test guarantees it).
func (s PRState) Icon() string {
	switch s {
	case PRStateDraft:
		return "git-pull-request-draft"
	case PRStateMerged:
		return "git-merge"
	case PRStateClosed:
		return "git-pull-request-closed"
	default:
		return "git-pull-request"
	}
}

// Modifier is the CSS modifier the stylesheet colors the pill and the mini-icon
// by: open green, draft gray, merged purple, closed red.
func (s PRState) Modifier() string {
	switch s {
	case PRStateDraft:
		return "draft"
	case PRStateMerged:
		return "merged"
	case PRStateClosed:
		return "closed"
	default:
		return "open"
	}
}

// PRStateVM is the derived pill the index rows and the shell header both render,
// flattened so a template prints fields instead of calling methods.
type PRStateVM struct {
	Label    string
	Icon     string
	Modifier string
}

// StateVM flattens the derived state into the printable pill model.
func (s PRState) StateVM() PRStateVM {
	return PRStateVM{Label: s.Label(), Icon: s.Icon(), Modifier: s.Modifier()}
}

// PRRow is one row in the pull-request index: the derived state mini-icon, the
// number and title link, the meta line, the labels, and the comment count. It
// mirrors IssueRow so the two lists read alike, with the four-state pill standing
// in for the issue's open/closed badge.
type PRRow struct {
	Number       int64
	Title        string
	URL          string
	State        PRStateVM
	Author       UserChipVM
	OpenedAt     string
	OpenedISO    string
	Labels       []LabelVM
	Assignees    []UserChipVM
	Milestone    *MilestoneVM
	CommentCount int
}

// PRIndexVM is the pull-request index: the header, the search-and-filter bar, the
// list, and the pagination. It is the issue index frame pre-filtered to PRs, so
// the open and closed tabs and the query input behave the same; only the rows and
// the empty line speak pull requests.
type PRIndexVM struct {
	Chrome      Chrome
	Header      RepoHeaderVM
	Nav         TreeNav
	Repo        RepoRef
	QueryValue  string
	OpenTab     FilterTab
	ClosedTab   FilterTab
	ActiveChips []LabelVM
	Rows        []PRRow
	Pager       Pager
	Empty       bool
	EmptyReason string
}

// PRShellVM is the context every /pull/{n} tab renders inside: the four-state
// pill, the "wants to merge N commits into BASE from HEAD" byline, the tab bar
// with its counts, and the active-tab marker. All four tab handlers build the same
// shell so the header and tabs are byte-identical across tabs, only the slotted
// content differs.
type PRShellVM struct {
	Chrome    Chrome
	Header    RepoHeaderVM
	Nav       TreeNav
	Repo      RepoRef
	Number    int64
	Title     string
	State     PRStateVM
	Author    UserChipVM
	OpenedAt  string
	OpenedISO string

	BaseRef     string
	HeadRef     string
	HeadLabel   string // owner:branch for a cross-repo head, the bare ref otherwise
	IsCrossRepo bool

	CommitCount  int
	ChangedFiles int
	CommentCount int
	Additions    int
	Deletions    int

	IsMerged bool
	IsClosed bool
	MergedAt string

	ActiveTab string // conversation | commits | files

	CanEdit bool
	EditURL string

	// The tab URLs, precomputed so the shell partial never builds a link.
	ConversationURL string
	CommitsURL      string
	FilesURL        string
}

// PRConversationVM is the Conversation tab: the shell plus the comment timeline,
// the new-comment composer, the opening-body reaction rollup, and the merge box.
// The timeline is the same CommentVM stream the issue detail renders, because a PR
// shares its number and its comments with an issue.
type PRConversationVM struct {
	Chrome    Chrome
	Shell     PRShellVM
	Timeline  []PRTimelineItem
	Composer  ComposerVM
	Reactions ReactionsVM
	MergeBox  MergeBoxVM
	FormError string
}

// PRCommitsVM is the Commits tab: the shell plus the PR's commits grouped by
// calendar date, each row a short sha, a title, and an author.
type PRCommitsVM struct {
	Chrome Chrome
	Shell  PRShellVM
	Groups []PRCommitDateGroup
}

// PRCommitDateGroup is one day's heading and the commits authored that day.
type PRCommitDateGroup struct {
	Date    string
	Commits []PRCommitRow
}

// PRCommitRow is one commit in the PR: the short and full sha, the title, the
// author, and the formatted authored-at time.
type PRCommitRow struct {
	SHA        string
	ShortSHA   string
	Title      string
	AuthorName string
	When       string
	WhenISO    string
}

// PRFilesVM is the Files-changed tab: the shell plus the diff. It carries the
// per-file diff stats summary line and the list of file diffs the shared diff
// component built. The Files tab is read-only in F4; the inline review threads and
// the review state machine arrive in F5 over this same model.
type PRFilesVM struct {
	Chrome       Chrome
	Shell        PRShellVM
	ChangedFiles int
	Additions    int
	Deletions    int
	Files        []DiffFileVM
	Truncated    bool            // true when the file list was capped, logged by the handler
	Review       ReviewSurfaceVM // the inline-comment and review-verdict context (F5)
}

// MergeBoxState is the visual state of the merge box, derived once from the cached
// mergeability columns. The template is a switch on this; each state renders its
// own control cluster.
type MergeBoxState int

const (
	MergeComputing MergeBoxState = iota // mergeable_state unknown: spinner, poll
	MergeDraft                          // a draft PR: Ready-for-review control
	MergeClean                          // mergeable: the green Merge control
	MergeBehind                         // clean but behind base: Update-branch note
	MergeDirty                          // conflicts: Resolve-conflicts entry, no merge
	MergeBlocked                        // F5: required reviews/checks not satisfied
	MergeUnstable                       // F9: a non-required check failed
	MergeHasHooks                       // pre-receive hooks present
	MergeQueue                          // merge queue enabled
	MergeMerged                         // post-merge panel
	MergeClosed                         // closed unmerged
)

// DeriveMergeBoxState maps the domain mergeability and lifecycle columns onto the
// visual state. It does no git work: it reads the columns the mergeability worker
// filled, so the box and a concurrent API read agree. It takes primitives, not a
// domain value, to keep fe/view domain-free. The blocked, unstable, has_hooks, and
// queue states are not produced by the live worker yet (F5 and F9); they are in
// the enum so the template and the next milestone do not need a reshape.
func DeriveMergeBoxState(merged bool, state, mergeableState string) MergeBoxState {
	switch {
	case merged:
		return MergeMerged
	case state == "closed":
		return MergeClosed
	}
	switch mergeableState {
	case "draft":
		return MergeDraft
	case "dirty":
		return MergeDirty
	case "behind":
		return MergeBehind
	case "blocked":
		return MergeBlocked
	case "unstable":
		return MergeUnstable
	case "has_hooks":
		return MergeHasHooks
	case "clean":
		return MergeClean
	default:
		// "unknown" and any value the worker has not filled yet read as computing,
		// which is the state that polls until the worker resolves it.
		return MergeComputing
	}
}

// MergeMethodVM is one selectable merge strategy in the merge control: the method
// key the form submits, its human label, and whether it is the default. F4 offers
// all three git strategies; the per-repo allow-list that hides some arrives with
// the repository settings milestone (F8).
type MergeMethodVM struct {
	Method    string // merge | squash | rebase
	Label     string
	IsDefault bool
}

// ChecksRollupVM is the status-checks summary the merge box and the list mini-icon
// share. It is empty until the checks milestone (F9) fills it; the box renders its
// merge control without it in F4.
type ChecksRollupVM struct {
	Headline string
	Present  bool
}

// ReviewRollupVM is the review-decision summary. It is empty until the code-review
// milestone (F5) fills it.
type ReviewRollupVM struct {
	Decision string // Approved | Changes requested | Review required
	Present  bool
}

// MergeBoxVM is the merge box on the Conversation tab: the derived state, the two
// rollups, the merge control, and the lifecycle affordances. HeadSHA rides along
// as the optimistic-concurrency token every merge form submits, so a merge of a
// head that moved out from under the viewer is rejected rather than silently
// merging the wrong tip. The box re-fetches itself while Computing.
type MergeBoxVM struct {
	State   MergeBoxState
	Checks  ChecksRollupVM
	Reviews ReviewRollupVM

	HeadSHA string

	Methods       []MergeMethodVM
	PrimaryMethod string

	BlockReasons []string

	ViewerCanMerge bool
	HeadRefExists  bool
	CanReopen      bool

	MergeURL           string // POST target for the merge
	PollURL            string // GET target the Computing state re-fetches
	DefaultCommitTitle string

	// CSRFToken is read from Chrome on the full page; the standalone fragment
	// carries it directly so the merge form posts with a token either way.
	CSRFToken string
}

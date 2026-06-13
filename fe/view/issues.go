package view

import "html/template"

// This file holds the issue-area view models the fe/web/issues handlers render:
// the index with its filter bar, the detail page with its comment timeline and
// sidebar, the composer, and the new-issue form. Like the repo models they are
// flat presentation structs with no behavior and no domain import: the handler
// package maps domain data into them and precomputes every URL through fe/route,
// so a template never reaches past its view model. Each full-page model embeds a
// Chrome and carries the shared RepoHeaderVM/TreeNav so the issues surface wears
// the same repo header as the code views. See implementation/08 sections 3 to 8.
//
// As-built scope. The timeline the binary can render today is the comment stream
// plus the open/close state derived from the issue itself. The richer event
// union the spec sketches (labeled, assigned, milestoned, referenced, renamed)
// needs a domain read path the IssueService does not expose yet, so the timeline
// is comments only and the deferral is recorded in the spec. Sub-issues, pin,
// lock, transfer, and minimize are likewise out of scope until the domain grows
// the methods, and the sidebar pickers edit labels, assignees, and milestone
// through the EditIssue patch the service does support.

// IssueStateVM is the open/closed badge shared by the index rows and the detail
// header: a label, an octicon, and a CSS modifier the stylesheet colors (green
// open, purple completed, gray not-planned).
type IssueStateVM struct {
	State    string // "open" | "closed"
	Reason   string // "completed" | "not_planned" | "reopened" | ""
	Label    string // "Open" | "Closed"
	Icon     string // the octicon name
	Modifier string // "open" | "closed" | "not-planned"
}

// LabelVM is one issue label as a chip: the name, the label color split into
// its RGB channels (the template emits them as --label-r/g/b custom properties
// and the theme recipe mixes the final colors, so the chip re-themes in dark
// modes), and the index URL that filters to it.
type LabelVM struct {
	Name        string
	R, G, B     int // the label color channels, 0-255
	Description string
	URL         string
}

// MilestoneVM is the milestone chip shown on a row and in the sidebar: the title
// and the index URL that filters to it.
type MilestoneVM struct {
	Title string
	URL   string
}

// UserChipVM is a small user reference: the login, the avatar URL, and the
// profile URL. Used for authors, assignees, and the closed-by line.
type UserChipVM struct {
	Login     string
	AvatarURL string
	URL       string
}

// ReactionVM is one reaction summary on the rollup bar: the content key, the
// emoji glyph, the count, whether the viewer reacted, and the toggle form URL.
type ReactionVM struct {
	Content string
	Emoji   string
	Count   int
	Reacted bool
	URL     string // the POST target that toggles this reaction
}

// ReactionsVM is the reaction rollup under a comment or issue body: the visible
// reactions (count > 0) and the picker of all eight contents.
type ReactionsVM struct {
	Subject  string       // "issue" | "comment", for the form's subject field
	Items    []ReactionVM // the eight contents, in canonical order
	Total    int
	CanReact bool
}

// IssueRow is one row in the index list: the state badge, the number and title
// link, the meta line (opened by, when), the labels, the assignee avatars, and
// the comment count.
type IssueRow struct {
	Number       int64
	Title        string
	URL          string
	State        IssueStateVM
	Author       UserChipVM
	OpenedAt     string // already formatted, relativeTime handles the page
	OpenedISO    string // the machine-readable timestamp for <time datetime>
	Labels       []LabelVM
	Assignees    []UserChipVM
	Milestone    *MilestoneVM
	CommentCount int
}

// FilterTab is one state tab in the index bar: the label, the count, the filter
// href, and whether it is the active state.
type FilterTab struct {
	Label    string
	Count    int
	URL      string
	IsActive bool
}

// IssueIndexVM is the issues index: the header, the search-and-filter bar, the
// list, and the pagination. QueryValue is the literal q string the search input
// shows; the tabs and chips carry composed hrefs built by the Query composer.
type IssueIndexVM struct {
	Chrome      Chrome
	Header      RepoHeaderVM
	Nav         TreeNav
	Repo        RepoRef
	QueryValue  string
	OpenTab     FilterTab
	ClosedTab   FilterTab
	ActiveChips []LabelVM // the active label filters, each with a remove URL
	Rows        []IssueRow
	NewIssueURL string
	Pager       Pager
	Empty       bool   // true when the filtered list is empty
	EmptyReason string // a human line for the blankslate
}

// Pager is the prev/next pagination for a list: the page number and the URLs,
// empty when there is no previous or next page.
type Pager struct {
	Page    int
	PrevURL string
	NextURL string
}

// CommentVM is one comment in the issue timeline: the author, the rendered body,
// the timestamps, the reaction rollup, the permalink, and the edit affordance
// when the viewer may edit it.
type CommentVM struct {
	ID         int64
	Author     UserChipVM
	Body       template.HTML // the GFM-rendered, sanitized comment HTML
	BodySource string        // the raw markdown, for the edit textarea
	CreatedAt  string
	CreatedISO string
	Edited     bool
	IsAuthor   bool   // whether the body is the issue's opening body (the first item)
	Anchor     string // "issuecomment-{id}" for the permalink fragment
	URL        string
	Reactions  ReactionsVM
	CanEdit    bool
	EditURL    string
	DeleteURL  string
}

// SidebarVM is the issue detail sidebar: the assignees, labels, and milestone as
// the viewer sees them, plus whether the viewer can edit them (which reveals the
// pickers).
type SidebarVM struct {
	Assignees []UserChipVM
	Labels    []LabelVM
	Milestone *MilestoneVM
	CanEdit   bool
	EditURL   string // the EditIssue form target for label/assignee/milestone edits
}

// ComposerVM is the new-comment box at the foot of the timeline: the form target,
// the CSRF token (carried via Chrome), and the close/reopen button state.
type ComposerVM struct {
	Action      string // the POST target for a new comment
	CanComment  bool
	CanClose    bool
	IssueOpen   bool
	CloseLabel  string // "Close issue" | "Reopen issue"
	CloseAction string // the POST target that toggles state (with a comment if filled)
}

// IssueDetailVM is the issue detail page: the title and state header, the comment
// timeline, the sidebar, and the composer.
type IssueDetailVM struct {
	Chrome    Chrome
	Header    RepoHeaderVM
	Nav       TreeNav
	Repo      RepoRef
	Number    int64
	Title     string
	State     IssueStateVM
	Author    UserChipVM
	OpenedAt  string
	OpenedISO string
	Locked    bool
	EditURL   string // the edit-title form target, shown when the viewer can edit
	CanEdit   bool
	Timeline      []CommentVM
	TimelinePager Pager // prev/next over a long comment thread; zero value hides it
	Sidebar       SidebarVM
	Composer      ComposerVM
	Reactions     ReactionsVM // the rollup on the opening body
	FormError     string      // a validation message echoed back into the page
}

// NewIssueVM is the new-issue form: the title and body fields (seeded from the
// documented prefill query or echoed back on a validation miss), the metadata
// the prefill asked to apply on creation, the submit target, and any form
// error.
type NewIssueVM struct {
	Chrome    Chrome
	Header    RepoHeaderVM
	Nav       TreeNav
	Repo      RepoRef
	Action    string
	Title     string
	Body      string
	Labels    []string // label names applied on creation, from ?labels= or the template
	Assignees []string // assignee logins applied on creation, from ?assignees=
	Milestone string   // milestone number applied on creation, from ?milestone=
	CanSubmit bool
	FormError string
}

// LabelsVM is the repository label list page: every label with its color chip,
// description, and the issues-index URL that filters to it.
type LabelsVM struct {
	Chrome Chrome
	Header RepoHeaderVM
	Nav    TreeNav
	Repo   RepoRef
	Labels []LabelVM
	Count  int
}

// MilestoneRowVM is one milestone in the list and the header block of the
// milestone page: the title, the due and closed lines, the rendered progress,
// and the open/closed issue counts.
type MilestoneRowVM struct {
	Number       int64
	Title        string
	URL          string
	State        string // open | closed
	Description  string
	DueOn        string // formatted; empty when the milestone has no due date
	DueISO       string
	Overdue      bool
	ClosedAt     string // formatted; set when the milestone is closed
	OpenIssues   int
	ClosedIssues int
	Percent      int // completeness, 0-100
}

// MilestonesVM is the milestone list page with its open/closed state tabs.
type MilestonesVM struct {
	Chrome    Chrome
	Header    RepoHeaderVM
	Nav       TreeNav
	Repo      RepoRef
	OpenTab   FilterTab
	ClosedTab FilterTab
	Items     []MilestoneRowVM
	NewURL    string // left empty until a milestone create form exists
}

// MilestoneDetailVM is one milestone's page: the header block plus its issues,
// the same bounded rows the issues index renders, tabbed open/closed.
type MilestoneDetailVM struct {
	Chrome      Chrome
	Header      RepoHeaderVM
	Nav         TreeNav
	Repo        RepoRef
	Milestone   MilestoneRowVM
	OpenTab     FilterTab
	ClosedTab   FilterTab
	Rows        []IssueRow
	Pager       Pager
	Empty       bool
	EmptyReason string
}

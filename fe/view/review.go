package view

import "html/template"

// review.go holds the code-review view models the pull-request Files tab and the
// Conversation timeline render: an inline thread anchored to a diff row, the
// comments inside it, the per-row composer that opens a new thread, the
// "Review changes" overlay that submits a review verdict, and the submitted-review
// summary the timeline interleaves with comments. Like the rest of fe/view these
// are flat presentation structs with no behavior beyond a few print helpers and no
// domain import: the fe/web/pulls handlers map the domain review objects into them
// and precompute every URL and token. See implementation/09 sections 4 and 10.
//
// As built (F5). The review surface lives entirely in fe/web/pulls rather than the
// spec's separate fe/web/review package: a pull request's whole comment-and-review
// machinery hangs off the same shell, the same diff, and the same loadPR, so one
// package owns it without exporting builder helpers across a package line. The
// thread anchor is the persisted (path, line, side), never a diff position; a
// thread whose anchor no longer maps onto the current diff is outdated and renders
// in a per-file outdated group rather than against a row. The inline composer and
// the overlay only ever post what the live domain ReviewService accepts: a single
// standalone comment per inline submit (the domain has no append-to-pending-review
// method yet) and a PR-level Approve, Request changes, or Comment verdict.

// ReviewState is the display state of a submitted review, the same five the domain
// ReviewService stores. PENDING never reaches the timeline (a draft is private to
// its author until submitted), but it is in the enum so the mapping is total.
type ReviewState int

const (
	ReviewStatePending          ReviewState = iota // a private draft, never shown
	ReviewStateApproved                            // green check
	ReviewStateChangesRequested                    // red request-changes
	ReviewStateCommented                           // gray comment
	ReviewStateDismissed                           // struck-through, no longer counts
)

// DeriveReviewState maps the domain review state string onto the display state. It
// takes the raw string rather than a domain value so fe/view stays domain-free; an
// unknown value reads as a plain comment, the most neutral rendering.
func DeriveReviewState(state string) ReviewState {
	switch state {
	case "APPROVED":
		return ReviewStateApproved
	case "CHANGES_REQUESTED":
		return ReviewStateChangesRequested
	case "DISMISSED":
		return ReviewStateDismissed
	case "PENDING":
		return ReviewStatePending
	default:
		return ReviewStateCommented
	}
}

// Label is the human phrase the timeline header shows after the reviewer's login.
func (s ReviewState) Label() string {
	switch s {
	case ReviewStateApproved:
		return "approved these changes"
	case ReviewStateChangesRequested:
		return "requested changes"
	case ReviewStateDismissed:
		return "review dismissed"
	default:
		return "reviewed"
	}
}

// Icon is the octicon for the review state, every name registered in the icon set
// (the coverage test guarantees it).
func (s ReviewState) Icon() string {
	switch s {
	case ReviewStateApproved:
		return "check-circle"
	case ReviewStateChangesRequested:
		return "file-diff"
	case ReviewStateDismissed:
		return "x-circle"
	default:
		return "eye"
	}
}

// Modifier is the CSS modifier the stylesheet colors the review header by: approved
// green, changes requested red, dismissed and commented gray.
func (s ReviewState) Modifier() string {
	switch s {
	case ReviewStateApproved:
		return "approved"
	case ReviewStateChangesRequested:
		return "changes"
	case ReviewStateDismissed:
		return "dismissed"
	default:
		return "commented"
	}
}

// ReviewStateVM flattens the derived state into the printable header model, so the
// template prints fields instead of calling methods.
type ReviewStateVM struct {
	Label    string
	Icon     string
	Modifier string
}

// StateVM flattens the derived review state.
func (s ReviewState) StateVM() ReviewStateVM {
	return ReviewStateVM{Label: s.Label(), Icon: s.Icon(), Modifier: s.Modifier()}
}

// ReviewCommentVM is one comment inside an inline thread: the author chip, the
// GFM-rendered body, the raw source for an edit box, the timestamps, and the
// permalink anchor. It mirrors CommentVM but carries the discussion anchor the
// review thread uses rather than the issuecomment anchor.
type ReviewCommentVM struct {
	ID         int64
	Author     UserChipVM
	Body       template.HTML
	BodySource string
	CreatedAt  string
	CreatedISO string
	Edited     bool
	Anchor     string // "discussion_r{id}" for the permalink fragment
	URL        string
}

// ReviewThreadVM is one inline review thread anchored to a diff row by its persisted
// (path, line, side): the comments in posting order, the resolved and outdated
// flags, and the reply and resolve form targets with the affordance gates. The
// builder leaves the form fields empty for a viewer who cannot act, so the template
// shows the thread read-only without a second permission check.
type ReviewThreadVM struct {
	ID         int64
	Path       string
	Side       string // "LEFT" or "RIGHT"
	Line       int
	IsResolved bool
	IsOutdated bool
	Comments   []ReviewCommentVM

	CanReply   bool
	CanResolve bool
	ReplyURL   string // POST target that appends a reply to this thread
	ResolveURL string // POST target that toggles resolved
	CSRFToken  string
}

// ReplyCount is the number of replies after the first comment, for the collapsed
// thread summary the template shows.
func (t ReviewThreadVM) ReplyCount() int {
	if len(t.Comments) <= 1 {
		return 0
	}
	return len(t.Comments) - 1
}

// ResolveLabel is the verb the resolve button shows, the opposite of the current
// state so the one button toggles.
func (t ReviewThreadVM) ResolveLabel() string {
	if t.IsResolved {
		return "Unresolve conversation"
	}
	return "Resolve conversation"
}

// InlineComposerVM is the new-thread composer a commentable diff row reveals: the
// POST target, the anchor fields it submits (path, side, line, and the head commit
// id the comment pins to), and the CSRF token. The UI never submits a diff position;
// the anchor is the persisted (path, line, side) the domain validates against the
// diff for CommitID.
type InlineComposerVM struct {
	Action    string
	Path      string
	Side      string
	Line      int
	CommitID  string
	CSRFToken string
}

// ReviewOverlayVM is the "Review changes" panel that submits a PR-level verdict: the
// POST target, whether the viewer may approve or request changes (write access and
// not the PR author, the same rule the domain enforces), and the CSRF token. A
// viewer who can only comment still sees the overlay with the approve and request
// options disabled, so the affordance never lies about what the submit will accept.
type ReviewOverlayVM struct {
	Action     string
	CanApprove bool // write access and not the PR author
	CanComment bool // any signed-in viewer who can see the repo
	CSRFToken  string
}

// ReviewSummaryVM is a submitted review as the Conversation timeline shows it: the
// reviewer chip, the derived state header, the optional rendered body, the count of
// inline comments the review carried, and the permalink. A pending draft never
// becomes a summary; only submitted reviews reach the timeline.
type ReviewSummaryVM struct {
	Author       UserChipVM
	State        ReviewStateVM
	Body         template.HTML
	HasBody      bool
	SubmittedAt  string
	SubmittedISO string
	CommentCount int
	URL          string
}

// PRTimelineItem is one entry in the merged Conversation timeline: either a comment
// or a submitted review, tagged so the template dispatches to the right partial. The
// handler builds the merged list sorted by time, because a review and a comment
// interleave by when they happened, not by type.
type PRTimelineItem struct {
	Kind    string // "comment" or "review"
	Comment CommentVM
	Review  ReviewSummaryVM
}

// IsReview reports whether the item is a submitted review, for the template switch.
func (i PRTimelineItem) IsReview() bool { return i.Kind == "review" }

// ReviewSurfaceVM is the page-level review context the Files tab carries: whether the
// viewer may open inline threads, the POST target a new inline comment submits to,
// the head commit id every new thread pins to, the CSRF token, and the overlay model
// for the PR-level verdict. The per-row composer reads the file's path and the row's
// (side, line); the surface supplies the rest so a row never builds a form on its own.
type ReviewSurfaceVM struct {
	CanComment    bool
	CommentAction string
	CommitID      string
	CSRFToken     string
	Overlay       ReviewOverlayVM
}

package view

// pulls_render.go holds the presentation accessors the pull-request templates read
// so a template never compares against an unexported MergeBoxState integer. They
// add no state; they read the derived state and turn it into the booleans, the CSS
// token, and the headline a template can branch on, the same way PRState exposes a
// flattened StateVM.

// IsComputing reports whether the box is still waiting on the mergeability worker,
// the one state that polls itself until it resolves.
func (s MergeBoxState) IsComputing() bool { return s == MergeComputing }

// IsClean reports whether the PR is mergeable with no conflicts, the green-control
// state.
func (s MergeBoxState) IsClean() bool { return s == MergeClean }

// IsBehind reports whether the head trails the base but does not conflict, which
// still merges (a merge commit subsumes the catch-up) but shows an update note.
func (s MergeBoxState) IsBehind() bool { return s == MergeBehind }

// IsDirty reports whether the PR has conflicts that block an in-app merge.
func (s MergeBoxState) IsDirty() bool { return s == MergeDirty }

// IsDraft reports whether the PR is a draft, which shows the ready-for-review note
// instead of a merge control.
func (s MergeBoxState) IsDraft() bool { return s == MergeDraft }

// IsMerged reports whether the PR is already merged, the post-merge panel.
func (s MergeBoxState) IsMerged() bool { return s == MergeMerged }

// IsClosed reports whether the PR is closed unmerged.
func (s MergeBoxState) IsClosed() bool { return s == MergeClosed }

// IsBlocked reports whether a required review or check is unmet (F5/F9 fill this).
func (s MergeBoxState) IsBlocked() bool { return s == MergeBlocked }

// Mergeable reports whether the box should offer a green merge control: a clean or
// a behind-but-not-conflicting PR. The handler gates ViewerCanMerge on write access
// on top of this; the template shows the control only when both agree.
func (s MergeBoxState) Mergeable() bool {
	return s == MergeClean || s == MergeBehind
}

// Modifier is the CSS token the stylesheet colors the box header by.
func (s MergeBoxState) Modifier() string {
	switch s {
	case MergeClean, MergeBehind:
		return "clean"
	case MergeDirty, MergeBlocked:
		return "blocked"
	case MergeUnstable:
		return "unstable"
	case MergeMerged:
		return "merged"
	case MergeClosed:
		return "closed"
	case MergeDraft:
		return "draft"
	default:
		return "computing"
	}
}

// Headline is the one-line status the box header shows for the state.
func (s MergeBoxState) Headline() string {
	switch s {
	case MergeClean:
		return "This branch has no conflicts with the base branch"
	case MergeBehind:
		return "This branch is out of date with the base branch"
	case MergeDirty:
		return "This branch has conflicts that must be resolved"
	case MergeDraft:
		return "This pull request is still a work in progress"
	case MergeBlocked:
		return "Merging is blocked"
	case MergeUnstable:
		return "Some checks were not successful"
	case MergeMerged:
		return "Pull request successfully merged and closed"
	case MergeClosed:
		return "This pull request is closed"
	default:
		return "Checking mergeability"
	}
}

// MergeIcon is the octicon the box header shows for the state, every name
// registered in the icon set (the coverage test guarantees it).
func (s MergeBoxState) MergeIcon() string {
	switch s {
	case MergeClean, MergeBehind:
		return "check"
	case MergeDirty, MergeBlocked, MergeUnstable:
		return "alert"
	case MergeMerged:
		return "git-merge"
	case MergeClosed:
		return "git-pull-request-closed"
	case MergeDraft:
		return "git-pull-request-draft"
	default:
		return "dot-fill"
	}
}

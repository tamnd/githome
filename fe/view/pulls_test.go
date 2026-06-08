package view

import "testing"

// These pin the two derivations the PR surface shares between the list and the
// detail: the four-state pill and the merge-box state. They are the one place the
// open/closed/merged/draft and the mergeable_state strings turn into display
// state, so the list mini-icon and the header pill can never disagree.

func TestDerivePRState(t *testing.T) {
	cases := []struct {
		name   string
		state  string
		merged bool
		draft  bool
		want   PRState
	}{
		{"open", "open", false, false, PRStateOpen},
		{"draft", "open", false, true, PRStateDraft},
		{"merged wins over closed", "closed", true, false, PRStateMerged},
		{"merged wins over draft", "open", true, true, PRStateMerged},
		{"closed unmerged", "closed", false, false, PRStateClosed},
		// A draft flag left set on a closed PR must not show Draft: closed wins.
		{"closed beats stale draft", "closed", false, true, PRStateClosed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DerivePRState(tc.state, tc.merged, tc.draft); got != tc.want {
				t.Errorf("DerivePRState(%q, %v, %v) = %v, want %v", tc.state, tc.merged, tc.draft, got, tc.want)
			}
		})
	}
}

func TestPRStateMetadata(t *testing.T) {
	// The pill icon must be a registered octicon name and the modifier a stable CSS
	// token; both are part of the rendered contract.
	cases := []struct {
		s        PRState
		label    string
		icon     string
		modifier string
	}{
		{PRStateOpen, "Open", "git-pull-request", "open"},
		{PRStateDraft, "Draft", "git-pull-request-draft", "draft"},
		{PRStateMerged, "Merged", "git-merge", "merged"},
		{PRStateClosed, "Closed", "git-pull-request-closed", "closed"},
	}
	for _, tc := range cases {
		vm := tc.s.StateVM()
		if vm.Label != tc.label || vm.Icon != tc.icon || vm.Modifier != tc.modifier {
			t.Errorf("state %v = %+v, want {%s %s %s}", tc.s, vm, tc.label, tc.icon, tc.modifier)
		}
	}
}

func TestDeriveMergeBoxState(t *testing.T) {
	cases := []struct {
		name    string
		merged  bool
		state   string
		mergeSt string
		want    MergeBoxState
	}{
		{"merged wins over everything", true, "closed", "clean", MergeMerged},
		{"closed unmerged", false, "closed", "dirty", MergeClosed},
		{"unknown polls", false, "open", "unknown", MergeComputing},
		{"empty mergeable_state polls", false, "open", "", MergeComputing},
		{"draft", false, "open", "draft", MergeDraft},
		{"dirty", false, "open", "dirty", MergeDirty},
		{"behind", false, "open", "behind", MergeBehind},
		{"clean", false, "open", "clean", MergeClean},
		// States the live worker does not produce yet still map, so the next
		// milestone does not need a reshape.
		{"blocked", false, "open", "blocked", MergeBlocked},
		{"unstable", false, "open", "unstable", MergeUnstable},
		{"has_hooks", false, "open", "has_hooks", MergeHasHooks},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveMergeBoxState(tc.merged, tc.state, tc.mergeSt); got != tc.want {
				t.Errorf("DeriveMergeBoxState(%v, %q, %q) = %v, want %v", tc.merged, tc.state, tc.mergeSt, got, tc.want)
			}
		})
	}
}

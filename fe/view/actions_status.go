package view

// The status vocabulary the checks surface shares. Spec 2005 doc 11 section 8
// names one function as the single source of truth for every check icon, color,
// and title, so the rollup pill, the check-run rows, the commit-status rows, and
// (later) the PR Checks tab never disagree about what a status looks like. Githome
// backs the check-run, check-suite, and commit-status state machines today, not
// the full Actions run engine, so this file maps exactly those enums and nothing
// it cannot back. The icons here are all registered in the asset icon set, so a
// precomputed StatusToken.Icon renders a real glyph rather than the dashed
// placeholder.

// StatusToken is the icon, color class, title, and spin flag for one check state.
// The view layer precomputes it into the row and pill VMs (the front's standing
// "view precomputes every string the template prints" rule), so a template prints
// .Token.Icon and .Token.ColorClass rather than calling a status helper. Spin is
// set only for an in-progress check, the one state the design system animates
// (and degrades to a static dot under prefers-reduced-motion or no-JS).
type StatusToken struct {
	Icon       string // a registered octicon name
	ColorClass string // a check-state-* CSS class (fe/assets settings/checks cascade)
	Title      string // the human label, e.g. "Successful"
	Spin       bool   // an in-progress check animates its icon
}

// The check-state color classes. They map to the same fgColor tokens the rest of
// the front uses, with the hex fallbacks for the two tokens the theme set does not
// generate (success and danger), matching the diff and hook-status conventions.
const (
	checkStateSuccess = "check-state-success"
	checkStateDanger  = "check-state-danger"
	checkStatePending = "check-state-pending"
	checkStateMuted   = "check-state-muted"
)

// CheckRunToken maps a check run's (status, conclusion) pair to its token. status
// is queued, in_progress, or completed (domain.CheckRun.Status); conclusion is set
// only once completed and is one of success, failure, neutral, cancelled,
// timed_out, action_required, skipped, or stale. An empty conclusion on a
// completed run reads as a bare completion. The enum strings are exactly the ones
// Spec 2003 doc 05 pins, so a renamed value surfaces as a drift the cross-product
// test catches rather than a silently wrong icon.
func CheckRunToken(status, conclusion string) StatusToken {
	if status != "completed" {
		switch status {
		case "in_progress":
			return StatusToken{Icon: "dot-fill", ColorClass: checkStatePending, Title: "In progress", Spin: true}
		case "waiting":
			return StatusToken{Icon: "dot-fill", ColorClass: checkStatePending, Title: "Waiting"}
		case "requested":
			return StatusToken{Icon: "dot-fill", ColorClass: checkStatePending, Title: "Requested"}
		default: // queued, pending, or any not-yet-running state
			return StatusToken{Icon: "dot-fill", ColorClass: checkStatePending, Title: "Queued"}
		}
	}
	switch conclusion {
	case "success":
		return StatusToken{Icon: "check-circle", ColorClass: checkStateSuccess, Title: "Successful"}
	case "failure":
		return StatusToken{Icon: "x-circle", ColorClass: checkStateDanger, Title: "Failing"}
	case "timed_out":
		return StatusToken{Icon: "x-circle", ColorClass: checkStateDanger, Title: "Timed out"}
	case "action_required":
		return StatusToken{Icon: "alert", ColorClass: checkStatePending, Title: "Action required"}
	case "cancelled":
		return StatusToken{Icon: "x-circle", ColorClass: checkStateMuted, Title: "Cancelled"}
	case "skipped":
		return StatusToken{Icon: "skip", ColorClass: checkStateMuted, Title: "Skipped"}
	case "neutral":
		return StatusToken{Icon: "dot-fill", ColorClass: checkStateMuted, Title: "Neutral"}
	case "stale":
		return StatusToken{Icon: "dot-fill", ColorClass: checkStateMuted, Title: "Stale"}
	default:
		return StatusToken{Icon: "dot-fill", ColorClass: checkStateMuted, Title: "Completed"}
	}
}

// CommitStatusToken maps a commit status's flat state (error, failure, pending,
// success) to its token. The error and failure states both read as a red failing
// mark, matching how the combined status folds error into failure.
func CommitStatusToken(state string) StatusToken {
	switch state {
	case "success":
		return StatusToken{Icon: "check-circle", ColorClass: checkStateSuccess, Title: "Successful"}
	case "failure":
		return StatusToken{Icon: "x-circle", ColorClass: checkStateDanger, Title: "Failing"}
	case "error":
		return StatusToken{Icon: "x-circle", ColorClass: checkStateDanger, Title: "Errored"}
	default: // pending
		return StatusToken{Icon: "dot-fill", ColorClass: checkStatePending, Title: "Pending"}
	}
}

// RollupToken maps a status-check rollup's worst-first verdict (the domain.Rollup*
// constants: ERROR, FAILURE, PENDING, SUCCESS, EXPECTED) to the token the page's
// summary pill shows. EXPECTED means a required context has not reported yet, so
// it reads as pending rather than as a pass.
func RollupToken(state string) StatusToken {
	switch state {
	case "SUCCESS":
		return StatusToken{Icon: "check-circle", ColorClass: checkStateSuccess, Title: "All checks have passed"}
	case "FAILURE":
		return StatusToken{Icon: "x-circle", ColorClass: checkStateDanger, Title: "Some checks were not successful"}
	case "ERROR":
		return StatusToken{Icon: "x-circle", ColorClass: checkStateDanger, Title: "Some checks reported errors"}
	case "EXPECTED":
		return StatusToken{Icon: "dot-fill", ColorClass: checkStatePending, Title: "Waiting for status to be reported"}
	default: // PENDING
		return StatusToken{Icon: "dot-fill", ColorClass: checkStatePending, Title: "Some checks haven't completed yet"}
	}
}

package route

// The dashboard URL builders address the global, viewer-scoped lists: the
// cross-repo "your issues" and "your pull requests" pages at the reserved
// top-level /issues and /pulls names. Like every builder in the package they
// are pure string functions; rawQuery is the already-encoded ?tab=/?q=/?page=
// string and an empty one yields the bare page.

// DashboardIssues is the global issues dashboard, /issues.
func DashboardIssues(rawQuery string) string {
	u := "/issues"
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	return u
}

// DashboardPulls is the global pull-request dashboard, /pulls.
func DashboardPulls(rawQuery string) string {
	u := "/pulls"
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	return u
}

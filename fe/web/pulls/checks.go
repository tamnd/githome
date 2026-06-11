package pulls

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/view"
)

// checks.go renders the PR Checks tab: the check runs reported against the pull
// request's head sha grouped by their check suite, beside the older flat commit
// statuses, under the same rollup verdict the merge box shows. The data is the
// backed slice of the Actions UI (check runs, suites, statuses, and their
// rollup, Spec 2003 docs 05 and 10); the run engine doc 11 sketches has no store,
// so there is no job graph or live log here, the same honest absence the
// standalone checks page takes. The tab reads through the same checks service
// the merge box and the REST surface use, so the tab badge, the box headline,
// and a concurrent API read never disagree. See implementation/09 and Spec 2005
// doc 11 section 8.

// Checks renders the Checks tab. A missing PR is the soft 404; a head sha with
// nothing reported renders the blankslate inside the shell. The route mounts
// only when the checks service is wired, the same gate the standalone checks
// page sits behind, so h.checks is non-nil here.
func (h *Handlers) Checks(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	pr, ok := h.loadPR(c, repo)
	if !ok {
		return nil
	}

	var (
		rollup *domain.StatusCheckRollup
		suites []*domain.CheckSuite
	)
	if pr.Head.SHA != "" {
		var err error
		rollup, err = h.checks.RollupForPull(ctx, repo, pr.Head.SHA)
		if err != nil {
			return h.render.ServerError(c, err)
		}
		suites, err = h.checks.SuitesForPull(ctx, repo, pr.Head.SHA)
		if err != nil {
			return h.render.ServerError(c, err)
		}
	}
	if rollup == nil {
		rollup = &domain.StatusCheckRollup{State: domain.RollupExpected, SHA: pr.Head.SHA}
	}

	title := pr.Title + " #" + strconv.FormatInt(pr.Number, 10)
	shell := h.shell(c, repo, pr, h.viewer(c).pk, "checks", title)
	rtok := view.RollupToken(rollup.State)
	vm := view.PRChecksVM{
		Chrome:      shell.Chrome,
		Shell:       shell,
		SHA:         rollup.SHA,
		ShortSHA:    shortSHA(rollup.SHA),
		Rollup:      rtok,
		RollupTitle: rtok.Title,
		Total:       rollup.TotalCount,
		Suites:      checkSuiteGroups(suites),
		Statuses:    commitStatusRows(rollup.Statuses),
		Empty:       rollup.TotalCount == 0,
	}
	return h.render.Page(c, "pulls/checks", vm)
}

// checkCount is the head sha's rollup total the shell's tab badge shows. It
// degrades to zero when the checks service is missing, the head is unknown, or
// the read fails, so a tab render never breaks on a checks hiccup.
func (h *Handlers) checkCount(c *mizu.Ctx, repo *domain.Repo, pr *domain.PullRequest) int {
	if h.checks == nil || pr.Head.SHA == "" {
		return 0
	}
	rollup, err := h.checks.RollupForPull(c.Context(), repo, pr.Head.SHA)
	if err != nil || rollup == nil {
		return 0
	}
	return rollup.TotalCount
}

// checkSuiteGroups maps the head's check suites into the tab's groups, skipping
// a suite that carries no runs so an empty container never renders a bare
// heading.
func checkSuiteGroups(suites []*domain.CheckSuite) []view.PRCheckSuiteVM {
	out := make([]view.PRCheckSuiteVM, 0, len(suites))
	for _, s := range suites {
		if len(s.Runs) == 0 {
			continue
		}
		g := view.PRCheckSuiteVM{App: s.AppSlug, Runs: make([]view.PRCheckRunRowVM, 0, len(s.Runs))}
		for _, r := range s.Runs {
			g.Runs = append(g.Runs, checkRunRow(r))
		}
		out = append(out, g)
	}
	return out
}

// checkRunRow maps one check run into its tab row, precomputing the shared
// status token, the one-line summary, the duration, the sanitized details link,
// and the time line.
func checkRunRow(r *domain.CheckRun) view.PRCheckRunRowVM {
	row := view.PRCheckRunRowVM{
		ID:       r.ID,
		Name:     r.Name,
		Token:    view.CheckRunToken(r.Status, strDeref(r.Conclusion)),
		Summary:  checkRunSummary(r),
		Duration: checkRunDuration(r),
	}
	if u := safeExternalURL(strDeref(r.DetailsURL)); u != "" {
		row.DetailsURL = u
		row.HasDetails = true
	}
	row.WhenVerb, row.WhenISO, row.WhenHuman = checkRunTiming(r)
	return row
}

// commitStatusRows maps the rollup's latest-per-context commit statuses into the
// shared status rows, with the sanitized target link.
func commitStatusRows(statuses []*domain.CommitStatus) []view.CommitStatusRowVM {
	out := make([]view.CommitStatusRowVM, 0, len(statuses))
	for _, s := range statuses {
		row := view.CommitStatusRowVM{
			Context:     s.Context,
			Token:       view.CommitStatusToken(s.State),
			Description: strDeref(s.Description),
		}
		if u := safeExternalURL(strDeref(s.TargetURL)); u != "" {
			row.TargetURL = u
			row.HasTarget = true
		}
		out = append(out, row)
	}
	return out
}

// checkRunSummary is the one-line summary a run row shows: the output title when
// set, falling back to the output summary.
func checkRunSummary(r *domain.CheckRun) string {
	if t := strDeref(r.OutputTitle); t != "" {
		return t
	}
	return strDeref(r.OutputSummary)
}

// checkRunDuration is the human run length, present only once the run has both
// its started and completed timestamps.
func checkRunDuration(r *domain.CheckRun) string {
	if r.StartedAt == nil || r.CompletedAt == nil {
		return ""
	}
	d := r.CompletedAt.Sub(*r.StartedAt)
	if d < 0 {
		return ""
	}
	return formatDuration(d)
}

// formatDuration renders a duration the way the run rows read it: "Ns" under a
// minute, "Mm Ss" under an hour, "Hh Mm" beyond, always at least "0s".
func formatDuration(d time.Duration) string {
	total := int(d.Round(time.Second).Seconds())
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	switch {
	case h > 0:
		return strconv.Itoa(h) + "h " + strconv.Itoa(m) + "m"
	case m > 0:
		return strconv.Itoa(m) + "m " + strconv.Itoa(s) + "s"
	default:
		return strconv.Itoa(s) + "s"
	}
}

// checkRunTiming returns the verb and the most-advanced timestamp a run reached
// (finished, started, or queued), the same line the standalone checks page
// shows. The ISO string feeds the <relative-time> element and the human string
// is its no-JS fallback.
func checkRunTiming(r *domain.CheckRun) (verb, iso, human string) {
	switch {
	case r.CompletedAt != nil:
		return "Finished", isoTime(*r.CompletedAt), humanTime(*r.CompletedAt)
	case r.StartedAt != nil:
		return "Started", isoTime(*r.StartedAt), humanTime(*r.StartedAt)
	case !r.CreatedAt.IsZero():
		return "Queued", isoTime(r.CreatedAt), humanTime(r.CreatedAt)
	default:
		return "", "", ""
	}
}

// safeExternalURL returns the URL only when it is an absolute http or https URL,
// the same guard the standalone checks page and the hook URL validation apply. A
// check run's details link and a commit status's target are reported by an
// external client, so a relative, scheme-relative, or javascript: value is
// dropped rather than rendered into an href.
func safeExternalURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return raw
}

// strDeref returns the string a pointer points at, or empty when nil, for the
// many optional check fields.
func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func isoTime(t time.Time) string   { return t.UTC().Format(time.RFC3339) }
func humanTime(t time.Time) string { return t.UTC().Format("Jan 2, 2006, 3:04 PM") }

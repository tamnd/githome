package checks

import (
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

// build maps the resolved repository and the ref's rollup into the page view
// model. It is the one place a domain check value becomes a VM string, keeping
// fe/view domain-free. The header bar reuses the repo context the other repo pages
// show; the rows precompute the shared status token so the template prints icons
// and colors without a status helper.
func (h *Handlers) build(c *mizu.Ctx, repo *domain.Repo, ref string, rollup *domain.StatusCheckRollup) view.ChecksPageVM {
	owner, name := h.owner(c), h.name(c)
	rtok := view.RollupToken(rollup.State)
	vm := view.ChecksPageVM{
		Chrome:      h.view.Chrome(c, name+" checks"),
		Header:      h.header(c, repo),
		Nav:         h.nav(c, ref),
		Repo:        view.RepoRef{Owner: owner, Name: name, URL: route.Repo(owner, name)},
		Ref:         ref,
		SHA:         rollup.SHA,
		ShortSHA:    shortSHA(rollup.SHA),
		Rollup:      rtok,
		RollupTitle: rtok.Title,
		Total:       rollup.TotalCount,
		Runs:        checkRunRows(rollup.CheckRuns),
		Statuses:    commitStatusRows(rollup.Statuses),
		Empty:       rollup.TotalCount == 0,
	}
	return vm
}

// header builds the repo context bar. The checks page is not one of the underline
// tabs, so ActiveTab is empty and no tab is marked current; the bar is there for
// the repository name, visibility, and the link back into the repository.
func (h *Handlers) header(c *mizu.Ctx, repo *domain.Repo) view.RepoHeaderVM {
	owner, name := h.owner(c), h.name(c)
	pk := webmw.ViewerID(c.Context())
	hdr := view.RepoHeaderVM{
		Owner:       owner,
		Name:        name,
		OwnerURL:    "/" + owner,
		URL:         route.Repo(owner, name),
		Private:     repo.Private,
		Fork:        repo.Fork,
		OpenIssues:  repo.OpenIssuesCount,
		CanSettings: pk != 0 && pk == repo.OwnerPK,
	}
	if repo.Description != nil {
		hdr.Description = *repo.Description
	}
	return hdr
}

// nav builds the repo underline-nav link set, the same one every repo page shows,
// so the checks page links into the rest of the repository with the same URLs. The
// Commits link carries the ref the checks anchor to.
func (h *Handlers) nav(c *mizu.Ctx, ref string) view.TreeNav {
	owner, name := h.owner(c), h.name(c)
	return view.TreeNav{
		CodeURL:     route.Repo(owner, name),
		IssuesURL:   route.Issues(owner, name, ""),
		PullsURL:    route.Pulls(owner, name, ""),
		CommitsURL:  route.Commits(owner, name, ref, ""),
		BranchesURL: route.Branches(owner, name),
		TagsURL:     route.Tags(owner, name),
		SettingsURL: route.RepoSettings(owner, name),
	}
}

// checkRunRows maps the rollup's check runs into row VMs, each carrying the shared
// status token, the one-line output summary, the sanitized details link, and the
// most-advanced timestamp the run reached.
func checkRunRows(runs []*domain.CheckRun) []view.CheckRunRowVM {
	out := make([]view.CheckRunRowVM, 0, len(runs))
	for _, r := range runs {
		row := view.CheckRunRowVM{
			Name:    r.Name,
			Token:   view.CheckRunToken(r.Status, deref(r.Conclusion)),
			Summary: runSummary(r),
		}
		if u := safeExternalURL(deref(r.DetailsURL)); u != "" {
			row.DetailsURL = u
			row.HasDetails = true
		}
		row.WhenVerb, row.WhenISO, row.WhenHuman = runTiming(r)
		out = append(out, row)
	}
	return out
}

// commitStatusRows maps the rollup's latest-per-context commit statuses into row
// VMs with the shared status token, the description, and the sanitized target.
func commitStatusRows(statuses []*domain.CommitStatus) []view.CommitStatusRowVM {
	out := make([]view.CommitStatusRowVM, 0, len(statuses))
	for _, s := range statuses {
		row := view.CommitStatusRowVM{
			Context:     s.Context,
			Token:       view.CommitStatusToken(s.State),
			Description: deref(s.Description),
		}
		if u := safeExternalURL(deref(s.TargetURL)); u != "" {
			row.TargetURL = u
			row.HasTarget = true
		}
		out = append(out, row)
	}
	return out
}

// runSummary is the one-line summary a check-run row shows: the output title when
// set, falling back to the output summary, so a run that reported only a summary
// still shows it and one that reported neither shows nothing.
func runSummary(r *domain.CheckRun) string {
	if t := deref(r.OutputTitle); t != "" {
		return t
	}
	return deref(r.OutputSummary)
}

// runTiming returns the verb and the most-advanced timestamp a check run reached:
// finished when completed, started when running, queued otherwise. The ISO string
// feeds the <relative-time> element and the human string is its no-JS fallback. A
// run with no timestamp at all returns empty strings and the row omits the line.
func runTiming(r *domain.CheckRun) (verb, iso, human string) {
	switch {
	case r.CompletedAt != nil:
		return "Finished", isoOf(*r.CompletedAt), humanOf(*r.CompletedAt)
	case r.StartedAt != nil:
		return "Started", isoOf(*r.StartedAt), humanOf(*r.StartedAt)
	case !r.CreatedAt.IsZero():
		return "Queued", isoOf(r.CreatedAt), humanOf(r.CreatedAt)
	default:
		return "", "", ""
	}
}

// deref returns the string a pointer points at, or the empty string when nil, the
// small helper the row builders use for the many optional domain fields.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// shortSHA abbreviates a sha to seven characters, the length the rest of the front
// shows; a shorter value is returned unchanged.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func isoOf(t time.Time) string   { return t.UTC().Format(time.RFC3339) }
func humanOf(t time.Time) string { return t.UTC().Format("Jan 2, 2006, 3:04 PM") }

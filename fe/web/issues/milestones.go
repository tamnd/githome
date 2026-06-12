package issues

import (
	"errors"
	"strconv"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// Milestones renders the milestone list: GET /{owner}/{repo}/milestones, with
// ?state=closed for the closed tab. One all-states read feeds both the rows
// and the honest tab counts. Milestone management stays in the REST API; this
// page is the browse surface. See spec 02 section 3.6.
func (h *Handlers) Milestones(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	owner := ownerLogin(repo)
	vc := h.viewer(c)

	state := "open"
	if c.Query("state") == "closed" {
		state = "closed"
	}

	all, err := h.issues.ListMilestones(ctx, vc.pk, owner, repo.Name, "all")
	if err != nil {
		return h.listError(c, err)
	}

	var rows []view.MilestoneRowVM
	var openCount, closedCount int
	for _, m := range all {
		if m.State == "closed" {
			closedCount++
		} else {
			openCount++
		}
		if m.State == state {
			rows = append(rows, milestoneRow(owner, repo.Name, m))
		}
	}

	vm := view.MilestonesVM{
		Chrome:    h.chrome(c, "Milestones · "+owner+"/"+repo.Name),
		Header:    h.header(repo),
		Nav:       h.nav(repo),
		Repo:      repoRef(repo),
		OpenTab:   view.FilterTab{Label: "Open", Count: openCount, URL: route.Milestones(owner, repo.Name, ""), IsActive: state == "open"},
		ClosedTab: view.FilterTab{Label: "Closed", Count: closedCount, URL: route.Milestones(owner, repo.Name, "closed"), IsActive: state == "closed"},
		Items:     rows,
	}
	return h.render.Page(c, "issues/milestones", vm)
}

// Milestone renders one milestone's page: GET /{owner}/{repo}/milestone/{number},
// github.com's singular form. The header block carries the progress and due
// date; below it the milestone's issues render as the same bounded rows the
// issues index uses, tabbed open (?closed=1 for closed). A missing milestone
// is the soft 404.
func (h *Handlers) Milestone(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	owner := ownerLogin(repo)
	vc := h.viewer(c)

	number, err := strconv.ParseInt(c.Param("number"), 10, 64)
	if err != nil || number < 1 {
		return h.notFound(c)
	}
	m, err := h.issues.GetMilestone(ctx, vc.pk, owner, repo.Name, number)
	if errors.Is(err, domain.ErrMilestoneNotFound) || isNotFound(err) {
		return h.notFound(c)
	}
	if err != nil {
		return err
	}

	state := "open"
	if c.Query("closed") == "1" {
		state = "closed"
	}
	page := pageParam(c.Query("page"))

	dq := domain.IssueQuery{
		State:           state,
		MilestoneNumber: &number,
		Sort:            "created",
		Direction:       "desc",
		Page:            page,
		PerPage:         indexPerPage,
	}
	issues, total, err := h.issues.ListIssues(ctx, vc.pk, owner, repo.Name, dq)
	if err != nil {
		return h.listError(c, err)
	}

	pager := view.Pager{Page: page}
	if page > 1 {
		pager.PrevURL = milestonePageURL(owner, repo.Name, number, state, page-1)
	}
	if page*indexPerPage < total {
		pager.NextURL = milestonePageURL(owner, repo.Name, number, state, page+1)
	}

	vm := view.MilestoneDetailVM{
		Chrome:    h.chrome(c, m.Title+" · "+owner+"/"+repo.Name),
		Header:    h.header(repo),
		Nav:       h.nav(repo),
		Repo:      repoRef(repo),
		Milestone: milestoneRow(owner, repo.Name, m),
		OpenTab:   view.FilterTab{Label: "Open", Count: m.OpenIssues, URL: route.Milestone(owner, repo.Name, number, false), IsActive: state == "open"},
		ClosedTab: view.FilterTab{Label: "Closed", Count: m.ClosedIssues, URL: route.Milestone(owner, repo.Name, number, true), IsActive: state == "closed"},
		Rows:      h.rows(repo, issues),
		Pager:     pager,
	}
	if len(vm.Rows) == 0 {
		vm.Empty = true
		vm.EmptyReason = "No " + state + " issues in this milestone."
	}
	return h.render.Page(c, "issues/milestone", vm)
}

// milestonePageURL is the milestone page at a given issue-state tab and page
// number, keeping the two query knobs composed consistently.
func milestonePageURL(owner, name string, number int64, state string, page int) string {
	u := route.Milestone(owner, name, number, state == "closed")
	sep := "?"
	if state == "closed" {
		sep = "&"
	}
	if page > 1 {
		u += sep + "page=" + strconv.Itoa(page)
	}
	return u
}

// milestoneRow maps a milestone into its row: the formatted due/closed lines,
// the overdue flag, and the integer completeness percentage.
func milestoneRow(owner, name string, m *domain.Milestone) view.MilestoneRowVM {
	row := view.MilestoneRowVM{
		Number:       m.Number,
		Title:        m.Title,
		URL:          route.Milestone(owner, name, m.Number, false),
		State:        m.State,
		OpenIssues:   m.OpenIssues,
		ClosedIssues: m.ClosedIssues,
	}
	if m.Description != nil {
		row.Description = *m.Description
	}
	if m.DueOn != nil {
		due := m.DueOn.UTC()
		row.DueOn = due.Format("Jan 2, 2006")
		row.DueISO = due.Format(time.RFC3339)
		row.Overdue = m.State == "open" && due.Before(time.Now().UTC())
	}
	if m.ClosedAt != nil {
		row.ClosedAt = m.ClosedAt.UTC().Format("Jan 2, 2006")
	}
	if total := m.OpenIssues + m.ClosedIssues; total > 0 {
		row.Percent = m.ClosedIssues * 100 / total
	}
	return row
}

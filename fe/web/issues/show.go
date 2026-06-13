package issues

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// showPerPage is how many comments one detail page loads. A very long thread
// pages, the same bounded window the index uses.
const showPerPage = 100

// Show renders the issue detail page: the title and state header, the comment
// timeline (the opening body first, then the comments oldest-first), the sidebar
// with the labels, assignees, and milestone, and the composer. A missing issue,
// or one in a repo the viewer cannot see, renders the soft 404, never a 403. See
// implementation/08 section 4.
func (h *Handlers) Show(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	owner := ownerLogin(repo)

	number, ok := numberParam(c.Param("number"))
	if !ok {
		return h.notFound(c)
	}
	vc := h.viewer(c)

	iss, err := h.issues.GetIssue(ctx, vc.pk, owner, repo.Name, number)
	if isNotFound(err) {
		return h.notFound(c)
	}
	if err != nil {
		return err
	}
	// Issues and pull requests share one number sequence; a PR addressed
	// through /issues/{n} redirects to its own page, matching github.com.
	if iss.IsPull {
		return c.Redirect(http.StatusFound, route.Pull(owner, repo.Name, number))
	}

	comments, err := h.issues.ListComments(ctx, vc.pk, owner, repo.Name, number, 1, showPerPage)
	if err != nil {
		return err
	}

	vm := h.detail(ctx, c, repo, iss, comments, vc, "")
	return h.render.Page(c, "issues/show", vm)
}

// detail assembles the issue detail view model. formError, when non-empty, is a
// validation message echoed back into the composer after a failed mutation that
// re-renders the page (the no-JS error path).
func (h *Handlers) detail(ctx context.Context, c *mizu.Ctx, repo *domain.Repo, iss *domain.Issue, comments []*domain.Comment, vc viewerCtx, formError string) view.IssueDetailVM {
	owner := ownerLogin(repo)
	write := canWrite(repo, vc.pk)
	open := iss.State == "open"

	vm := view.IssueDetailVM{
		Chrome:    h.chrome(c, iss.Title+" #"+strconv.FormatInt(iss.Number, 10)),
		Header:    h.header(c.Context(), repo),
		Nav:       h.nav(repo),
		Repo:      repoRef(repo),
		Number:    iss.Number,
		Title:     iss.Title,
		State:     stateBadge(iss),
		Author:    h.userChip(iss.User),
		OpenedAt:  iss.CreatedAt.UTC().Format("Jan 2, 2006"),
		OpenedISO: iss.CreatedAt.UTC().Format(time.RFC3339),
		Locked:    iss.Locked,
		CanEdit:   write,
		FormError: formError,
	}
	if write {
		vm.EditURL = route.IssueTitle(owner, repo.Name, iss.Number)
	}

	// The opening body is the first timeline item: the issue author and the issue
	// body, carrying the issue's reaction rollup rather than a comment's.
	body := ""
	if iss.Body != nil {
		body = *iss.Body
	}
	opening := view.CommentVM{
		ID:         0,
		Author:     h.userChip(iss.User),
		Body:       h.renderBody(ctx, repo, body),
		BodySource: body,
		CreatedAt:  iss.CreatedAt.UTC().Format("Jan 2, 2006"),
		CreatedISO: iss.CreatedAt.UTC().Format(time.RFC3339),
		IsAuthor:   true,
		Anchor:     "issue-" + strconv.FormatInt(iss.Number, 10),
		URL:        route.Issue(owner, repo.Name, iss.Number),
		Reactions:  reactionsRollup("issue", route.IssueReactions(owner, repo.Name, iss.Number), iss.Reactions, vc.pk != 0),
	}
	// The opening body is edited through the issue, not as a comment, so it reuses
	// the title edit target's neighbor: the body edit posts to the same edit endpoint
	// as the title. Until the body editor lands, only the title pencil shows, so the
	// opening comment leaves CanEdit off regardless of write access.
	vm.Timeline = append(vm.Timeline, opening)
	for _, cm := range comments {
		vm.Timeline = append(vm.Timeline, h.comment(ctx, repo, iss.Number, cm, vc))
	}

	vm.Sidebar = view.SidebarVM{
		Assignees: h.userChips(iss.Assignees),
		Labels:    labelChips(owner, repo.Name, iss.Labels),
		Milestone: milestoneChip(owner, repo.Name, iss.Milestone),
		CanEdit:   write,
	}
	if write {
		vm.Sidebar.EditURL = route.IssueEdit(owner, repo.Name, iss.Number)
	}

	vm.Composer = view.ComposerVM{
		Action:      route.IssueComments(owner, repo.Name, iss.Number),
		CanComment:  canComment(vc.pk),
		CanClose:    write,
		IssueOpen:   open,
		CloseAction: route.IssueState(owner, repo.Name, iss.Number),
	}
	if open {
		vm.Composer.CloseLabel = "Close issue"
	} else {
		vm.Composer.CloseLabel = "Reopen issue"
	}
	vm.Reactions = opening.Reactions
	return vm
}

// New renders the new-issue form, seeded from the documented prefill query
// (?title=&body=&labels=&assignees=&milestone=&template=). A viewer who cannot
// write still sees the form shell, and the create handler authorizes the
// submit, so the affordance and the action stay consistent. See
// implementation/08 section 10.
func (h *Handlers) New(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	owner := ownerLogin(repo)
	vc := h.viewer(c)
	vm := view.NewIssueVM{
		Chrome:    h.chrome(c, "New issue"),
		Header:    h.header(c.Context(), repo),
		Nav:       h.nav(repo),
		Repo:      repoRef(repo),
		Action:    route.Issues(owner, repo.Name, ""),
		CanSubmit: canComment(vc.pk) && canWrite(repo, vc.pk),
	}
	h.prefillNewIssue(c, repo, &vm)
	return h.render.Page(c, "issues/new", vm)
}

// numberParam parses the {number} path parameter into a positive issue number.
func numberParam(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

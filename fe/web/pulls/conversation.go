package pulls

import (
	"context"
	"strconv"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// timelinePerPage is how many comments the Conversation tab loads. A very long
// thread pages, the same bounded window the issue detail uses.
const timelinePerPage = 100

// Conversation renders the PR Conversation tab: the shell, the comment timeline
// (the opening body first, then the comments oldest-first), the new-comment
// composer, and the merge box. A PR shares its number and its comments with an
// issue, so the timeline is the same CommentVM stream the issue detail renders,
// read through the issue service on the PR's number. A missing PR, or one in a
// repo the viewer cannot see, renders the soft 404, never a 403. See
// implementation/09 section 3.
func (h *Handlers) Conversation(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	pr, ok := h.loadPR(c, repo)
	if !ok {
		return nil
	}
	vc := h.viewer(c)

	vm, err := h.conversation(ctx, c, repo, pr, vc, "")
	if err != nil {
		return h.render.ServerError(c, err)
	}
	return h.render.Page(c, "pulls/conversation", vm)
}

// conversation assembles the Conversation view model. formError, when non-empty,
// is a validation message echoed back into the composer after a failed mutation
// that re-renders the page (the no-JS error path). The opening body and its
// reaction rollup come from the issue row the PR shares its number with, since the
// pull row carries no body of its own.
func (h *Handlers) conversation(ctx context.Context, c *mizu.Ctx, repo *domain.Repo, pr *domain.PullRequest, vc viewerCtx, formError string) (view.PRConversationVM, error) {
	owner := ownerLogin(repo)

	iss, err := h.issues.GetIssue(ctx, vc.pk, owner, repo.Name, pr.Number)
	if err != nil {
		return view.PRConversationVM{}, err
	}
	comments, err := h.issues.ListComments(ctx, vc.pk, owner, repo.Name, pr.Number, 1, timelinePerPage)
	if err != nil {
		return view.PRConversationVM{}, err
	}

	title := pr.Title + " #" + strconv.FormatInt(pr.Number, 10)
	shell := h.shell(c, repo, pr, vc.pk, "conversation", title)

	vm := view.PRConversationVM{
		Chrome:    shell.Chrome,
		Shell:     shell,
		FormError: formError,
	}

	// The opening body is the first timeline item: the PR author and the shared
	// issue body, carrying the issue's reaction rollup rather than a comment's.
	body := ""
	if iss.Body != nil {
		body = *iss.Body
	}
	opening := view.CommentVM{
		ID:         0,
		Author:     h.userChip(pr.User),
		Body:       h.renderBody(ctx, repo, body),
		BodySource: body,
		CreatedAt:  pr.CreatedAt.UTC().Format("Jan 2, 2006"),
		CreatedISO: pr.CreatedAt.UTC().Format(time.RFC3339),
		IsAuthor:   true,
		Anchor:     "issue-" + strconv.FormatInt(pr.Number, 10),
		URL:        route.Pull(owner, repo.Name, pr.Number),
		Reactions:  reactionsRollup("issue", route.IssueReactions(owner, repo.Name, pr.Number), iss.Reactions, vc.pk != 0),
	}
	vm.Timeline = append(vm.Timeline, opening)
	for _, cm := range comments {
		vm.Timeline = append(vm.Timeline, h.comment(ctx, repo, pr.Number, cm, vc))
	}
	vm.Reactions = opening.Reactions

	open := pr.State == "open"
	vm.Composer = view.ComposerVM{
		Action:      route.PullComments(owner, repo.Name, pr.Number),
		CanComment:  canComment(vc.pk),
		CanClose:    canWrite(repo, vc.pk) && !pr.Merged,
		IssueOpen:   open,
		CloseAction: route.PullState(owner, repo.Name, pr.Number),
	}
	if open {
		vm.Composer.CloseLabel = "Close pull request"
	} else {
		vm.Composer.CloseLabel = "Reopen pull request"
	}

	vm.MergeBox = h.mergeBox(c, repo, pr, vc)
	return vm, nil
}

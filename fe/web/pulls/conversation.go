package pulls

import (
	"context"
	"sort"
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

	// The timeline interleaves comments and submitted reviews by when they happened,
	// not by type, so a review that landed between two comments reads in order. The
	// opening body is pinned first (it is the PR's creation, earlier than any item).
	reviews, err := h.submittedReviews(ctx, owner, repo.Name, pr.Number)
	if err != nil {
		return view.PRConversationVM{}, err
	}
	vm.Timeline = h.buildTimeline(ctx, repo, pr, opening, pr.CreatedAt, comments, reviews, vc)
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

// submittedReviews lists the pull request's submitted reviews for the timeline. The
// review service excludes a viewer's own pending draft, so a draft stays private
// until its author submits it. With no review service wired (a partial test setup)
// it returns no reviews rather than failing the page.
func (h *Handlers) submittedReviews(ctx context.Context, owner, name string, number int64) ([]*domain.Review, error) {
	if h.reviews == nil {
		return nil, nil
	}
	reviews, err := h.reviews.ListReviews(ctx, 0, owner, name, number)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return reviews, nil
}

// buildTimeline merges the opening body, the comments, and the submitted reviews
// into one timeline ordered by time. Each entry carries its own timestamp so a
// review and a comment interleave by when they happened; the opening body is pinned
// first since it is the pull request's creation. A review with no body and no inline
// comments is dropped, since it carries nothing to show (a pending draft that was
// discarded leaves no trace).
func (h *Handlers) buildTimeline(ctx context.Context, repo *domain.Repo, pr *domain.PullRequest, opening view.CommentVM, openedAt time.Time, comments []*domain.Comment, reviews []*domain.Review, vc viewerCtx) []view.PRTimelineItem {
	type entry struct {
		when time.Time
		item view.PRTimelineItem
	}
	entries := make([]entry, 0, 1+len(comments)+len(reviews))
	entries = append(entries, entry{when: openedAt, item: view.PRTimelineItem{Kind: "comment", Comment: opening}})
	for _, cm := range comments {
		entries = append(entries, entry{
			when: cm.CreatedAt,
			item: view.PRTimelineItem{Kind: "comment", Comment: h.comment(ctx, repo, pr.Number, cm, vc)},
		})
	}
	for _, r := range reviews {
		if r.Body == "" && len(r.Comments) == 0 {
			continue
		}
		when := r.CreatedAt
		if r.SubmittedAt != nil {
			when = *r.SubmittedAt
		}
		entries = append(entries, entry{
			when: when,
			item: view.PRTimelineItem{Kind: "review", Review: h.reviewSummary(ctx, repo, pr, r)},
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].when.Before(entries[j].when)
	})
	out := make([]view.PRTimelineItem, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.item)
	}
	return out
}

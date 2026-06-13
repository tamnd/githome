package pulls

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// review_build.go maps the domain review objects (threads, comments, submitted
// reviews) into the fe/view review models, the F5 companion to build.go. It keeps
// fe/view domain-free by doing the mapping here and precomputes every URL, anchor,
// and token through fe/route. The load-bearing piece is attachThreads: it hangs each
// live thread off the diff row its persisted (path, line, side) anchor matches, the
// same anchor the domain resolver and the API read, so a thread opened in the
// browser and one opened with gh pr review land on the same line. A thread whose
// anchor no longer maps onto the current diff is outdated and renders in a per-file
// group rather than against a row. See implementation/09 section 4.

// reviewComment maps a domain review comment into its inline thread view model,
// carrying the discussion anchor the Files tab links to.
func (h *Handlers) reviewComment(ctx context.Context, repo *domain.Repo, number int64, rc *domain.ReviewComment) view.ReviewCommentVM {
	owner := ownerLogin(repo)
	return view.ReviewCommentVM{
		ID:         rc.ID,
		Author:     h.userChip(rc.User),
		Body:       h.renderBody(ctx, repo, rc.Body),
		BodySource: rc.Body,
		CreatedAt:  rc.CreatedAt.UTC().Format("Jan 2, 2006"),
		CreatedISO: rc.CreatedAt.UTC().Format(time.RFC3339),
		Edited:     rc.UpdatedAt.After(rc.CreatedAt),
		Anchor:     "discussion_r" + strconv.FormatInt(rc.ID, 10),
		URL:        route.PullReviewComment(owner, repo.Name, number, rc.ID),
	}
}

// reviewThread maps a domain thread into its view model: the comments in posting
// order, the resolved and outdated flags, and the reply and resolve targets gated to
// what the viewer may do. The anchor side comes from the root comment, since the
// domain thread carries the line but not the side directly.
func (h *Handlers) reviewThread(ctx context.Context, repo *domain.Repo, pr *domain.PullRequest, t *domain.ReviewThread, vc viewerCtx) view.ReviewThreadVM {
	owner := ownerLogin(repo)
	// The thread is addressed by its root comment's public database id (t.ID), the
	// same id GetReviewComment reads in the reply and resolve handlers. t.RootPK is
	// the internal store primary key and never leaves the domain, so it must not
	// reach a URL.
	vm := view.ReviewThreadVM{
		ID:         t.ID,
		Path:       t.Path,
		Side:       threadSide(t),
		IsResolved: t.IsResolved,
		IsOutdated: t.IsOutdated,
		CSRFToken:  view.CSRFFrom(ctx),
	}
	if t.Line != nil {
		vm.Line = int(*t.Line)
	}
	for _, rc := range t.Comments {
		vm.Comments = append(vm.Comments, h.reviewComment(ctx, repo, pr.Number, rc))
	}
	if vc.pk != 0 {
		vm.CanReply = true
		vm.ReplyURL = route.PullReviewReply(owner, repo.Name, pr.Number, t.ID)
	}
	if canResolveThread(repo, vc) {
		vm.CanResolve = true
		vm.ResolveURL = route.PullReviewThreadResolve(owner, repo.Name, pr.Number, t.ID)
	}
	return vm
}

// threadSide reads the anchor side off the thread's first comment, defaulting to the
// head side (RIGHT) when a thread has no comment to read it from, which is where a
// comment on an added or context line anchors.
func threadSide(t *domain.ReviewThread) string {
	for _, rc := range t.Comments {
		if rc.Side != "" {
			return rc.Side
		}
	}
	return "RIGHT"
}

// canResolveThread reports whether the viewer may resolve or unresolve a thread:
// a viewer with write access, the same rule ReviewService.ResolveThread enforces.
// The service authorizes write (so the author of a pull request they cannot push
// to does not get the toggle), and the display gate matches it rather than showing
// an affordance the submit would reject. The service authorizes again on submit.
func canResolveThread(repo *domain.Repo, vc viewerCtx) bool {
	return canWrite(repo, vc.pk)
}

// prAuthored reports whether the viewer is the pull request's author, matched by
// login, the check the approve gate and the resolve gate both read.
func prAuthored(pr *domain.PullRequest, vc viewerCtx) bool {
	return pr.User != nil && vc.login != "" && pr.User.Login == vc.login
}

// canApprove reports whether the viewer may approve or request changes: any
// signed-in viewer who is not the pull request's own author. ReviewService.
// CreateReview authorizes read access (the page already rendered, so the viewer
// can see the repo) and forbids self-approval, so a reader who is not the author
// may submit a verdict, while the author sees the overlay with those options
// disabled and only Comment live.
func canApprove(pr *domain.PullRequest, vc viewerCtx) bool {
	return vc.pk != 0 && !prAuthored(pr, vc)
}

// reviewSummary maps a submitted review into its Conversation-timeline item: the
// reviewer, the derived state header, the optional rendered body, the count of inline
// comments it carried, and the permalink.
func (h *Handlers) reviewSummary(ctx context.Context, repo *domain.Repo, pr *domain.PullRequest, r *domain.Review) view.ReviewSummaryVM {
	owner := ownerLogin(repo)
	vm := view.ReviewSummaryVM{
		Author:       h.userChip(r.User),
		State:        view.DeriveReviewState(r.State).StateVM(),
		CommentCount: len(r.Comments),
		Anchor:       "pullrequestreview-" + strconv.FormatInt(r.ID, 10),
		URL:          route.PullReviewSummary(owner, repo.Name, pr.Number, r.ID),
	}
	if r.SubmittedAt != nil {
		vm.SubmittedAt = r.SubmittedAt.UTC().Format("Jan 2, 2006")
		vm.SubmittedISO = r.SubmittedAt.UTC().Format(time.RFC3339)
	}
	if body := r.Body; body != "" {
		vm.HasBody = true
		vm.Body = h.renderBody(ctx, repo, body)
	}
	return vm
}

// reviewSurface builds the page-level review context the Files tab carries: the
// inline-comment POST target, the head commit id every new thread pins to, the CSRF
// token, and the verdict overlay. The composer affordances are display gates; the
// service re-authorizes every submit.
func (h *Handlers) reviewSurface(ctx context.Context, repo *domain.Repo, pr *domain.PullRequest, vc viewerCtx) view.ReviewSurfaceVM {
	owner := ownerLogin(repo)
	return view.ReviewSurfaceVM{
		CanComment:    canComment(vc.pk),
		CommentAction: route.PullReviewComments(owner, repo.Name, pr.Number),
		CommitID:      pr.Head.SHA,
		CSRFToken:     view.CSRFFrom(ctx),
		Overlay: view.ReviewOverlayVM{
			Action:     route.PullReviews(owner, repo.Name, pr.Number),
			CanApprove: canApprove(pr, vc),
			CanComment: canComment(vc.pk),
			CSRFToken:  view.CSRFFrom(ctx),
		},
	}
}

// attachThreads hangs each live review thread off the diff row its persisted
// (path, line, side) anchor matches, and collects a thread whose anchor no longer
// maps onto the current diff into its file's outdated group. A thread on a file that
// is not in the rendered diff (an anchor on an unchanged file) has nowhere to land;
// those are counted and logged rather than dropped in silence. It mutates the files
// in place and returns them for chaining.
func (h *Handlers) attachThreads(files []view.DiffFileVM, threads []view.ReviewThreadVM, owner, name string, number int64) []view.DiffFileVM {
	byPath := make(map[string]int, len(files))
	for i := range files {
		byPath[files[i].Path] = i
	}
	orphans := 0
	for _, t := range threads {
		fi, ok := byPath[t.Path]
		if !ok {
			orphans++
			continue
		}
		f := &files[fi]
		if t.IsOutdated || !placeThread(f, t) {
			f.OutdatedThreads = append(f.OutdatedThreads, t)
		}
	}
	if orphans > 0 {
		h.logOrphanThreads(owner, name, number, orphans)
	}
	return files
}

// placeThread finds the row in a file whose anchor matches the thread's (side, line)
// and appends the thread to it, reporting whether a row was found. A thread with no
// matching row (the anchored line is outside the rendered hunks) falls through to the
// outdated group.
func placeThread(f *view.DiffFileVM, t view.ReviewThreadVM) bool {
	for i := range f.Rows {
		r := &f.Rows[i]
		if r.AnchorSide == t.Side && r.AnchorLine == t.Line && t.Line != 0 {
			r.Threads = append(r.Threads, t)
			return true
		}
	}
	return false
}

// logOrphanThreads records review threads anchored to a file outside the rendered
// diff, so they read as accounted-for rather than silently missing. A nil logger (a
// test wiring) skips the line.
func (h *Handlers) logOrphanThreads(owner, name string, number int64, count int) {
	if h.log == nil {
		return
	}
	h.log.Warn("pulls: review threads anchored outside the diff",
		slog.String("owner", owner),
		slog.String("repo", name),
		slog.Int64("number", number),
		slog.Int("count", count),
	)
}

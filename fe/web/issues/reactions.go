package issues

import (
	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
)

// reactions.go toggles a reaction on an issue or a comment. The service has no
// single toggle call, only create and delete, and the rollup the read path
// carries has no per-viewer signal, so the handler resolves the toggle here: list
// the subject's reactions, and if the viewer already left this content delete it,
// otherwise create it. The redirect lands back on the issue (or the comment
// anchor), so the no-JS flow returns to the reacted element. See
// implementation/08 section 7.

// ToggleIssueReaction adds or removes the viewer's reaction of the submitted
// content on the issue body.
func (h *Handlers) ToggleIssueReaction(c *mizu.Ctx) error {
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

	content := formString(c, "content")
	if !isReactionContent(content) {
		return h.notFound(c)
	}

	existing, err := h.issues.ListIssueReactions(ctx, vc.pk, owner, repo.Name, number)
	if err != nil {
		return h.writeError(c, err)
	}
	if id, found := viewerReaction(existing, vc, content); found {
		if err := h.issues.DeleteIssueReaction(ctx, vc.pk, owner, repo.Name, number, id); err != nil {
			return h.writeError(c, err)
		}
	} else if _, err := h.issues.CreateIssueReaction(ctx, vc.pk, owner, repo.Name, number, content); err != nil {
		return h.writeError(c, err)
	}
	return redirect(c, route.Issue(owner, repo.Name, number))
}

// ToggleCommentReaction adds or removes the viewer's reaction of the submitted
// content on a comment, returning to the comment's anchor on the issue page.
func (h *Handlers) ToggleCommentReaction(c *mizu.Ctx) error {
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
	commentID, ok := numberParam(c.Param("comment"))
	if !ok {
		return h.notFound(c)
	}
	vc := h.viewer(c)

	content := formString(c, "content")
	if !isReactionContent(content) {
		return h.notFound(c)
	}

	existing, err := h.issues.ListCommentReactions(ctx, vc.pk, owner, repo.Name, commentID)
	if err != nil {
		return h.writeError(c, err)
	}
	if id, found := viewerReaction(existing, vc, content); found {
		if err := h.issues.DeleteCommentReaction(ctx, vc.pk, owner, repo.Name, commentID, id); err != nil {
			return h.writeError(c, err)
		}
	} else if _, err := h.issues.CreateCommentReaction(ctx, vc.pk, owner, repo.Name, commentID, content); err != nil {
		return h.writeError(c, err)
	}
	return redirect(c, route.IssueComment(owner, repo.Name, number, commentID))
}

// viewerReaction finds the viewer's reaction of a given content in a list,
// returning its id. The match is by viewer login, the identity the read path
// carries on each reaction's user.
func viewerReaction(list []*domain.Reaction, vc viewerCtx, content string) (int64, bool) {
	if vc.pk == 0 {
		return 0, false
	}
	for _, r := range list {
		if r.Content != content || r.User == nil {
			continue
		}
		if r.User.Login == vc.login {
			return r.ID, true
		}
	}
	return 0, false
}

// isReactionContent reports whether s is one of the eight canonical reaction
// contents, the guard that keeps a stray form value from reaching the service.
func isReactionContent(s string) bool {
	for _, c := range reactionOrder {
		if c.key == s {
			return true
		}
	}
	return false
}

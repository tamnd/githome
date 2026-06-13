package presenter

import (
	"net/url"
	"strconv"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/gqlmodel"
)

// GQLIssue renders a domain issue into the GraphQL Issue shape for owner/repo.
// It fills labels, assignees, and milestone eagerly from the pre-loaded domain
// data; comments carry the total only and are paged by the resolver on demand.
// format selects the node-ID encoding.
func (b *URLBuilder) GQLIssue(owner, repo string, iss *domain.Issue, format nodeid.Format) *gqlmodel.Issue {
	num := strconv.FormatInt(iss.Number, 10)
	out := &gqlmodel.Issue{
		ID:               nodeid.Encode(nodeid.KindIssue, iss.ID, format),
		Number:           int32(iss.Number),
		Title:            iss.Title,
		Body:             deref(iss.Body),
		State:            issueState(iss.State),
		URL:              gqlmodel.URI(b.RepoHTML(owner, repo) + "/issues/" + num),
		Locked:           iss.Locked,
		Closed:           iss.State == "closed",
		Author:           b.gqlActor(iss.User, format),
		CreatedAt:        gqlmodel.NewDateTime(iss.CreatedAt),
		UpdatedAt:        gqlmodel.NewDateTime(iss.UpdatedAt),
		Labels:           b.gqlLabelConnection(owner, repo, iss.Labels, format),
		Assignees:        b.GQLUserConnection(iss.Assignees, format),
		Milestone:        b.GQLMilestone(owner, repo, iss.Milestone, format),
		Comments:         &gqlmodel.IssueCommentConnection{TotalCount: int32(iss.CommentsCount), PageInfo: &gqlmodel.PageInfo{}},
		ReactionGroups:   gqlReactionGroups(iss.Reactions),
		RepoOwner:        owner,
		RepoName:         repo,
		PK:               iss.PK,
		UserPK:           iss.UserPK,
		DatabaseID:       iss.ID,
		ActiveLockReason: iss.ActiveLockReason,
	}
	if sr := issueStateReason(iss.StateReason); sr != nil {
		out.StateReason = sr
	}
	if iss.ClosedAt != nil {
		closed := gqlmodel.NewDateTime(*iss.ClosedAt)
		out.ClosedAt = &closed
	}
	return out
}

// GQLIssueComment renders a domain comment into the GraphQL IssueComment shape.
// The author association is the owner heuristic Githome applies everywhere: the
// repository owner is OWNER, everyone else is NONE until collaborator roles are
// modeled. includesCreatedEdit falls out of the timestamps.
func (b *URLBuilder) GQLIssueComment(owner, repo string, cm *domain.Comment, format nodeid.Format) *gqlmodel.IssueComment {
	num := strconv.FormatInt(cm.IssueNumber, 10)
	id := strconv.FormatInt(cm.ID, 10)
	out := &gqlmodel.IssueComment{
		ID:                  nodeid.Encode(nodeid.KindIssueComment, cm.ID, format),
		Body:                cm.Body,
		URL:                 gqlmodel.URI(b.RepoHTML(owner, repo) + "/issues/" + num + "#issuecomment-" + id),
		Author:              b.gqlActor(cm.User, format),
		AuthorAssociation:   GQLAuthorAssociation(owner, cm.User),
		IncludesCreatedEdit: cm.UpdatedAt.After(cm.CreatedAt),
		ReactionGroups:      gqlReactionGroups(cm.Reactions),
		CreatedAt:           gqlmodel.NewDateTime(cm.CreatedAt),
		UpdatedAt:           gqlmodel.NewDateTime(cm.UpdatedAt),
	}
	if cm.User != nil {
		out.AuthorPK = cm.User.ID
	}
	return out
}

// GQLAuthorAssociation is the GraphQL spelling of the REST authorAssociation
// heuristic: the repository owner is OWNER; anyone else is NONE until
// collaborator and member roles are modeled. A ghost author is NONE.
func GQLAuthorAssociation(owner string, u *domain.User) gqlmodel.CommentAuthorAssociation {
	if u == nil {
		return gqlmodel.CommentAuthorAssociationNone
	}
	return gqlmodel.CommentAuthorAssociation(authorAssociation(u.Login, owner))
}

// gqlReactionGroups maps a domain reaction rollup onto the GraphQL reaction
// groups, in GitHub's content order. The slice is non-nil even when empty.
func gqlReactionGroups(r domain.ReactionRollup) []gqlmodel.ReactionGroup {
	out := []gqlmodel.ReactionGroup{}
	for _, m := range []struct {
		key     string
		content gqlmodel.ReactionContent
	}{
		{"+1", gqlmodel.ReactionContentThumbsUp},
		{"-1", gqlmodel.ReactionContentThumbsDown},
		{"laugh", gqlmodel.ReactionContentLaugh},
		{"hooray", gqlmodel.ReactionContentHooray},
		{"confused", gqlmodel.ReactionContentConfused},
		{"heart", gqlmodel.ReactionContentHeart},
		{"rocket", gqlmodel.ReactionContentRocket},
		{"eyes", gqlmodel.ReactionContentEyes},
	} {
		if n := r.Counts[m.key]; n > 0 {
			out = append(out, gqlmodel.ReactionGroup{
				Content: m.content,
				Users:   gqlmodel.ReactingUserConnection{TotalCount: int32(n)},
			})
		}
	}
	return out
}

// GQLLabel renders a domain label into the GraphQL Label shape. owner and repo
// name the repository the label belongs to, used to build the label's HTML URL
// (the label-filtered issue list, matching GitHub). Githome does not track a
// separate label-update instant, so updatedAt mirrors createdAt.
func (b *URLBuilder) GQLLabel(owner, repo string, l *domain.Label, format nodeid.Format) *gqlmodel.Label {
	created := gqlmodel.NewDateTime(l.CreatedAt)
	return &gqlmodel.Label{
		ID:          nodeid.Encode(nodeid.KindLabel, l.ID, format),
		Name:        l.Name,
		Color:       l.Color,
		Description: l.Description,
		IsDefault:   l.Default,
		URL:         gqlmodel.URI(b.RepoHTML(owner, repo) + "/labels/" + url.PathEscape(l.Name)),
		CreatedAt:   created,
		UpdatedAt:   created,
	}
}

// gqlLabelConnection builds the label connection embedded on an issue. It fills
// both edges and nodes so a client selecting either shape resolves; the cursor
// is the label's node ID, the same opaque token the keyset pager would emit.
func (b *URLBuilder) gqlLabelConnection(owner, repo string, ls []*domain.Label, format nodeid.Format) *gqlmodel.LabelConnection {
	nodes := make([]*gqlmodel.Label, 0, len(ls))
	edges := make([]*gqlmodel.LabelEdge, 0, len(ls))
	for _, l := range ls {
		n := b.GQLLabel(owner, repo, l, format)
		nodes = append(nodes, n)
		edges = append(edges, &gqlmodel.LabelEdge{Cursor: n.ID, Node: n})
	}
	return &gqlmodel.LabelConnection{Edges: edges, Nodes: nodes, PageInfo: &gqlmodel.PageInfo{}, TotalCount: int32(len(nodes))}
}

// gqlActor renders an issue or comment author into the GraphQL Actor shape,
// returning nil for a ghost (deleted) author so the field marshals to null.
// The concrete value is always a *gqlmodel.User so inline fragments dispatch.
func (b *URLBuilder) gqlActor(u *domain.User, format nodeid.Format) gqlmodel.Actor {
	if u == nil {
		return nil
	}
	return b.GQLUser(u, format)
}

// issueState maps the domain open/closed string to the GraphQL enum.
func issueState(s string) gqlmodel.IssueState {
	if s == "closed" {
		return gqlmodel.IssueStateClosed
	}
	return gqlmodel.IssueStateOpen
}

// issueStateReason maps the optional domain state reason to the GraphQL enum,
// returning nil when the issue carries no reason.
func issueStateReason(s *string) *gqlmodel.IssueStateReason {
	if s == nil {
		return nil
	}
	var r gqlmodel.IssueStateReason
	switch *s {
	case "completed":
		r = gqlmodel.IssueStateReasonCompleted
	case "not_planned":
		r = gqlmodel.IssueStateReasonNotPlanned
	case "reopened":
		r = gqlmodel.IssueStateReasonReopened
	default:
		return nil
	}
	return &r
}

// deref returns the string an optional points at, or empty when it is nil.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

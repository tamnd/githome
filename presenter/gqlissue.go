package presenter

import (
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
		ID:        nodeid.Encode(nodeid.KindIssue, iss.ID, format),
		Number:    int32(iss.Number),
		Title:     iss.Title,
		Body:      deref(iss.Body),
		State:     issueState(iss.State),
		URL:       gqlmodel.URI(b.RepoHTML(owner, repo) + "/issues/" + num),
		Locked:    iss.Locked,
		Closed:    iss.State == "closed",
		Author:    b.gqlActor(iss.User, format),
		CreatedAt: gqlmodel.NewDateTime(iss.CreatedAt),
		UpdatedAt: gqlmodel.NewDateTime(iss.UpdatedAt),
		Labels:    b.gqlLabelConnection(owner, repo, iss.Labels, format),
		Assignees: b.GQLUserConnection(iss.Assignees, format),
		Milestone:      b.GQLMilestone(owner, repo, iss.Milestone, format),
		Comments:       &gqlmodel.IssueCommentConnection{TotalCount: int32(iss.CommentsCount)},
		ReactionGroups: []gqlmodel.ReactionGroup{}, // Githome does not store reactions
		RepoOwner:      owner,
		RepoName:  repo,
		PK:        iss.PK,
		UserPK:    iss.UserPK,
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
func (b *URLBuilder) GQLIssueComment(owner, repo string, cm *domain.Comment, format nodeid.Format) *gqlmodel.IssueComment {
	num := strconv.FormatInt(cm.IssueNumber, 10)
	id := strconv.FormatInt(cm.ID, 10)
	return &gqlmodel.IssueComment{
		ID:        nodeid.Encode(nodeid.KindIssueComment, cm.ID, format),
		Body:      cm.Body,
		URL:       gqlmodel.URI(b.RepoHTML(owner, repo) + "/issues/" + num + "#issuecomment-" + id),
		Author:    b.gqlActor(cm.User, format),
		CreatedAt: gqlmodel.NewDateTime(cm.CreatedAt),
		UpdatedAt: gqlmodel.NewDateTime(cm.UpdatedAt),
	}
}

// GQLLabel renders a domain label into the GraphQL Label shape.
func (b *URLBuilder) GQLLabel(l *domain.Label, format nodeid.Format) *gqlmodel.Label {
	return &gqlmodel.Label{
		ID:          nodeid.Encode(nodeid.KindLabel, l.ID, format),
		Name:        l.Name,
		Color:       l.Color,
		Description: l.Description,
	}
}

// gqlLabelConnection builds the label connection embedded on an issue.
func (b *URLBuilder) gqlLabelConnection(_, _ string, ls []*domain.Label, format nodeid.Format) *gqlmodel.LabelConnection {
	nodes := make([]*gqlmodel.Label, 0, len(ls))
	for _, l := range ls {
		nodes = append(nodes, b.GQLLabel(l, format))
	}
	return &gqlmodel.LabelConnection{Nodes: nodes, TotalCount: int32(len(nodes))}
}

// gqlActor renders an issue or comment author into the GraphQL Actor shape,
// returning nil for a ghost (deleted) author so the field marshals to null.
func (b *URLBuilder) gqlActor(u *domain.User, _ nodeid.Format) *gqlmodel.Actor {
	if u == nil {
		return nil
	}
	return &gqlmodel.Actor{
		Login:     u.Login,
		URL:       gqlmodel.URI(b.UserHTML(u.Login)),
		AvatarURL: gqlmodel.URI(b.HTML("avatars", "u", strconv.FormatInt(u.ID, 10))),
	}
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

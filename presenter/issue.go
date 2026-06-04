package presenter

import (
	"net/url"
	"strconv"
	"time"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/restmodel"
)

// Issue renders the full issue object for owner/repo. It is pure: the same
// domain issue, URL config, and node-id format always produce the same bytes.
// The URLs all hang off the repository's API base so the two API surfaces agree
// about an issue's links.
func (b *URLBuilder) Issue(owner, repo string, iss *domain.Issue, format nodeid.Format) restmodel.Issue {
	base := b.RepoAPI(owner, repo)
	self := base + "/issues/" + strconv.FormatInt(iss.Number, 10)
	html := b.RepoHTML(owner, repo) + "/issues/" + strconv.FormatInt(iss.Number, 10)

	out := restmodel.Issue{
		ID:                iss.ID,
		NodeID:            nodeid.Encode(nodeid.KindIssue, iss.ID, format),
		URL:               self,
		RepositoryURL:     base,
		LabelsURL:         self + "/labels{/name}",
		CommentsURL:       self + "/comments",
		EventsURL:         self + "/events",
		HTMLURL:           html,
		Number:            iss.Number,
		State:             iss.State,
		StateReason:       iss.StateReason,
		Title:             iss.Title,
		Body:              iss.Body,
		User:              b.SimpleUser(iss.User, format),
		Labels:            b.labels(owner, repo, iss.Labels, format),
		Assignees:         b.assignees(iss.Assignees, format),
		Milestone:         b.milestonePtr(owner, repo, iss.Milestone, format),
		Locked:            iss.Locked,
		Comments:          iss.CommentsCount,
		ClosedAt:          timePtr(iss.ClosedAt),
		CreatedAt:         restmodel.NewTime(iss.CreatedAt),
		UpdatedAt:         restmodel.NewTime(iss.UpdatedAt),
		AuthorAssociation: authorAssociation(iss.User.Login, owner),
		Reactions:         reactionRollup(self+"/reactions", iss.Reactions),
		TimelineURL:       self + "/timeline",
	}
	// GitHub exposes the first assignee on the legacy single-value assignee field.
	if len(out.Assignees) > 0 {
		first := out.Assignees[0]
		out.Assignee = &first
	}
	if iss.ClosedBy != nil {
		closer := b.SimpleUser(iss.ClosedBy, format)
		out.ClosedBy = &closer
	}
	return out
}

// Label renders a repository label for owner/repo. The label URL escapes the
// name so labels with spaces or slashes address correctly.
func (b *URLBuilder) Label(owner, repo string, l *domain.Label, format nodeid.Format) restmodel.Label {
	return restmodel.Label{
		ID:          l.ID,
		NodeID:      nodeid.Encode(nodeid.KindLabel, l.ID, format),
		URL:         b.RepoAPI(owner, repo) + "/labels/" + url.PathEscape(l.Name),
		Name:        l.Name,
		Color:       l.Color,
		Default:     l.Default,
		Description: l.Description,
	}
}

// Milestone renders a milestone for owner/repo, including the open and closed
// issue counts the domain computed on read.
func (b *URLBuilder) Milestone(owner, repo string, m *domain.Milestone, format nodeid.Format) restmodel.Milestone {
	base := b.RepoAPI(owner, repo)
	self := base + "/milestones/" + strconv.FormatInt(m.Number, 10)
	out := restmodel.Milestone{
		URL:          self,
		HTMLURL:      b.RepoHTML(owner, repo) + "/milestone/" + strconv.FormatInt(m.Number, 10),
		LabelsURL:    self + "/labels",
		ID:           m.ID,
		NodeID:       nodeid.Encode(nodeid.KindMilestone, m.ID, format),
		Number:       m.Number,
		State:        m.State,
		Title:        m.Title,
		Description:  m.Description,
		OpenIssues:   m.OpenIssues,
		ClosedIssues: m.ClosedIssues,
		CreatedAt:    restmodel.NewTime(m.CreatedAt),
		UpdatedAt:    restmodel.NewTime(m.UpdatedAt),
		ClosedAt:     timePtr(m.ClosedAt),
		DueOn:        timePtr(m.DueOn),
	}
	if m.Creator != nil {
		creator := b.SimpleUser(m.Creator, format)
		out.Creator = &creator
	}
	return out
}

// IssueComment renders an issue comment for owner/repo. The comment carries the
// number of the issue it belongs to so its html_url and issue_url resolve.
func (b *URLBuilder) IssueComment(owner, repo string, issueNumber int64, cm *domain.Comment, format nodeid.Format) restmodel.IssueComment {
	base := b.RepoAPI(owner, repo)
	num := strconv.FormatInt(issueNumber, 10)
	id := strconv.FormatInt(cm.ID, 10)
	self := base + "/issues/comments/" + id
	return restmodel.IssueComment{
		ID:                cm.ID,
		NodeID:            nodeid.Encode(nodeid.KindIssueComment, cm.ID, format),
		URL:               self,
		HTMLURL:           b.RepoHTML(owner, repo) + "/issues/" + num + "#issuecomment-" + id,
		Body:              cm.Body,
		User:              b.SimpleUser(cm.User, format),
		CreatedAt:         restmodel.NewTime(cm.CreatedAt),
		UpdatedAt:         restmodel.NewTime(cm.UpdatedAt),
		IssueURL:          base + "/issues/" + num,
		AuthorAssociation: authorAssociation(cm.User.Login, owner),
		Reactions:         reactionRollup(self+"/reactions", cm.Reactions),
	}
}

// Reaction renders a single reaction.
func (b *URLBuilder) Reaction(r *domain.Reaction, format nodeid.Format) restmodel.Reaction {
	return restmodel.Reaction{
		ID:        r.ID,
		NodeID:    nodeid.Encode(nodeid.KindReaction, r.ID, format),
		User:      b.SimpleUser(r.User, format),
		Content:   r.Content,
		CreatedAt: restmodel.NewTime(r.CreatedAt),
	}
}

// labels renders an issue's attached labels.
func (b *URLBuilder) labels(owner, repo string, ls []*domain.Label, format nodeid.Format) []restmodel.Label {
	out := make([]restmodel.Label, 0, len(ls))
	for _, l := range ls {
		out = append(out, b.Label(owner, repo, l, format))
	}
	return out
}

// assignees renders an issue's assignees.
func (b *URLBuilder) assignees(us []*domain.User, format nodeid.Format) []restmodel.SimpleUser {
	out := make([]restmodel.SimpleUser, 0, len(us))
	for _, u := range us {
		out = append(out, b.SimpleUser(u, format))
	}
	return out
}

// milestonePtr renders the optional milestone embedded on an issue.
func (b *URLBuilder) milestonePtr(owner, repo string, m *domain.Milestone, format nodeid.Format) *restmodel.Milestone {
	if m == nil {
		return nil
	}
	out := b.Milestone(owner, repo, m, format)
	return &out
}

// reactionRollup renders the per-content reaction summary, filling the literal
// +1/-1 keys and the named contents from the domain rollup's count map.
func reactionRollup(rollupURL string, r domain.ReactionRollup) restmodel.ReactionRollup {
	return restmodel.ReactionRollup{
		URL:        rollupURL,
		TotalCount: r.TotalCount,
		PlusOne:    r.Counts["+1"],
		MinusOne:   r.Counts["-1"],
		Laugh:      r.Counts["laugh"],
		Hooray:     r.Counts["hooray"],
		Confused:   r.Counts["confused"],
		Heart:      r.Counts["heart"],
		Rocket:     r.Counts["rocket"],
		Eyes:       r.Counts["eyes"],
	}
}

// authorAssociation reports the actor's relationship to the repository. Githome
// grants write access only to the repository owner today, so an author who owns
// the repo is OWNER and everyone else is NONE; the richer MEMBER, COLLABORATOR,
// and CONTRIBUTOR values arrive with org and collaborator support.
func authorAssociation(login, owner string) string {
	if login == owner {
		return "OWNER"
	}
	return "NONE"
}

// timePtr wraps an optional timestamp for wire rendering, returning nil so an
// absent time marshals to JSON null.
func timePtr(t *time.Time) *restmodel.Time {
	if t == nil {
		return nil
	}
	out := restmodel.NewTime(*t)
	return &out
}

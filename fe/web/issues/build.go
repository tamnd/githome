package issues

import (
	"context"
	"html/template"
	"strconv"
	"strings"
	"time"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/markup"
)

// build.go maps domain issue data into the fe/view models. It keeps fe/view a
// pure data package by concentrating the mapping here, next to the handlers, and
// it precomputes every URL through fe/route so a template never builds a link.
// See implementation/08 sections 3 to 8.

// ownerLogin returns the repo owner's login, tolerating a repo assembled without
// its owner.
func ownerLogin(r *domain.Repo) string {
	if r.Owner != nil {
		return r.Owner.Login
	}
	return ""
}

// header builds the repo context bar with the issues tab current, the same
// partial every repo page renders.
func (h *Handlers) header(r *domain.Repo) view.RepoHeaderVM {
	owner := ownerLogin(r)
	hdr := view.RepoHeaderVM{
		Owner:     owner,
		Name:      r.Name,
		OwnerURL:  "/" + owner,
		URL:       route.Repo(owner, r.Name),
		Private:   r.Private,
		Fork:      r.Fork,
		ActiveTab: "issues",
	}
	if r.Description != nil {
		hdr.Description = *r.Description
	}
	return hdr
}

// nav builds the repo underline-nav link set, the same one the code views show.
// The default branch drives the commits link; the issues link is the bare index.
func (h *Handlers) nav(r *domain.Repo) view.TreeNav {
	owner := ownerLogin(r)
	return view.TreeNav{
		CodeURL:     route.Repo(owner, r.Name),
		IssuesURL:   route.Issues(owner, r.Name, ""),
		PullsURL:    route.Pulls(owner, r.Name, ""),
		CommitsURL:  route.Commits(owner, r.Name, r.DefaultBranch, ""),
		BranchesURL: route.Branches(owner, r.Name),
		TagsURL:     route.Tags(owner, r.Name),
	}
}

// repoRef is the small identity every issue view carries.
func repoRef(r *domain.Repo) view.RepoRef {
	owner := ownerLogin(r)
	return view.RepoRef{Owner: owner, Name: r.Name, URL: route.Repo(owner, r.Name)}
}

// stateBadge maps an issue's state and reason into the badge view model: a label,
// an octicon, and a CSS modifier the stylesheet colors. A closed issue split into
// completed (purple) and not-planned (gray) follows github.com.
func stateBadge(iss *domain.Issue) view.IssueStateVM {
	reason := ""
	if iss.StateReason != nil {
		reason = *iss.StateReason
	}
	if iss.State == "closed" {
		mod := "closed"
		icon := "issue-closed"
		if reason == "not_planned" {
			mod = "not-planned"
			icon = "skip"
		}
		return view.IssueStateVM{State: "closed", Reason: reason, Label: "Closed", Icon: icon, Modifier: mod}
	}
	return view.IssueStateVM{State: "open", Reason: reason, Label: "Open", Icon: "issue-opened", Modifier: "open"}
}

// userChip maps a domain user into the small chip the timeline and rows show. A
// nil user (a ghost author whose account is gone) yields a neutral "ghost" chip
// with no profile link rather than a broken one.
func (h *Handlers) userChip(u *domain.User) view.UserChipVM {
	if u == nil {
		return view.UserChipVM{Login: "ghost"}
	}
	return view.UserChipVM{
		Login:     u.Login,
		AvatarURL: h.urls.HTML("avatars", "u", strconv.FormatInt(u.ID, 10)),
		URL:       "/" + u.Login,
	}
}

// userChips maps a slice of users.
func (h *Handlers) userChips(us []*domain.User) []view.UserChipVM {
	out := make([]view.UserChipVM, 0, len(us))
	for _, u := range us {
		out = append(out, h.userChip(u))
	}
	return out
}

// labelChip maps a domain label into its chip, computing the contrasting text
// color from the background and the index URL that filters to the label.
func labelChip(owner, name string, l *domain.Label) view.LabelVM {
	desc := ""
	if l.Description != nil {
		desc = *l.Description
	}
	color := strings.TrimPrefix(l.Color, "#")
	return view.LabelVM{
		Name:        l.Name,
		Color:       color,
		TextColor:   contrastText(color),
		Description: desc,
		URL:         route.IssuesQuery(owner, name, "is:issue is:open label:"+quoteLabel(l.Name)),
	}
}

// labelChips maps a slice of labels.
func labelChips(owner, name string, ls []*domain.Label) []view.LabelVM {
	out := make([]view.LabelVM, 0, len(ls))
	for _, l := range ls {
		out = append(out, labelChip(owner, name, l))
	}
	return out
}

// quoteLabel wraps a multi-word label name in double quotes so the composed q
// value keeps it as one qualifier.
func quoteLabel(name string) string {
	if strings.ContainsAny(name, " \t") {
		return `"` + name + `"`
	}
	return name
}

// milestoneChip maps a milestone into its chip with the filter URL, or nil when
// the issue has none.
func milestoneChip(owner, name string, m *domain.Milestone) *view.MilestoneVM {
	if m == nil {
		return nil
	}
	return &view.MilestoneVM{
		Title: m.Title,
		URL:   route.IssuesQuery(owner, name, "is:issue is:open milestone:"+quoteLabel(m.Title)),
	}
}

// contrastText picks black or white text for a label background by its luminance,
// the same rule github.com uses so a light label reads in dark text and a dark
// one in white. A malformed color falls back to white on the neutral default.
func contrastText(hex string) string {
	if len(hex) != 6 {
		return "ffffff"
	}
	r, err1 := strconv.ParseInt(hex[0:2], 16, 0)
	g, err2 := strconv.ParseInt(hex[2:4], 16, 0)
	b, err3 := strconv.ParseInt(hex[4:6], 16, 0)
	if err1 != nil || err2 != nil || err3 != nil {
		return "ffffff"
	}
	// Perceived luminance, the standard 0.299/0.587/0.114 weighting.
	lum := (299*r + 587*g + 114*b) / 1000
	if lum > 140 {
		return "000000"
	}
	return "ffffff"
}

// reactionContent is one of the eight reaction kinds in canonical order, paired
// with the emoji glyph the rollup bar shows.
type reactionContent struct {
	key   string
	emoji string
}

// reactionOrder is the canonical reaction set and order github.com shows.
var reactionOrder = []reactionContent{
	{"+1", "\U0001F44D"},
	{"-1", "\U0001F44E"},
	{"laugh", "\U0001F604"},
	{"hooray", "\U0001F389"},
	{"confused", "\U0001F615"},
	{"heart", "❤️"},
	{"rocket", "\U0001F680"},
	{"eyes", "\U0001F440"},
}

// reactionsRollup builds the reaction bar for an issue body or a comment. It
// renders the canonical eight in order with their counts; toggleURL is the POST
// target the per-content form submits to with the content as a field. Whether the
// viewer already reacted is not in the rollup the domain returns, so Reacted stays
// false and the toggle handler resolves create-or-delete on submit (recorded in
// the spec as-built note).
func reactionsRollup(subject, toggleURL string, roll domain.ReactionRollup, canReact bool) view.ReactionsVM {
	vm := view.ReactionsVM{Subject: subject, Total: roll.TotalCount, CanReact: canReact}
	for _, rc := range reactionOrder {
		count := 0
		if roll.Counts != nil {
			count = roll.Counts[rc.key]
		}
		vm.Items = append(vm.Items, view.ReactionVM{
			Content: rc.key,
			Emoji:   rc.emoji,
			Count:   count,
			URL:     toggleURL,
		})
	}
	return vm
}

// viewerCtx carries the two viewer facts the build functions gate on: the PK for
// write access and the login for the author-owns-comment check. It is assembled
// once per request from the context and the chrome.
type viewerCtx struct {
	pk    int64
	login string
}

// comment builds a timeline comment from a domain comment.
func (h *Handlers) comment(ctx context.Context, repo *domain.Repo, number int64, cm *domain.Comment, vc viewerCtx) view.CommentVM {
	owner := ownerLogin(repo)
	vm := view.CommentVM{
		ID:         cm.ID,
		Author:     h.userChip(cm.User),
		Body:       h.renderBody(ctx, repo, cm.Body),
		BodySource: cm.Body,
		CreatedAt:  cm.CreatedAt.UTC().Format("Jan 2, 2006"),
		CreatedISO: cm.CreatedAt.UTC().Format(time.RFC3339),
		Edited:     cm.UpdatedAt.After(cm.CreatedAt),
		Anchor:     "issuecomment-" + strconv.FormatInt(cm.ID, 10),
		URL:        route.IssueComment(owner, repo.Name, number, cm.ID),
		Reactions:  reactionsRollup("comment", route.CommentReactions(owner, repo.Name, number, cm.ID), cm.Reactions, vc.pk != 0),
	}
	if canEditComment(repo, cm, vc) {
		vm.CanEdit = true
		vm.EditURL = route.CommentEdit(owner, repo.Name, number, cm.ID)
		vm.DeleteURL = route.CommentDelete(owner, repo.Name, number, cm.ID)
	}
	return vm
}

// canEditComment reports whether the viewer may edit or delete the comment: its
// author (matched by login, the identity the view carries) or a viewer with
// write access, the same author-or-writer rule the comment service enforces. It
// gates the display affordance only; the service re-authorizes on submit.
func canEditComment(repo *domain.Repo, cm *domain.Comment, vc viewerCtx) bool {
	if vc.pk == 0 {
		return false
	}
	if cm.User != nil && vc.login != "" && cm.User.Login == vc.login {
		return true
	}
	return canWrite(repo, vc.pk)
}

// renderBody renders a comment body to trusted HTML through the markup package,
// or returns the empty HTML when markup is unconfigured so the template falls
// back to the escaped source.
func (h *Handlers) renderBody(ctx context.Context, repo *domain.Repo, src string) template.HTML {
	if h.markup == nil || strings.TrimSpace(src) == "" {
		return ""
	}
	return h.markup.RenderComment(ctx, &markup.RepoRef{Owner: ownerLogin(repo), Name: repo.Name, ID: repo.ID}, src)
}

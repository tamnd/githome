package search

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// helpers.go holds the small shared mappers the search build reuses: the repo
// identity and header bar an in-repo search wears, the user chip and state badge
// the issue and pull rows carry, the type-rail labels and icons, and the page
// title. They mirror the equivalents in the issues and pulls handlers so the
// surfaces render the same chrome.

// ownerLogin returns the repo owner's login, tolerating a repo assembled without
// its owner.
func ownerLogin(r *domain.Repo) string {
	if r != nil && r.Owner != nil {
		return r.Owner.Login
	}
	return ""
}

// repoRef is the small identity an in-repo search page carries.
func repoRef(r *domain.Repo) view.RepoRef {
	owner := ownerLogin(r)
	return view.RepoRef{Owner: owner, Name: r.Name, URL: route.Repo(owner, r.Name)}
}

// chrome builds the shell model through the view builder, so a search page wears
// the same header, theme, and viewer menu as any other page.
func (h *Handlers) chrome(c *mizu.Ctx, title string) view.Chrome {
	return h.view.Chrome(c, title)
}

// header builds the repo context bar for an in-repo search. No repo nav tab is
// the search page's own, so none is marked current; the bar links back to the
// repo's tabs.
func (h *Handlers) header(r *domain.Repo) view.RepoHeaderVM {
	owner := ownerLogin(r)
	hdr := view.RepoHeaderVM{
		Owner:      owner,
		Name:       r.Name,
		OwnerURL:   "/" + owner,
		URL:        route.Repo(owner, r.Name),
		Private:    r.Private,
		Fork:       r.Fork,
		OpenIssues: r.OpenIssuesCount,
	}
	if r.Description != nil {
		hdr.Description = *r.Description
	}
	return hdr
}

// nav builds the repo underline-nav link set, the same one the code and issue
// views show.
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

// userChip maps a domain user into the small chip the result rows show. A nil
// user (a ghost author whose account is gone) yields a neutral chip with no
// profile link rather than a broken one.
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

// issueStateBadge maps a matched issue or pull request into its state badge. A
// pull request splits open/closed into the pull-request icons (and a merged pull
// reads as closed with the merge glyph); an issue uses the issue icons. The merged
// state rides on the issue's state reason set by the pull-request domain.
func issueStateBadge(iss *domain.Issue, isPull bool) view.IssueStateVM {
	reason := ""
	if iss.StateReason != nil {
		reason = *iss.StateReason
	}
	if isPull {
		switch {
		case reason == "merged":
			return view.IssueStateVM{State: "closed", Reason: "merged", Label: "Merged", Icon: "git-merge", Modifier: "merged"}
		case iss.State == "closed":
			return view.IssueStateVM{State: "closed", Reason: reason, Label: "Closed", Icon: "git-pull-request-closed", Modifier: "closed"}
		default:
			return view.IssueStateVM{State: "open", Reason: reason, Label: "Open", Icon: "git-pull-request", Modifier: "open"}
		}
	}
	if iss.State == "closed" {
		if reason == "not_planned" {
			return view.IssueStateVM{State: "closed", Reason: reason, Label: "Closed", Icon: "skip", Modifier: "not-planned"}
		}
		return view.IssueStateVM{State: "closed", Reason: reason, Label: "Closed", Icon: "issue-closed", Modifier: "closed"}
	}
	return view.IssueStateVM{State: "open", Reason: reason, Label: "Open", Icon: "issue-opened", Modifier: "open"}
}

// typeLabel is the human label for a result-type tab.
func typeLabel(typ string) string {
	switch typ {
	case view.SearchCode:
		return "Code"
	case view.SearchRepos:
		return "Repositories"
	case view.SearchIssues:
		return "Issues"
	case view.SearchPulls:
		return "Pull requests"
	default:
		return typ
	}
}

// typeIcon is the octicon name for a result-type tab.
func typeIcon(typ string) string {
	switch typ {
	case view.SearchCode:
		return "code"
	case view.SearchRepos:
		return "repo"
	case view.SearchIssues:
		return "issue-opened"
	case view.SearchPulls:
		return "git-pull-request"
	default:
		return "search"
	}
}

// searchTitle is the page title: the query plus the repo name for an in-repo
// search, just the query globally, and a plain "Search" for the landing.
func searchTitle(q, repoName string) string {
	q = trimTitle(q)
	switch {
	case q == "":
		return "Search"
	case repoName != "":
		return q + " in " + repoName
	default:
		return "Search: " + q
	}
}

// trimTitle bounds a query used in a title so a pathological query does not blow
// up the title tag; the full query still drives the search.
func trimTitle(q string) string {
	const limit = 60
	if len(q) > limit {
		return q[:limit] + "…"
	}
	return q
}

// logIncomplete records that a code search stopped at its scan budget before
// scanning every file, so the partial-results note reads as accounted-for rather
// than a silent truncation. A nil logger (a test wiring) skips the line.
func (h *Handlers) logIncomplete(ctx context.Context, r req, served int) {
	if h.log == nil {
		return
	}
	scope := "global"
	if r.scope == view.ScopeRepo {
		scope = ownerLogin(r.repo) + "/" + r.repo.Name
	}
	h.log.WarnContext(ctx, "search: code walk stopped at the scan budget",
		slog.String("scope", scope),
		slog.String("query", r.q),
		slog.Int("served", served),
	)
}

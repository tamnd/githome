package compare

import (
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/git"
)

func ownerLogin(r *domain.Repo) string {
	if r.Owner != nil {
		return r.Owner.Login
	}
	return ""
}

func (h *Handlers) header(r *domain.Repo, activeTab string) view.RepoHeaderVM {
	owner := ownerLogin(r)
	hdr := view.RepoHeaderVM{
		Owner:     owner,
		Name:      r.Name,
		OwnerURL:  "/" + owner,
		URL:       route.Repo(owner, r.Name),
		Private:   r.Private,
		Fork:      r.Fork,
		ActiveTab: activeTab,
	}
	if r.Description != nil {
		hdr.Description = *r.Description
	}
	return hdr
}

func (h *Handlers) nav(r *domain.Repo) view.TreeNav {
	owner := ownerLogin(r)
	return view.TreeNav{
		CodeURL:     route.Repo(owner, r.Name),
		IssuesURL:   route.Issues(owner, r.Name, ""),
		PullsURL:    route.Pulls(owner, r.Name, ""),
		CommitsURL:  route.Commits(owner, r.Name, "", ""),
		BranchesURL: route.Branches(owner, r.Name),
		TagsURL:     route.Tags(owner, r.Name),
	}
}

func branchVM(r *domain.Repo, b git.Branch) view.CompareBranchVM {
	owner := ownerLogin(r)
	sha := b.Commit
	short := sha
	if len(short) > 7 {
		short = short[:7]
	}
	return view.CompareBranchVM{
		Name:     b.Name,
		SHA:      sha,
		ShortSHA: short,
		URL:      route.Tree(owner, r.Name, b.Name, ""),
	}
}

func commitTitle(msg string) string {
	for i := 0; i < len(msg); i++ {
		if msg[i] == '\n' {
			return msg[:i]
		}
	}
	return msg
}

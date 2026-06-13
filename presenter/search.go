package presenter

import (
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/restmodel"
	"github.com/tamnd/githome/search"
)

// SearchIssues renders the issue search envelope. Each hit carries the
// repository it belongs to so its URLs resolve, since a cross-repository search
// returns issues whose owner and name are not implied by the request path.
func (b *URLBuilder) SearchIssues(hits []domain.IssueHit, total int, incomplete bool, format nodeid.Format) restmodel.SearchIssues {
	items := make([]restmodel.IssueSearchItem, 0, len(hits))
	for _, h := range hits {
		owner, name := h.Repo.Owner.Login, h.Repo.Name
		items = append(items, restmodel.IssueSearchItem{
			Issue: b.Issue(owner, name, h.Issue, format),
			Score: search.Score(),
		})
	}
	return restmodel.SearchIssues{
		TotalCount:        total,
		IncompleteResults: incomplete,
		Items:             items,
	}
}

// SearchRepositories renders the repository search envelope.
func (b *URLBuilder) SearchRepositories(repos []*domain.Repo, total int, incomplete bool, format nodeid.Format) restmodel.SearchRepositories {
	items := make([]restmodel.RepoSearchItem, 0, len(repos))
	for _, r := range repos {
		items = append(items, restmodel.RepoSearchItem{
			// Search items omit the permissions block, like GitHub's search hits.
			Repository: b.Repository(r, format, nil),
			Score:      search.Score(),
		})
	}
	return restmodel.SearchRepositories{
		TotalCount:        total,
		IncompleteResults: incomplete,
		Items:             items,
	}
}

// SearchCode renders the code search envelope. The file's url addresses it
// through the contents API at the matched ref, git_url through the blob API,
// and html_url through the repository's default branch, the three links GitHub
// returns on a code hit.
func (b *URLBuilder) SearchCode(results []domain.CodeResult, total int, incomplete bool, format nodeid.Format) restmodel.SearchCode {
	items := make([]restmodel.CodeSearchItem, 0, len(results))
	for _, r := range results {
		owner, name := r.Repo.Owner.Login, r.Repo.Name
		base := b.RepoAPI(owner, name)
		items = append(items, restmodel.CodeSearchItem{
			Name:       r.Name,
			Path:       r.Path,
			SHA:        r.SHA,
			URL:        base + "/contents/" + r.Path + "?ref=" + r.Repo.DefaultBranch,
			GitURL:     base + "/git/blobs/" + r.SHA,
			HTMLURL:    b.RepoHTML(owner, name) + "/blob/" + r.Repo.DefaultBranch + "/" + r.Path,
			Repository: b.Repository(r.Repo, format, nil),
			Score:      search.Score(),
		})
	}
	return restmodel.SearchCode{
		TotalCount:        total,
		IncompleteResults: incomplete,
		Items:             items,
	}
}

// SearchUsers renders the user search envelope. Each hit is a SimpleUser, the
// shape GitHub returns for account search rather than the full profile.
func (b *URLBuilder) SearchUsers(users []*domain.User, total int, incomplete bool, format nodeid.Format) restmodel.SearchUsers {
	items := make([]restmodel.UserSearchItem, 0, len(users))
	for _, u := range users {
		items = append(items, restmodel.UserSearchItem{
			SimpleUser: b.SimpleUser(u, format),
			Score:      search.Score(),
		})
	}
	return restmodel.SearchUsers{
		TotalCount:        total,
		IncompleteResults: incomplete,
		Items:             items,
	}
}

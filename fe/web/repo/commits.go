package repo

import (
	"errors"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/git"
)

// commitsPerPage bounds one history page. F1 renders the first page; the
// load-more fragment that pages further is a later enhancement (implementation/07
// section 7).
const commitsPerPage = 30

// Commits renders the history view: GET /{owner}/{repo}/commits and
// /{owner}/{repo}/commits/{rest}. The tail is a ref and an optional path filter;
// unlike tree and blob, commits never auto-corrects, so the history of a deleted
// file still renders. The list is grouped by calendar date. See implementation/07
// section 7.
func (h *Handlers) Commits(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}

	ref, path := h.commitsRef(repo, c.Param("rest"))
	if ref == "" {
		// Empty repo or unknown ref: redirect to the repo home which shows the
		// quick-setup guide when there are no commits.
		return c.Redirect(303, route.Repo(ownerLogin(repo), repo.Name))
	}

	commits, err := h.repos.ListCommits(repo, git.LogOpts{From: ref, Path: path, Max: commitsPerPage})
	if errors.Is(err, domain.ErrEmptyRepo) || errors.Is(err, domain.ErrGitNotFound) {
		return h.notFound(c)
	}
	if err != nil {
		return err
	}

	vm := view.CommitsVM{
		Chrome: h.chrome(c, "Commits · "+repo.FullName()),
		Header: h.header(repo, "commits"),
		Nav:    h.nav(repo, ref),
		Repo:   repoRef(repo),
		Ref:    view.Ref{Name: ref, IsDefault: ref == repo.DefaultBranch},
		Path:   path,
		Groups: groupCommitsByDate(repo, commits),
	}
	return h.render.Page(c, "repo/commits", vm)
}

// commitsRef resolves the commits tail into a ref and an optional path. An empty
// tail defaults to the repository's head branch. A non-empty tail must name a
// ref; the remainder is the path history filter and need not exist as a current
// path. A tail that names no ref yields an empty ref, a soft 404.
func (h *Handlers) commitsRef(repo *domain.Repo, rest string) (ref, path string) {
	if rest == "" {
		head, err := h.repos.DefaultBranchRef(repo)
		if err != nil {
			return "", ""
		}
		return head.Name, ""
	}
	ref, path, ok := route.SplitRefPath(rest, h.refExists(repo, h.loadRefs(repo)))
	if !ok {
		return "", ""
	}
	return ref, path
}

// groupCommitsByDate projects a flat commit list into date-headed groups in the
// order the history returned them, preserving the newest-first walk.
func groupCommitsByDate(repo *domain.Repo, commits []git.Commit) []view.CommitDateGroup {
	owner := ownerLogin(repo)
	var groups []view.CommitDateGroup
	var cur *view.CommitDateGroup
	for _, c := range commits {
		date := c.Author.When.UTC().Format("Jan 2, 2006")
		if cur == nil || cur.Date != date {
			groups = append(groups, view.CommitDateGroup{Date: date})
			cur = &groups[len(groups)-1]
		}
		cur.Commits = append(cur.Commits, view.CommitRowVM{
			SHA:         c.SHA,
			ShortSHA:    shortSHA(c.SHA),
			Title:       commitTitle(c.Message),
			Body:        commitBody(c.Message),
			AuthorName:  c.Author.Name,
			AuthorEmail: c.Author.Email,
			When:        c.Author.When.UTC().Format("Jan 2, 2006"),
			BrowseURL:   route.Tree(owner, repo.Name, c.SHA, ""),
			CommitURL:   route.Commit(owner, repo.Name, c.SHA),
		})
	}
	return groups
}

package repo

import (
	"errors"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// Commit renders the single-commit view: the commit message, author, and the
// unified diff against the first parent. GET /{owner}/{repo}/commit/{sha}. See
// implementation/07 section 8.
func (h *Handlers) Commit(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}

	sha := c.Param("sha")
	if sha == "" {
		return h.notFound(c)
	}

	commit, err := h.repos.GetCommit(repo, sha)
	if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
		return h.notFound(c)
	}
	if err != nil {
		return err
	}

	patch, err := h.repos.CommitPatch(repo, commit.SHA)
	if err != nil && !errors.Is(err, domain.ErrGitNotFound) {
		return err
	}

	owner := ownerLogin(repo)

	// Build parent short-SHA + URL pairs.
	var parentSHAs, parentURLs []string
	for _, p := range commit.Parents {
		parentSHAs = append(parentSHAs, shortSHA(string(p)))
		parentURLs = append(parentURLs, route.Commit(owner, repo.Name, string(p)))
	}

	vm := view.CommitVM{
		Chrome:      h.chrome(c, shortSHA(commit.SHA)+" · "+commitTitle(commit.Message)),
		Header:      h.header(repo, ""),
		Nav:         h.nav(repo, commit.SHA),
		Repo:        repoRef(repo),
		SHA:         commit.SHA,
		ShortSHA:    shortSHA(commit.SHA),
		Title:       commitTitle(commit.Message),
		Body:        commitBody(commit.Message),
		AuthorName:  commit.Author.Name,
		AuthorEmail: commit.Author.Email,
		When:        commit.Author.When.UTC().Format("Jan 2, 2006"),
		ParentSHAs:  parentSHAs,
		ParentURLs:  parentURLs,
		RawPatch:    patch,
		FilesCount:  countDiffFiles(patch),
		CommitsURL:  route.Commits(owner, repo.Name, commit.SHA, ""),
		TreeURL:     route.Tree(owner, repo.Name, commit.SHA, ""),
	}
	return h.render.Page(c, "repo/commit", vm)
}

// countDiffFiles counts the number of "diff --git" headers in a unified patch.
func countDiffFiles(patch string) int {
	n := strings.Count(patch, "\ndiff --git ")
	if strings.HasPrefix(patch, "diff --git ") {
		n++
	}
	return n
}

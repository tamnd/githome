package repo

import (
	"errors"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/git"
)

// maxCommitFiles caps how many file diffs the commit page inlines, the same
// ceiling the PR Files tab and the compare page observe. A vendored-dependency
// or generated commit can touch thousands of files; past the cap the page shows
// the first files and a note that points at the browse view for the rest.
const maxCommitFiles = 300

// Commit renders the single-commit view: the commit message, author, and the
// diff against the first parent rendered through the shared per-file diff
// component, one section per file with its own #diff- anchor. GET
// /{owner}/{repo}/commit/{sha}. See implementation/07 section 8.
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
	// The .diff and .patch suffixes select the plain-text twin of this page.
	if rest, format, ok := route.SplitPatchSuffix(sha); ok {
		return h.commitText(c, repo, rest, format)
	}

	commit, err := h.repos.GetCommit(repo, sha)
	if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
		return h.notFound(c)
	}
	if err != nil {
		return err
	}

	changes, err := h.repos.CommitFiles(ctx, repo, commit.SHA)
	if err != nil && !errors.Is(err, domain.ErrGitNotFound) {
		return err
	}

	owner := ownerLogin(repo)

	// FilesCount and the line totals come from the full change set before the
	// display cap, so the counts stay honest when the inline list is cut short.
	filesCount := len(changes)
	additions, deletions := 0, 0
	for _, ch := range changes {
		additions += ch.Additions
		deletions += ch.Deletions
	}
	truncated := false
	if len(changes) > maxCommitFiles {
		changes = changes[:maxCommitFiles]
		truncated = true
	}
	files := commitDiffFiles(changes, view.DiffUnified)

	// Build parent short-SHA + URL pairs.
	var parentSHAs, parentURLs []string
	for _, p := range commit.Parents {
		parentSHAs = append(parentSHAs, shortSHA(p))
		parentURLs = append(parentURLs, route.Commit(owner, repo.Name, p))
	}

	vm := view.CommitVM{
		Chrome:         h.chrome(c, shortSHA(commit.SHA)+" · "+commitTitle(commit.Message)),
		Header:         h.header(repo, ""),
		Nav:            h.nav(repo, commit.SHA),
		Repo:           repoRef(repo),
		SHA:            commit.SHA,
		ShortSHA:       shortSHA(commit.SHA),
		Title:          commitTitle(commit.Message),
		Body:           commitBody(commit.Message),
		AuthorName:     commit.Author.Name,
		AuthorEmail:    commit.Author.Email,
		When:           commit.Author.When.UTC().Format("Jan 2, 2006"),
		ParentSHAs:     parentSHAs,
		ParentURLs:     parentURLs,
		Files:          files,
		FilesTruncated: truncated,
		FilesCount:     filesCount,
		Additions:      additions,
		Deletions:      deletions,
		CommitsURL:     route.Commits(owner, repo.Name, commit.SHA, ""),
		TreeURL:        route.Tree(owner, repo.Name, commit.SHA, ""),
	}
	return h.render.Page(c, "repo/commit", vm)
}

// commitDiffFiles maps the commit's per-file changes into the shared diff
// component's file models, the same mapping the PR Files tab and the compare
// page apply.
func commitDiffFiles(changes []git.FileChange, mode view.DiffMode) []view.DiffFileVM {
	out := make([]view.DiffFileVM, 0, len(changes))
	for _, ch := range changes {
		out = append(out, view.BuildDiffFile(
			ch.Path,
			ch.PrevPath,
			view.FileStatus(ch.Status),
			ch.Additions,
			ch.Deletions,
			ch.Patch,
			mode,
		))
	}
	return out
}

// commitText serves /{owner}/{repo}/commit/{sha}.diff and .patch: the plain
// unified diff against the first parent (the whole tree for a root commit), or
// the commit as a format-patch mail. Both are uncapped text bodies; the inline
// cap belongs to the HTML page only.
func (h *Handlers) commitText(c *mizu.Ctx, repo *domain.Repo, sha, format string) error {
	ctx := c.Context()
	var body []byte
	var err error
	if format == "diff" {
		body, err = h.repos.CommitDiff(ctx, repo, sha)
	} else {
		body, err = h.repos.CommitFormatPatch(ctx, repo, sha)
	}
	if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
		return h.notFound(c)
	}
	if err != nil {
		return err
	}
	w := c.Writer()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, err = w.Write(body)
	return err
}

package compare

import (
	"errors"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/git"
)

// maxCompareFiles caps how many file diffs are inlined on the compare page, the
// same ceiling the PR Files tab observes.
const maxCompareFiles = 300

// Range renders the comparison between two branches, GET
// /{owner}/{repo}/compare/{basehead...}. The basehead tail is parsed as
// "base...head". With ?expand=1 the PR creation form is shown below the diff.
// A missing branch, or a basehead that does not parse, renders the soft 404.
// See implementation/09 section 8.
func (h *Handlers) Range(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	owner := ownerLogin(repo)

	base, head, ok := route.ParseBaseHead(c.Param("basehead"))
	if !ok {
		return h.notFound(c)
	}
	// When no base was specified (single-branch form), fall back to the default.
	if base == "" {
		db, err := h.repos.DefaultBranchRef(repo)
		if errors.Is(err, domain.ErrEmptyRepo) || errors.Is(err, domain.ErrGitNotFound) {
			return h.notFound(c)
		}
		if err != nil {
			return h.render.ServerError(c, err)
		}
		base = db.Name
	}

	cmp, err := h.repos.Compare(ctx, repo, base, head)
	if errors.Is(err, domain.ErrGitNotFound) {
		return h.notFound(c)
	}
	if err != nil {
		return h.render.ServerError(c, err)
	}

	files := buildFiles(cmp.Files)
	if len(files) > maxCompareFiles {
		files = files[:maxCompareFiles]
	}

	commits := buildCommits(owner, repo.Name, cmp.Commits)

	expanded := c.Query("expand") == "1"
	title := "Comparing " + base + "..." + head + " · " + owner + "/" + repo.Name
	vm := view.CompareRangeVM{
		Chrome:       h.chrome(c, title),
		Header:       h.header(repo, "pulls"),
		Nav:          h.nav(repo),
		Base:         branchVM(repo, cmp.Base),
		Head:         branchVM(repo, cmp.Head),
		MergeBase:    shortSHA(cmp.MergeBase),
		Commits:      commits,
		TotalCommits: cmp.TotalCommits,
		Files:        files,
		Additions:    cmp.Additions,
		Deletions:    cmp.Deletions,
		ChangedFiles: cmp.ChangedFiles,
		HasDiff:      len(cmp.Files) > 0,
		Expanded:     expanded,
		CreateURL:    route.Pulls(owner, repo.Name, ""),
		CSRFToken:    view.CSRFFrom(ctx),
		ExpandURL:    route.CompareExpanded(owner, repo.Name, base, head),
	}
	return h.render.Page(c, "compare/range", vm)
}

func buildFiles(changes []git.FileChange) []view.DiffFileVM {
	out := make([]view.DiffFileVM, 0, len(changes))
	for _, ch := range changes {
		out = append(out, view.BuildDiffFile(
			ch.Path, ch.PrevPath,
			view.FileStatus(ch.Status),
			ch.Additions, ch.Deletions,
			ch.Patch,
			view.DiffUnified,
		))
	}
	return out
}

func buildCommits(owner, repo string, commits []git.Commit) []view.CompareCommitVM {
	out := make([]view.CompareCommitVM, 0, len(commits))
	for _, c := range commits {
		when := c.Author.When.UTC()
		out = append(out, view.CompareCommitVM{
			ShortSHA:   shortSHA(c.SHA),
			Title:      commitTitle(c.Message),
			AuthorName: c.Author.Name,
			When:       when.Format("Jan 2, 2006"),
			WhenISO:    when.Format(time.RFC3339),
			URL:        route.Commit(owner, repo, c.SHA),
		})
	}
	return out
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

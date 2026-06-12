package repo

import (
	"errors"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// maxCommitPatchBytes caps the raw patch the commit page inlines. The patch is
// the whole commit against its first parent; a vendored-dependency or generated
// commit produces tens of megabytes, every byte of which would be escaped into
// one <pre>. Past the cap the page shows the head of the patch and a note that
// points at the browse view for the rest.
const maxCommitPatchBytes = 256 << 10

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

	patch, err := h.repos.CommitPatch(repo, commit.SHA)
	if err != nil && !errors.Is(err, domain.ErrGitNotFound) {
		return err
	}

	owner := ownerLogin(repo)

	// FilesCount comes from the full patch before the display cap, so the count
	// stays honest when the inline patch is cut short.
	filesCount := countDiffFiles(patch)
	patch, patchTruncated := truncatePatch(patch)

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
		RawPatch:       patch,
		PatchTruncated: patchTruncated,
		FilesCount:     filesCount,
		CommitsURL:     route.Commits(owner, repo.Name, commit.SHA, ""),
		TreeURL:        route.Tree(owner, repo.Name, commit.SHA, ""),
	}
	return h.render.Page(c, "repo/commit", vm)
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

// truncatePatch bounds a patch for inline display, cutting on a line boundary
// so the inline view ends on a whole diff line.
func truncatePatch(patch string) (string, bool) {
	if len(patch) <= maxCommitPatchBytes {
		return patch, false
	}
	cut := patch[:maxCommitPatchBytes]
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		cut = cut[:i+1]
	}
	return cut, true
}

// countDiffFiles counts the number of "diff --git" headers in a unified patch.
func countDiffFiles(patch string) int {
	n := strings.Count(patch, "\ndiff --git ")
	if strings.HasPrefix(patch, "diff --git ") {
		n++
	}
	return n
}

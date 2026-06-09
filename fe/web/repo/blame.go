package repo

import (
	"errors"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

// Blame renders the line-by-line blame for a file at a ref:
// GET /{owner}/{repo}/blame/{rest}. Each source line is annotated with the
// commit that last changed it; consecutive lines from the same commit are
// grouped so the commit metadata is shown once per hunk, not per line.
func (h *Handlers) Blame(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	ref, path, ok := h.resolveRef(repo, c.Param("rest"))
	if !ok || path == "" {
		return h.notFound(c)
	}
	_ = webmw.ViewerID(ctx)

	lines, err := h.repos.Blame(repo, ref, path)
	if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
		return h.notFound(c)
	}
	if err != nil {
		return err
	}

	owner := ownerLogin(repo)
	lineVMs := make([]view.BlameLineVM, len(lines))
	var prevSHA string
	for i, l := range lines {
		lineVMs[i] = view.BlameLineVM{
			LineNum:    l.LineNum,
			Text:       l.Text,
			SHA:        l.SHA,
			ShortSHA:   shortSHA(l.SHA),
			AuthorName: l.AuthorName,
			When:       l.When.UTC().Format("Jan 2, 2006"),
			CommitURL:  route.Commit(owner, repo.Name, l.SHA),
			NewGroup:   l.SHA != prevSHA,
		}
		prevSHA = l.SHA
	}

	vm := view.BlameVM{
		Chrome:  h.chrome(c, "Blame · "+path),
		Header:  h.header(repo, "blame"),
		Nav:     h.nav(repo, ref),
		Repo:    repoRef(repo),
		Ref:     view.Ref{Name: ref, IsDefault: ref == repo.DefaultBranch},
		Path:    path,
		Lines:   lineVMs,
		BlobURL: route.Blob(owner, repo.Name, ref, path),
	}
	return h.render.Page(c, "repo/blame", vm)
}

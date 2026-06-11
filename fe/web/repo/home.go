package repo

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/git"
)

// Home renders the Code tab at the default-branch root: the repo header, the tree
// listing, the About sidebar, and the auto-rendered README. The bare repo URL
// does not redirect to /tree/{default}; it renders the default root in place and
// the address bar stays /{owner}/{repo} (implementation/07 section 3). An empty
// repository renders the quick-setup page instead of a tree.
func (h *Handlers) Home(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}

	head, err := h.repos.DefaultBranchRef(repo)
	if errors.Is(err, domain.ErrEmptyRepo) {
		return h.quickSetup(c, repo)
	}
	if err != nil {
		return err
	}

	ref := view.Ref{Name: head.Name, CommitSHA: head.Commit, IsDefault: true}
	res, err := h.repos.Contents(repo, "", ref.Name)
	if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
		return h.notFound(c)
	}
	if err != nil {
		return err
	}
	tree := h.buildTreeFromDir(ctx, repo, h.loadRefs(repo), ref, "refs/heads/"+head.Name, "", res.Dir, true)

	vm := view.RepoHomeVM{
		Chrome: h.chrome(c, repo.Name),
		Header: h.header(repo, "code"),
		Nav:    h.nav(repo, ref.Name),
		Tree:   tree,
		About:  h.about(repo),
		Readme: tree.Readme,
	}
	return h.render.Page(c, "repo/home", vm)
}

// quickSetup renders the empty-repo home: the header plus the clone-and-push
// blocks (implementation/07 section 1.11).
func (h *Handlers) quickSetup(c *mizu.Ctx, repo *domain.Repo) error {
	vm := view.QuickSetupVM{
		Chrome: h.chrome(c, repo.Name),
		Header: h.header(repo, "code"),
		Nav:    h.nav(repo, repo.DefaultBranch),
		Clone:  h.clone(repo),
	}
	return h.render.Page(c, "repo/quick-setup", vm)
}

// about builds the repo home sidebar from the repository metadata. The topics
// column is the JSON array the REST surface stores; a malformed value renders
// as no topics rather than an error. The license chip and the languages bar
// wait for their domain fields.
func (h *Handlers) about(repo *domain.Repo) view.AboutVM {
	about := view.AboutVM{}
	if repo.Description != nil {
		about.Description = *repo.Description
	}
	if repo.Homepage != nil {
		about.Homepage = *repo.Homepage
	}
	if repo.Topics != "" {
		var topics []string
		if err := json.Unmarshal([]byte(repo.Topics), &topics); err == nil {
			about.Topics = topics
		}
	}
	return about
}

// buildTreeFromDir builds the tree view model from an already-read directory
// listing: the breadcrumb, the ref picker, the latest-commit bar, the sorted
// entries, and the README if the directory has one. The handler reads Contents
// once (to tell a tree from a blob) and hands the listing here, so a tree page is
// one Contents read plus the latest-commit walk. rev is the unambiguous revision
// the remaining git reads (latest commit, README) take, while ref.Name stays the
// short form every link and label shows. embedded marks the model as rendered
// inside the home page so the home and tree pages share it.
func (h *Handlers) buildTreeFromDir(ctx context.Context, repo *domain.Repo, refs *refSet, ref view.Ref, rev, p string, dir []git.PathEntry, embedded bool) view.TreeVM {
	return view.TreeVM{
		Header:    h.header(repo, "code"),
		Nav:       h.nav(repo, ref.Name),
		Repo:      repoRef(repo),
		Ref:       ref,
		Path:      p,
		Crumbs:    breadcrumbs(repo, ref.Name, p, false),
		RefPicker: h.refPicker(repo, refs, ref.Name, route.KindTree, p),
		Latest:    h.latestCommit(ctx, repo, rev, p),
		Entries:   treeEntries(repo, ref.Name, dir),
		Readme:    h.readme(ctx, repo, ref.Name, rev, dir),
		Clone:     h.clone(repo),
		Embedded:  embedded,
	}
}

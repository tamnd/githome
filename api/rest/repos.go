package rest

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/presenter/restmodel"
)

// handleRepoGet serves GET /repos/{owner}/{repo}. A repository the actor cannot
// see is a 404, never a 403, so a private repo's existence does not leak. The
// permissions block is present for authenticated callers and omitted otherwise.
func handleRepoGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		actor := auth.ActorFrom(c.Request().Context())
		body := d.URLs.Repository(repo, d.NodeFormat, repoPermissions(actor, repo))
		writeJSON(c.Writer(), http.StatusOK, body)
		return nil
	}
}

// handleBranches serves GET /repos/{owner}/{repo}/branches. An empty repository
// yields an empty array.
func handleBranches(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		branches, err := d.Repos.ListBranches(repo)
		if err != nil {
			return err
		}
		out := make([]restmodel.BranchShort, 0, len(branches))
		for _, br := range branches {
			out = append(out, d.URLs.BranchShort(repo.Owner.Login, repo.Name, br))
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleBranch serves GET /repos/{owner}/{repo}/branches/{branch}, the named
// branch with its full head commit and protection summary.
func handleBranch(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		br, err := d.Repos.GetBranch(repo, c.Param("branch"))
		if errors.Is(err, domain.ErrGitNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		head, err := d.Repos.GetCommit(repo, br.Commit)
		if err != nil {
			return err
		}
		body := d.URLs.Branch(repo.Owner.Login, repo.Name, repo.ID, br, head)
		writeJSON(c.Writer(), http.StatusOK, body)
		return nil
	}
}

// handleTags serves GET /repos/{owner}/{repo}/tags. An empty repository yields
// an empty array.
func handleTags(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		tags, err := d.Repos.ListTags(repo)
		if err != nil {
			return err
		}
		out := make([]restmodel.Tag, 0, len(tags))
		for _, t := range tags {
			out = append(out, d.URLs.Tag(repo.Owner.Login, repo.Name, repo.ID, t))
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleCommits serves GET /repos/{owner}/{repo}/commits. The optional sha and
// path queries scope the walk; per_page caps it. A repository with no commits
// is a 409, matching GitHub.
func handleCommits(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		opts := git.LogOpts{From: c.Query("sha"), Path: c.Query("path"), Max: perPage(c)}
		commits, err := d.Repos.ListCommits(repo, opts)
		if errors.Is(err, domain.ErrEmptyRepo) {
			writeError(c.Writer(), errConflict("Git Repository is empty."))
			return nil
		}
		if errors.Is(err, domain.ErrGitNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		out := make([]restmodel.RepoCommit, 0, len(commits))
		for _, cm := range commits {
			out = append(out, d.URLs.RepoCommit(repo.Owner.Login, repo.Name, repo.ID, cm))
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleContents serves GET /repos/{owner}/{repo}/contents/{path}. A blob path
// returns a single file object with base64 content; a tree path returns a
// directory listing array. The ref query selects the revision, defaulting to
// the repository's default branch.
func handleContents(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		ref := c.Query("ref")
		if ref == "" {
			ref = repo.DefaultBranch
		}
		res, err := d.Repos.Contents(repo, c.Param("path"), ref)
		if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		if res.IsDir {
			writeJSON(c.Writer(), http.StatusOK, d.URLs.ContentDir(repo.Owner.Login, repo.Name, ref, res.Dir))
			return nil
		}
		body := d.URLs.ContentFile(repo.Owner.Login, repo.Name, ref, res.Entry, res.File.Content)
		writeJSON(c.Writer(), http.StatusOK, body)
		return nil
	}
}

// handleBlob serves GET /repos/{owner}/{repo}/git/blobs/{sha}.
func handleBlob(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		blob, err := d.Repos.GetBlob(repo, c.Param("sha"))
		if gitNotFound(err) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.Blob(repo.Owner.Login, repo.Name, repo.ID, blob))
		return nil
	}
}

// handleTree serves GET /repos/{owner}/{repo}/git/trees/{sha}. The recursive
// query walks the whole subtree.
func handleTree(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		tree, err := d.Repos.GetTree(repo, c.Param("sha"), truthy(c.Query("recursive")))
		if gitNotFound(err) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.Tree(repo.Owner.Login, repo.Name, tree))
		return nil
	}
}

// handleGitCommit serves GET /repos/{owner}/{repo}/git/commits/{sha}.
func handleGitCommit(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		commit, err := d.Repos.GetCommit(repo, c.Param("sha"))
		if gitNotFound(err) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.GitCommit(repo.Owner.Login, repo.Name, repo.ID, commit))
		return nil
	}
}

// handleRefs serves GET /repos/{owner}/{repo}/git/refs, every branch and tag
// ref. An empty repository yields an empty array.
func handleRefs(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		refs, err := d.Repos.ListRefs(repo)
		if err != nil {
			return err
		}
		out := make([]restmodel.GitRefObject, 0, len(refs))
		for _, ref := range refs {
			out = append(out, d.URLs.GitRef(repo.Owner.Login, repo.Name, repo.ID, ref))
		}
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// handleRef serves GET /repos/{owner}/{repo}/git/ref/{ref}, a single reference
// such as heads/main or tags/v1.0.
func handleRef(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		ref, err := d.Repos.GetRef(repo, c.Param("ref"))
		if gitNotFound(err) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.GitRef(repo.Owner.Login, repo.Name, repo.ID, ref))
		return nil
	}
}

// loadRepo resolves {owner}/{repo} for the request actor. It returns (nil, nil)
// after writing the 404 when the repository is missing or invisible, so callers
// short-circuit on a nil repo; any other error is returned for the central
// error handler.
func loadRepo(d Deps, c *mizu.Ctx) (*domain.Repo, error) {
	ctx := c.Request().Context()
	actor := auth.ActorFrom(ctx)
	repo, err := d.Repos.GetRepo(ctx, actor.UserID, c.Param("owner"), c.Param("repo"))
	if errors.Is(err, domain.ErrRepoNotFound) {
		writeError(c.Writer(), errNotFound())
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return repo, nil
}

// repoPermissions returns the actor's effective permission block: all-true for
// the owner, pull-only for any other authenticated user, and nil (omitted) for
// an anonymous caller.
func repoPermissions(actor *auth.Actor, repo *domain.Repo) *restmodel.RepoPermissions {
	if actor == nil || !actor.IsUser() {
		return nil
	}
	if actor.UserID == repo.OwnerPK {
		return presenter.OwnerPermissions()
	}
	return presenter.ReadPermissions()
}

// gitNotFound reports whether err is a git lookup that should surface as a 404:
// a missing object or path, or a read against a commitless repository.
func gitNotFound(err error) bool {
	return errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo)
}

// perPage reads the per_page query, clamping to GitHub's 1..100 range with a
// default of 30.
func perPage(c *mizu.Ctx) int {
	n, err := strconv.Atoi(c.Query("per_page"))
	if err != nil || n <= 0 {
		return 30
	}
	if n > 100 {
		return 100
	}
	return n
}

// truthy reports whether a query flag is set to a true-ish value.
func truthy(v string) bool {
	switch v {
	case "1", "true", "t", "yes", "on":
		return true
	default:
		return false
	}
}

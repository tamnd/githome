package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/etag"
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
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		det, err := repoDetail(d, c, repo)
		if err != nil {
			return err
		}
		perm, err := repoPermissions(ctx, d, actor, repo)
		if err != nil {
			return err
		}
		body := d.URLs.RepositoryFull(repo, d.NodeFormat, perm, det)
		tag := etag.Version("repo", repo.ID, repo.UpdatedAt.UnixNano())
		conditionalVersioned(c.Writer(), c.Request(), http.StatusOK, body, tag)
		return nil
	}
}

// repoDetail assembles the extras only the single-repository responses carry:
// the fork network count, the organization block for org-owned repositories,
// and the resolved parent/source chain for forks. subscribers_count stays
// zero until watching lands. A fork whose parent the actor cannot see simply
// omits parent/source, the same non-leak rule as everywhere else.
func repoDetail(d Deps, c *mizu.Ctx, repo *domain.Repo) (presenter.RepoDetail, error) {
	ctx := c.Request().Context()
	actor := auth.ActorFrom(ctx)
	det := presenter.RepoDetail{}

	n, err := d.Repos.ForksCount(ctx, repo.PK)
	if err != nil {
		return det, err
	}
	det.NetworkCount = n

	if repo.Owner != nil && repo.Owner.Type == "Organization" {
		det.Organization = repo.Owner
	}

	if repo.ForkOfPK != nil {
		parent, err := d.Repos.GetRepoByPK(ctx, actor.UserID, *repo.ForkOfPK)
		if errors.Is(err, domain.ErrRepoNotFound) {
			return det, nil
		}
		if err != nil {
			return det, err
		}
		p := d.URLs.Repository(parent, d.NodeFormat, nil)
		det.Parent = &p
		src := parent
		for src.ForkOfPK != nil {
			next, err := d.Repos.GetRepoByPK(ctx, actor.UserID, *src.ForkOfPK)
			if err != nil {
				break
			}
			src = next
		}
		sr := d.URLs.Repository(src, d.NodeFormat, nil)
		det.Source = &sr
	}
	return det, nil
}

// handleBranches serves GET /repos/{owner}/{repo}/branches. An empty repository
// yields an empty array.
func handleBranches(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		page, perr := parsePageFor(c, "Repository")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		branches, err := d.Repos.ListBranches(repo)
		if err != nil {
			return err
		}
		branches = paginateSlice(&page, branches)
		out := make([]restmodel.BranchShort, 0, len(branches))
		for _, br := range branches {
			out = append(out, d.URLs.BranchShort(repo.Owner.Login, repo.Name, br))
		}
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
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
		page, perr := parsePageFor(c, "Repository")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		tags, err := d.Repos.ListTags(repo)
		if err != nil {
			return err
		}
		tags = paginateSlice(&page, tags)
		out := make([]restmodel.Tag, 0, len(tags))
		for _, t := range tags {
			out = append(out, d.URLs.Tag(repo.Owner.Login, repo.Name, repo.ID, t))
		}
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		writeJSON(c.Writer(), http.StatusOK, out)
		return nil
	}
}

// timeQuery reads an ISO 8601 timestamp query parameter. A missing or empty
// parameter is nil; one that does not parse is the structured 422 GitHub
// sends for a malformed since or until.
func timeQuery(c *mizu.Ctx, name string) (*time.Time, *apiError) {
	v := c.Query(name)
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, errValidation(FieldError{Resource: "Commit", Field: name, Code: "invalid"})
	}
	return &t, nil
}

// handleCommits serves GET /repos/{owner}/{repo}/commits. The optional sha and
// path queries scope the walk; author and committer filter by name or email;
// since and until bound it by commit time; per_page caps it. A repository
// with no commits is a 409, matching GitHub.
func handleCommits(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		page, perr := parsePageFor(c, "Repository")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		since, perr := timeQuery(c, "since")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		until, perr := timeQuery(c, "until")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		// Skip straight to the requested window and walk one commit past it:
		// the window itself is the page, and the extra commit is the existence
		// proof for rel="next" without counting the whole history.
		opts := git.LogOpts{
			From: c.Query("sha"), Path: c.Query("path"),
			Author: c.Query("author"), Committer: c.Query("committer"),
			Since: since, Until: until,
			Skip: page.Offset(), Max: page.PerPage + 1,
		}
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
		hasNext := len(commits) > page.PerPage
		window := commits
		if hasNext {
			window = commits[:page.PerPage]
		}
		out := make([]restmodel.RepoCommit, 0, len(window))
		for _, cm := range window {
			out = append(out, d.URLs.RepoCommit(repo.Owner.Login, repo.Name, repo.ID, cm))
		}
		writeLinkHeaderUncounted(c.Writer(), c.Request(), d.URLs, page, hasNext)
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
		if errors.Is(err, domain.ErrBlobTooLarge) {
			writeError(c.Writer(), errBlobTooLarge())
			return nil
		}
		if err != nil {
			return err
		}
		if res.IsDir {
			conditionalJSON(c.Writer(), c.Request(), http.StatusOK, d.URLs.ContentDir(repo.Owner.Login, repo.Name, ref, res.Dir))
			return nil
		}
		body := d.URLs.ContentFile(repo.Owner.Login, repo.Name, ref, res.Entry, res.File.Content)
		conditionalJSON(c.Writer(), c.Request(), http.StatusOK, body)
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
		if errors.Is(err, domain.ErrBlobTooLarge) {
			writeError(c.Writer(), errBlobTooLarge())
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
		conditionalJSON(c.Writer(), c.Request(), http.StatusOK, out)
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
		conditionalJSON(c.Writer(), c.Request(), http.StatusOK, d.URLs.GitRef(repo.Owner.Login, repo.Name, repo.ID, ref))
		return nil
	}
}

// repoPatchBody is the JSON body for PATCH /repos/{owner}/{repo}.
// SecurityAndAnalysis is accepted so clients that always send the block (the
// Terraform provider does) are not rejected, but githome has no security
// products to toggle, so nothing from it is stored and the repository object
// does not render it back.
type repoPatchBody struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	Homepage      *string `json:"homepage"`
	DefaultBranch *string `json:"default_branch"`
	Private       *bool   `json:"private"`
	HasIssues     *bool   `json:"has_issues"`
	HasProjects   *bool   `json:"has_projects"`
	HasWiki       *bool   `json:"has_wiki"`
	Archived      *bool   `json:"archived"`
	IsTemplate    *bool   `json:"is_template"`

	AllowSquashMerge         *bool   `json:"allow_squash_merge"`
	AllowMergeCommit         *bool   `json:"allow_merge_commit"`
	AllowRebaseMerge         *bool   `json:"allow_rebase_merge"`
	AllowAutoMerge           *bool   `json:"allow_auto_merge"`
	DeleteBranchOnMerge      *bool   `json:"delete_branch_on_merge"`
	AllowUpdateBranch        *bool   `json:"allow_update_branch"`
	WebCommitSignoffRequired *bool   `json:"web_commit_signoff_required"`
	Visibility               *string `json:"visibility"`

	SecurityAndAnalysis json.RawMessage `json:"security_and_analysis"`
}

// handleRepoUpdate serves PATCH /repos/{owner}/{repo}. Only the repository
// owner may update settings.
func handleRepoUpdate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		var body repoPatchBody
		if !decodeJSON(c, &body) {
			return nil
		}
		// visibility is the modern spelling of the private flag; when both are
		// sent, visibility wins. Githome has no internal visibility, so only
		// the two real values are accepted.
		if body.Visibility != nil {
			switch *body.Visibility {
			case "public":
				f := false
				body.Private = &f
			case "private":
				t := true
				body.Private = &t
			default:
				writeError(c.Writer(), errValidation(FieldError{
					Resource: "Repository", Field: "visibility", Code: "invalid",
				}))
				return nil
			}
		}
		owner, name := c.Param("owner"), c.Param("repo")
		repo, err := d.Repos.UpdateRepo(ctx, actor.UserID, owner, name, domain.RepoPatch{
			Name:          body.Name,
			Description:   body.Description,
			Homepage:      body.Homepage,
			DefaultBranch: body.DefaultBranch,
			Private:       body.Private,
			HasIssues:     body.HasIssues,
			HasProjects:   body.HasProjects,
			HasWiki:       body.HasWiki,
			Archived:      body.Archived,
			IsTemplate:    body.IsTemplate,

			AllowSquashMerge:         body.AllowSquashMerge,
			AllowMergeCommit:         body.AllowMergeCommit,
			AllowRebaseMerge:         body.AllowRebaseMerge,
			AllowAutoMerge:           body.AllowAutoMerge,
			DeleteBranchOnMerge:      body.DeleteBranchOnMerge,
			AllowUpdateBranch:        body.AllowUpdateBranch,
			WebCommitSignoffRequired: body.WebCommitSignoffRequired,
		})
		if errors.Is(err, domain.ErrRepoNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if errors.Is(err, domain.ErrForbidden) {
			writeError(c.Writer(), errForbidden("Must have admin rights to Repository."))
			return nil
		}
		if err != nil {
			return err
		}
		det, err := repoDetail(d, c, repo)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.RepositoryFull(repo, d.NodeFormat, presenter.OwnerPermissions(), det))
		return nil
	}
}

// handleRepoDelete serves DELETE /repos/{owner}/{repo}. Only the repository
// owner may delete. A successful delete returns 204.
func handleRepoDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		owner, name := c.Param("owner"), c.Param("repo")
		err := d.Repos.DeleteRepo(ctx, actor.UserID, owner, name)
		if errors.Is(err, domain.ErrRepoNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if errors.Is(err, domain.ErrForbidden) {
			writeError(c.Writer(), errForbidden("Must have admin rights to Repository."))
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
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

// repoPermissions resolves the actor's effective permission block through the
// owner and collaborator grants: all-true for the owner, the granted role
// expanded for a collaborator, pull-only for any other authenticated viewer,
// and nil (omitted) for an anonymous caller.
func repoPermissions(ctx context.Context, d Deps, actor *auth.Actor, repo *domain.Repo) (*restmodel.RepoPermissions, error) {
	if actor == nil || !actor.IsUser() {
		return nil, nil
	}
	role, err := d.Repos.RepoPermission(ctx, actor.UserID, repo)
	if err != nil {
		return nil, err
	}
	return permissionBlock(role), nil
}

// permissionBlock expands a role name into GitHub's permission booleans; each
// role implies everything below it (admin > maintain > push > triage > pull).
// An empty or unknown role yields nil, omitting the block.
func permissionBlock(role string) *restmodel.RepoPermissions {
	p := &restmodel.RepoPermissions{}
	switch role {
	case "admin":
		p.Admin = true
		fallthrough
	case "maintain":
		p.Maintain = true
		fallthrough
	case "push":
		p.Push = true
		fallthrough
	case "triage":
		p.Triage = true
		fallthrough
	case "pull":
		p.Pull = true
	default:
		return nil
	}
	return p
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

package domain

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
	"github.com/tamnd/githome/worker"
)

// The repo service errors. The REST layer maps them to status: a repository the
// actor cannot see is reported as not found rather than forbidden, so a private
// repo's existence never leaks through a 403. An empty repository is not an
// error for branch and tag listings (they come back empty) but is a 404 for the
// commit, tree, blob, and contents reads that need a head commit.
var (
	// ErrRepoNotFound is returned when no repository matches the lookup or the
	// actor is not allowed to see it.
	ErrRepoNotFound = errors.New("domain: repository not found")

	// ErrGitNotFound is returned when a ref, revision, object id, or path does
	// not resolve within a repository.
	ErrGitNotFound = errors.New("domain: git object or path not found")

	// ErrEmptyRepo is returned by head-dependent reads on a repository that has
	// no commits yet.
	ErrEmptyRepo = errors.New("domain: repository is empty")

	// ErrBlobTooLarge is returned when a blob or file read exceeds the server's
	// blob size ceiling. The REST layer maps it to a 403 too_large.
	ErrBlobTooLarge = errors.New("domain: blob exceeds size limit")

	// ErrConflict is returned by a file write whose CurrentBlobSHA no longer
	// matches the blob at the path. The REST layer maps it to GitHub's 409.
	ErrConflict = errors.New("domain: current blob sha mismatch")

	// ErrRepoExists is returned when a create or fork would claim a name the
	// target account already uses for an unrelated repository.
	ErrRepoExists = errors.New("domain: repository name already exists")
)

// RepoStore is the slice of the store the repo service needs. The write path
// (the post-receive sink) adds the repo-by-pk lookup, the pushed_at touch, and
// the job enqueue; enqueuing through the store keeps the domain on its single
// store dependency rather than importing the worker package.
type RepoStore interface {
	RepoByOwnerName(ctx context.Context, owner, name string) (*store.RepoRow, error)
	RepoByPK(ctx context.Context, pk int64) (*store.RepoRow, error)
	RepoByDBID(ctx context.Context, dbID int64) (*store.RepoRow, error)
	RepoByRedirect(ctx context.Context, owner, name string) (*store.RepoRow, error)
	UpsertRepoRedirect(ctx context.Context, oldOwner, oldName string, repoPK int64) error
	ReposByOwner(ctx context.Context, ownerPK int64) ([]*store.RepoRow, error)
	UserByPK(ctx context.Context, pk int64) (*store.UserRow, error)
	UserByLogin(ctx context.Context, login string) (*store.UserRow, error)
	InsertRepo(ctx context.Context, r *store.RepoRow) error
	UpdateRepo(ctx context.Context, pk int64, p store.RepoPatch) (*store.RepoRow, error)
	SoftDeleteRepo(ctx context.Context, pk int64) error
	CountForks(ctx context.Context, pk int64) (int64, error)
	ForksOf(ctx context.Context, pk int64) ([]*store.RepoRow, error)
	ReposByCollaborator(ctx context.Context, userPK int64) ([]*store.RepoRow, error)
	ReposByTeamMember(ctx context.Context, userPK int64) ([]*store.RepoRow, error)
	CollaboratorByRepo(ctx context.Context, repoPK, userPK int64) (*store.CollaboratorRow, error)
	TouchRepoPushedAt(ctx context.Context, pk int64, at time.Time) error
	EnqueueJob(ctx context.Context, j *store.JobRow) (bool, error)
	InsertEvent(ctx context.Context, e *store.EventRow) error
}

// RepoService resolves repositories and reads their git data. It pairs the
// metadata store with the git object store: GetRepo authorizes and assembles
// the domain Repo, and the git-data methods open the repository's bare git
// store through the internal pk GetRepo carried.
type RepoService struct {
	store    RepoStore
	gitStore *git.Store
	enq      worker.Enqueuer
}

// NewRepoService builds a RepoService over the metadata store and the git store.
// The push sink submits its jobs through a store-backed enqueuer built from the
// same store, so a push records its events in the durable queue.
func NewRepoService(st RepoStore, gs *git.Store) *RepoService {
	return &RepoService{store: st, gitStore: gs, enq: worker.NewStoreEnqueuer(st)}
}

// GetRepo resolves a repository by owner login and name for the given viewer
// (the authenticated user's internal pk, or 0 when anonymous). A private
// repository the viewer does not own is reported as ErrRepoNotFound, the same
// as a repository that does not exist, so a private repo never leaks through
// the status code.
func (s *RepoService) GetRepo(ctx context.Context, viewerPK int64, owner, name string) (*Repo, error) {
	row, err := s.store.RepoByOwnerName(ctx, owner, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrRepoNotFound
	}
	if err != nil {
		return nil, err
	}
	visible, err := s.viewerCanSee(ctx, row, viewerPK)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, ErrRepoNotFound
	}
	ownerRow, err := s.store.UserByPK(ctx, row.OwnerPK)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrRepoNotFound
	}
	if err != nil {
		return nil, err
	}
	return repoFromRow(row, userFromRow(ownerRow)), nil
}

// GetRepoByID resolves a repository by its public database id for the viewer,
// applying the same visibility rule as GetRepo: a private repository the viewer
// cannot see is ErrRepoNotFound, never leaked. The GraphQL mutations decode a
// repository node id to this database id, then act through the owner-login and
// name path the rest of the domain uses.
func (s *RepoService) GetRepoByID(ctx context.Context, viewerPK, dbID int64) (*Repo, error) {
	row, err := s.store.RepoByDBID(ctx, dbID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrRepoNotFound
	}
	if err != nil {
		return nil, err
	}
	visible, err := s.viewerCanSee(ctx, row, viewerPK)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, ErrRepoNotFound
	}
	ownerRow, err := s.store.UserByPK(ctx, row.OwnerPK)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrRepoNotFound
	}
	if err != nil {
		return nil, err
	}
	return repoFromRow(row, userFromRow(ownerRow)), nil
}

// GetRepoByPK resolves a repository by its internal primary key for the viewer,
// applying the same visibility rule as GetRepo. It is used when the caller has
// decoded a ref node ID (which embeds the internal PK) and needs the full Repo.
func (s *RepoService) GetRepoByPK(ctx context.Context, viewerPK, repoPK int64) (*Repo, error) {
	row, err := s.store.RepoByPK(ctx, repoPK)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrRepoNotFound
	}
	if err != nil {
		return nil, err
	}
	visible, err := s.viewerCanSee(ctx, row, viewerPK)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, ErrRepoNotFound
	}
	ownerRow, err := s.store.UserByPK(ctx, row.OwnerPK)
	if err != nil {
		return nil, err
	}
	return repoFromRow(row, userFromRow(ownerRow)), nil
}

// RepoInput holds the caller-supplied fields for creating a new repository.
// The nil-able feature and merge flags mean "use the default" (issues,
// projects, wiki, and the three merge methods on; auto-merge and branch
// deletion off). GitignoreTemplate and LicenseTemplate name templates from
// init_templates.go; the API layer validates them before calling CreateRepo,
// and a non-empty template implies an initial commit even without AutoInit,
// matching GitHub.
type RepoInput struct {
	Name          string
	Description   *string
	Homepage      *string
	Private       bool
	AutoInit      bool   // init with a README commit
	DefaultBranch string // default "main"

	HasIssues   *bool
	HasProjects *bool
	HasWiki     *bool
	IsTemplate  bool

	AllowSquashMerge    *bool
	AllowMergeCommit    *bool
	AllowRebaseMerge    *bool
	AllowAutoMerge      *bool
	DeleteBranchOnMerge *bool

	GitignoreTemplate string
	LicenseTemplate   string
}

// RepoPatch holds nullable editable fields for PATCH /repos/{owner}/{repo}.
// A nil field leaves the stored value unchanged.
type RepoPatch struct {
	Name          *string
	Description   *string
	Homepage      *string
	DefaultBranch *string
	Private       *bool
	HasIssues     *bool
	HasProjects   *bool
	HasWiki       *bool
	Archived      *bool
	IsTemplate    *bool

	AllowSquashMerge         *bool
	AllowMergeCommit         *bool
	AllowRebaseMerge         *bool
	AllowAutoMerge           *bool
	DeleteBranchOnMerge      *bool
	AllowUpdateBranch        *bool
	WebCommitSignoffRequired *bool
}

// ListReposByLogin returns all non-deleted repositories owned by ownerLogin,
// filtered by the viewer's visibility. The owner login is resolved to an
// internal PK via the store. ErrUserNotFound is returned when no such account
// exists.
func (s *RepoService) ListReposByLogin(ctx context.Context, viewerPK int64, ownerLogin string) ([]*Repo, error) {
	ownerRow, err := s.store.UserByLogin(ctx, ownerLogin)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	owner := userFromRow(ownerRow)
	rows, err := s.store.ReposByOwner(ctx, ownerRow.PK)
	if err != nil {
		return nil, err
	}
	out := make([]*Repo, 0, len(rows))
	for _, r := range rows {
		if canSee(r, viewerPK) {
			out = append(out, repoFromRow(r, owner))
		}
	}
	return out, nil
}

// ListRepos returns all non-deleted repositories owned by ownerPK, filtered by
// the viewer's visibility. Anonymous viewers and non-owner viewers see only
// public repos; the owner sees all.
func (s *RepoService) ListRepos(ctx context.Context, viewerPK, ownerPK int64) ([]*Repo, error) {
	ownerRow, err := s.store.UserByPK(ctx, ownerPK)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	owner := userFromRow(ownerRow)
	rows, err := s.store.ReposByOwner(ctx, ownerPK)
	if err != nil {
		return nil, err
	}
	out := make([]*Repo, 0, len(rows))
	for _, r := range rows {
		if canSee(r, viewerPK) {
			out = append(out, repoFromRow(r, owner))
		}
	}
	return out, nil
}

// ListCollaboratorRepos returns the non-deleted repositories userPK holds a
// direct collaborator grant on, filtered by the viewer's visibility. It backs
// the member type and the collaborator affiliation of the repository lists.
func (s *RepoService) ListCollaboratorRepos(ctx context.Context, viewerPK, userPK int64) ([]*Repo, error) {
	rows, err := s.store.ReposByCollaborator(ctx, userPK)
	if err != nil {
		return nil, err
	}
	return s.visibleWithOwners(ctx, viewerPK, rows)
}

// ListTeamRepos returns the non-deleted repositories userPK can reach through
// a team grant, filtered by the viewer's visibility. It backs the
// organization_member affiliation of GET /user/repos.
func (s *RepoService) ListTeamRepos(ctx context.Context, viewerPK, userPK int64) ([]*Repo, error) {
	rows, err := s.store.ReposByTeamMember(ctx, userPK)
	if err != nil {
		return nil, err
	}
	return s.visibleWithOwners(ctx, viewerPK, rows)
}

// visibleWithOwners resolves each row's owning account and keeps the rows the
// viewer can see, the shared tail of the cross-owner repository lists. The
// check runs through viewerCanSee so a private repository stays in the list
// for the collaborator the grant names.
func (s *RepoService) visibleWithOwners(ctx context.Context, viewerPK int64, rows []*store.RepoRow) ([]*Repo, error) {
	out := make([]*Repo, 0, len(rows))
	for _, r := range rows {
		ok, err := s.viewerCanSee(ctx, r, viewerPK)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		ownerRow, err := s.store.UserByPK(ctx, r.OwnerPK)
		if err != nil {
			continue
		}
		out = append(out, repoFromRow(r, userFromRow(ownerRow)))
	}
	return out, nil
}

// CreateRepo creates a new repository owned by ownerLogin under the authenticated
// actor (viewerPK). The actor must own the target account (or be a site admin).
func (s *RepoService) CreateRepo(ctx context.Context, viewerPK int64, ownerLogin string, inp RepoInput) (*Repo, error) {
	ownerRow, err := s.store.UserByLogin(ctx, ownerLogin)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	if ownerRow.PK != viewerPK && !ownerRow.SiteAdmin {
		return nil, ErrForbidden
	}
	if inp.DefaultBranch == "" {
		inp.DefaultBranch = "main"
	}
	row := &store.RepoRow{
		OwnerPK:       ownerRow.PK,
		Name:          inp.Name,
		Description:   inp.Description,
		Homepage:      inp.Homepage,
		Private:       inp.Private,
		DefaultBranch: inp.DefaultBranch,
	}
	if err := s.store.InsertRepo(ctx, row); err != nil {
		return nil, err
	}
	// The insert leaves the feature and merge flags on their column defaults;
	// settings the caller chose explicitly are applied in a follow-up patch so
	// the insert path stays one shape.
	sp := store.RepoPatch{
		HasIssues:           inp.HasIssues,
		HasProjects:         inp.HasProjects,
		HasWiki:             inp.HasWiki,
		AllowSquashMerge:    inp.AllowSquashMerge,
		AllowMergeCommit:    inp.AllowMergeCommit,
		AllowRebaseMerge:    inp.AllowRebaseMerge,
		AllowAutoMerge:      inp.AllowAutoMerge,
		DeleteBranchOnMerge: inp.DeleteBranchOnMerge,
	}
	if inp.IsTemplate {
		t := true
		sp.IsTemplate = &t
	}
	if sp != (store.RepoPatch{}) {
		row, err = s.store.UpdateRepo(ctx, row.PK, sp)
		if err != nil {
			return nil, err
		}
	}
	if _, err := s.gitStore.Init(row.PK); err != nil {
		return nil, err
	}
	owner := userFromRow(ownerRow)
	repo := repoFromRow(row, owner)
	if inp.AutoInit || inp.GitignoreTemplate != "" || inp.LicenseTemplate != "" {
		if err := s.writeInitialCommit(ctx, repo, inp, ownerRow); err != nil {
			return nil, err
		}
	}
	return repo, nil
}

// writeInitialCommit seeds the freshly created repository: a README named
// after the repo, plus the .gitignore and LICENSE templates the caller asked
// for. Each file lands as its own commit on the default branch (GitHub folds
// them into one; the file-write path here works a file at a time, and the
// resulting tree is identical). pushed_at is stamped the way a real first
// push would.
func (s *RepoService) writeInitialCommit(ctx context.Context, repo *Repo, inp RepoInput, ownerRow *store.UserRow) error {
	authorName := ownerRow.Login
	if ownerRow.Name != nil && *ownerRow.Name != "" {
		authorName = *ownerRow.Name
	}
	authorEmail := ""
	if ownerRow.Email != nil {
		authorEmail = *ownerRow.Email
	}
	write := func(path string, content []byte) error {
		_, err := s.WriteFile(repo, WriteFileInput{
			Path:        path,
			Content:     content,
			Message:     "Initial commit",
			AuthorName:  authorName,
			AuthorEmail: authorEmail,
			Branch:      repo.DefaultBranch,
		})
		return err
	}
	readme := "# " + repo.Name + "\n"
	if repo.Description != nil && *repo.Description != "" {
		readme += "\n" + *repo.Description + "\n"
	}
	if err := write("README.md", []byte(readme)); err != nil {
		return err
	}
	if inp.GitignoreTemplate != "" {
		body, ok := GitignoreTemplate(inp.GitignoreTemplate)
		if ok {
			if err := write(".gitignore", []byte(body)); err != nil {
				return err
			}
		}
	}
	if inp.LicenseTemplate != "" {
		lic, ok := LicenseTemplate(inp.LicenseTemplate)
		if ok {
			year := strconv.Itoa(time.Now().UTC().Year())
			body := fillLicense(lic.Body, year, authorName)
			if err := write("LICENSE", []byte(body)); err != nil {
				return err
			}
		}
	}
	now := time.Now().UTC()
	if err := s.store.TouchRepoPushedAt(ctx, repo.PK, now); err != nil {
		return err
	}
	repo.PushedAt = &now
	return nil
}

// UpdateRepo applies patch to the repository identified by owner/name for the
// given viewer. Only the repository owner may update settings.
func (s *RepoService) UpdateRepo(ctx context.Context, viewerPK int64, owner, name string, p RepoPatch) (*Repo, error) {
	row, err := s.store.RepoByOwnerName(ctx, owner, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrRepoNotFound
	}
	if err != nil {
		return nil, err
	}
	if row.OwnerPK != viewerPK {
		return nil, ErrForbidden
	}
	sp := store.RepoPatch{
		Name:          p.Name,
		Description:   p.Description,
		Homepage:      p.Homepage,
		DefaultBranch: p.DefaultBranch,
		Private:       p.Private,
		HasIssues:     p.HasIssues,
		HasProjects:   p.HasProjects,
		HasWiki:       p.HasWiki,
		Archived:      p.Archived,
		IsTemplate:    p.IsTemplate,

		AllowSquashMerge:         p.AllowSquashMerge,
		AllowMergeCommit:         p.AllowMergeCommit,
		AllowRebaseMerge:         p.AllowRebaseMerge,
		AllowAutoMerge:           p.AllowAutoMerge,
		DeleteBranchOnMerge:      p.DeleteBranchOnMerge,
		AllowUpdateBranch:        p.AllowUpdateBranch,
		WebCommitSignoffRequired: p.WebCommitSignoffRequired,
	}
	oldName := row.Name
	updated, err := s.store.UpdateRepo(ctx, row.PK, sp)
	if err != nil {
		return nil, err
	}
	ownerRow, err := s.store.UserByPK(ctx, updated.OwnerPK)
	if err != nil {
		return nil, err
	}
	// A rename leaves a redirect behind so the old URL keeps working. The
	// redirect points at the repository row, not at the new name, so a chain
	// of renames collapses to wherever the repo currently lives, and a new
	// repository claiming the old name shadows the redirect because the direct
	// lookup always runs first. A case-only rename records nothing: the direct
	// lookup is case-insensitive and still hits.
	if p.Name != nil && !strings.EqualFold(*p.Name, oldName) {
		if err := s.store.UpsertRepoRedirect(ctx, ownerRow.Login, oldName, row.PK); err != nil {
			return nil, err
		}
	}
	return repoFromRow(updated, userFromRow(ownerRow)), nil
}

// RepoRedirect resolves a repository that used to live at owner/name, for the
// 301 the web front serves after a rename. The redirect table is only
// consulted after the direct lookup missed (the caller's GetRepo), and the
// target is gated by the same visibility rule, so a moved private repository
// never confirms its existence through a redirect.
func (s *RepoService) RepoRedirect(ctx context.Context, viewerPK int64, owner, name string) (*Repo, error) {
	row, err := s.store.RepoByRedirect(ctx, owner, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrRepoNotFound
	}
	if err != nil {
		return nil, err
	}
	if !canSee(row, viewerPK) {
		return nil, ErrRepoNotFound
	}
	ownerRow, err := s.store.UserByPK(ctx, row.OwnerPK)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrRepoNotFound
	}
	if err != nil {
		return nil, err
	}
	return repoFromRow(row, userFromRow(ownerRow)), nil
}

// ForkInput holds the caller-supplied options for forking a repository.
// Organization names the target account when the fork should land under an
// org the viewer administers; empty forks under the viewer. Name renames the
// fork; empty keeps the source's name. DefaultBranchOnly copies just the
// source's default branch instead of every ref.
type ForkInput struct {
	Organization      string
	Name              string
	DefaultBranchOnly bool
}

// ForkRepo forks src for the viewer: a new repository row marked as a fork of
// src plus a git-level copy of its refs and objects. Forking a repository the
// viewer already forked returns the existing fork rather than failing, the
// way GitHub answers a repeat fork with the same 202. A name collision with
// an unrelated repository is ErrRepoExists. The caller has already resolved
// src through the visibility gate, so no second check runs here.
func (s *RepoService) ForkRepo(ctx context.Context, viewerPK int64, src *Repo, inp ForkInput) (*Repo, error) {
	var ownerRow *store.UserRow
	var err error
	if inp.Organization != "" {
		ownerRow, err = s.store.UserByLogin(ctx, inp.Organization)
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrUserNotFound
		}
		if err != nil {
			return nil, err
		}
		if ownerRow.PK != viewerPK && !ownerRow.SiteAdmin {
			return nil, ErrForbidden
		}
	} else {
		ownerRow, err = s.store.UserByPK(ctx, viewerPK)
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrUserNotFound
		}
		if err != nil {
			return nil, err
		}
	}
	name := inp.Name
	if name == "" {
		name = src.Name
	}
	if existing, err := s.store.RepoByOwnerName(ctx, ownerRow.Login, name); err == nil {
		if existing.ForkOfPK != nil && *existing.ForkOfPK == src.PK {
			return repoFromRow(existing, userFromRow(ownerRow)), nil
		}
		return nil, ErrRepoExists
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	forkOf := src.PK
	row := &store.RepoRow{
		OwnerPK:       ownerRow.PK,
		Name:          name,
		Description:   src.Description,
		Homepage:      src.Homepage,
		Private:       src.Private,
		Fork:          true,
		DefaultBranch: src.DefaultBranch,
		ForkOfPK:      &forkOf,
		PushedAt:      src.PushedAt,
	}
	if err := s.store.InsertRepo(ctx, row); err != nil {
		return nil, err
	}
	if err := s.gitStore.ForkFrom(ctx, src.PK, row.PK, src.DefaultBranch, inp.DefaultBranchOnly); err != nil {
		return nil, err
	}
	return repoFromRow(row, userFromRow(ownerRow)), nil
}

// ListForks returns the live forks of repoPK the viewer can see, newest
// first, the order the forks endpoint serves by default.
func (s *RepoService) ListForks(ctx context.Context, viewerPK, repoPK int64) ([]*Repo, error) {
	rows, err := s.store.ForksOf(ctx, repoPK)
	if err != nil {
		return nil, err
	}
	out := make([]*Repo, 0, len(rows))
	for _, r := range rows {
		if !canSee(r, viewerPK) {
			continue
		}
		ownerRow, err := s.store.UserByPK(ctx, r.OwnerPK)
		if err != nil {
			continue
		}
		out = append(out, repoFromRow(r, userFromRow(ownerRow)))
	}
	return out, nil
}

// ForksCount reports how many live repositories were forked from repoPK. It
// backs network_count (and the fork counters) on the single-repository shape.
func (s *RepoService) ForksCount(ctx context.Context, repoPK int64) (int, error) {
	n, err := s.store.CountForks(ctx, repoPK)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// DeleteRepo soft-deletes the repository identified by owner/name. Only the
// repository owner may delete it.
func (s *RepoService) DeleteRepo(ctx context.Context, viewerPK int64, owner, name string) error {
	row, err := s.store.RepoByOwnerName(ctx, owner, name)
	if errors.Is(err, store.ErrNotFound) {
		return ErrRepoNotFound
	}
	if err != nil {
		return err
	}
	if row.OwnerPK != viewerPK {
		return ErrForbidden
	}
	return s.store.SoftDeleteRepo(ctx, row.PK)
}

// RepoForEvent assembles a repository by internal pk with no visibility check,
// the system path the webhook renderer loads an event's repository through. The
// event was already authorized when it was recorded, so the renderer does not
// re-gate it.
func (s *RepoService) RepoForEvent(ctx context.Context, repoPK int64) (*Repo, error) {
	row, err := s.store.RepoByPK(ctx, repoPK)
	if err != nil {
		return nil, err
	}
	ownerRow, err := s.store.UserByPK(ctx, row.OwnerPK)
	if err != nil {
		return nil, err
	}
	return repoFromRow(row, userFromRow(ownerRow)), nil
}

// DefaultBranchRef resolves the repository's head branch. It returns ErrEmptyRepo
// when the repository has no commits, which the caller renders as a null
// default_branch ref rather than an error.
func (s *RepoService) DefaultBranchRef(repo *Repo) (git.Branch, error) {
	gr, err := s.open(repo)
	if err != nil {
		return git.Branch{}, err
	}
	defer gr.Release()
	b, err := gr.HEAD()
	if err != nil {
		return git.Branch{}, gitErr(err)
	}
	return b, nil
}

// ListBranches lists the repository's branches in name order. An empty or
// uninitialized repository yields an empty slice, not an error.
func (s *RepoService) ListBranches(repo *Repo) ([]git.Branch, error) {
	gr, err := s.open(repo)
	if err != nil {
		if errors.Is(err, ErrEmptyRepo) {
			return []git.Branch{}, nil
		}
		return nil, err
	}
	defer gr.Release()
	bs, err := gr.Branches()
	if err != nil {
		return nil, gitErr(err)
	}
	return bs, nil
}

// GetBranch resolves a single branch by short name.
func (s *RepoService) GetBranch(repo *Repo, name string) (git.Branch, error) {
	gr, err := s.open(repo)
	if err != nil {
		return git.Branch{}, ErrGitNotFound
	}
	defer gr.Release()
	ref, err := gr.RefByName("heads/" + name)
	if err != nil {
		return git.Branch{}, ErrGitNotFound
	}
	return git.Branch{Name: name, Commit: ref.Target}, nil
}

// ListTags lists the repository's tags in name order. An empty or uninitialized
// repository yields an empty slice, not an error.
func (s *RepoService) ListTags(repo *Repo) ([]git.Tag, error) {
	gr, err := s.open(repo)
	if err != nil {
		if errors.Is(err, ErrEmptyRepo) {
			return []git.Tag{}, nil
		}
		return nil, err
	}
	defer gr.Release()
	ts, err := gr.Tags()
	if err != nil {
		return nil, gitErr(err)
	}
	return ts, nil
}

// ListRefs lists every branch and tag ref, fully qualified, in name order.
func (s *RepoService) ListRefs(repo *Repo) ([]git.Ref, error) {
	gr, err := s.open(repo)
	if err != nil {
		if errors.Is(err, ErrEmptyRepo) {
			return []git.Ref{}, nil
		}
		return nil, err
	}
	defer gr.Release()
	rs, err := gr.Refs()
	if err != nil {
		return nil, gitErr(err)
	}
	return rs, nil
}

// GetRef resolves a single reference. The name carries the suffix the REST API
// uses (heads/main, tags/v1.0) or is fully qualified.
func (s *RepoService) GetRef(repo *Repo, name string) (git.Ref, error) {
	gr, err := s.open(repo)
	if err != nil {
		return git.Ref{}, ErrGitNotFound
	}
	defer gr.Release()
	ref, err := gr.RefByName(name)
	if err != nil {
		return git.Ref{}, ErrGitNotFound
	}
	return ref, nil
}

// GetCommit loads a single commit by any revision (a sha, a branch or tag name,
// HEAD, or an expression like HEAD~2).
func (s *RepoService) GetCommit(repo *Repo, rev string) (git.Commit, error) {
	gr, err := s.open(repo)
	if err != nil {
		return git.Commit{}, ErrGitNotFound
	}
	defer gr.Release()
	c, err := gr.Commit(rev)
	if err != nil {
		return git.Commit{}, gitErr(err)
	}
	return c, nil
}

// ListCommits walks commit history from opts.From (defaulting to the head
// branch), optionally filtered to a path.
func (s *RepoService) ListCommits(repo *Repo, opts git.LogOpts) ([]git.Commit, error) {
	gr, err := s.open(repo)
	if err != nil {
		return nil, gitErr(err)
	}
	defer gr.Release()
	if opts.From == "" {
		opts.From = "HEAD"
	}
	cs, err := gr.Log(opts)
	if err != nil {
		return nil, gitErr(err)
	}
	return cs, nil
}

// LatestCommit returns the newest commit at rev touching path (the whole tree
// when path is empty). ok is false when nothing matches: an unborn ref, a bad
// revision, or a path with no history. It runs one bounded git log -1
// subprocess instead of an in-process history walk, so a tree page asking for
// its latest-commit bar does not pay for the repository's depth.
func (s *RepoService) LatestCommit(ctx context.Context, repo *Repo, rev, path string) (git.Commit, bool, error) {
	c, ok, err := s.gitStore.LastCommitForPath(ctx, repo.PK, rev, path)
	if err != nil {
		return git.Commit{}, false, gitErr(err)
	}
	return c, ok, nil
}

// GetTree loads a tree by any revision, optionally walking the whole subtree.
func (s *RepoService) GetTree(repo *Repo, rev string, recursive bool) (git.Tree, error) {
	gr, err := s.open(repo)
	if err != nil {
		return git.Tree{}, ErrGitNotFound
	}
	defer gr.Release()
	t, err := gr.Tree(rev, recursive)
	if err != nil {
		return git.Tree{}, gitErr(err)
	}
	return t, nil
}

// GetBlob loads a blob by its object id.
func (s *RepoService) GetBlob(repo *Repo, sha string) (git.Blob, error) {
	gr, err := s.open(repo)
	if err != nil {
		return git.Blob{}, ErrGitNotFound
	}
	defer gr.Release()
	b, err := gr.Blob(sha)
	if err != nil {
		return git.Blob{}, gitErr(err)
	}
	return b, nil
}

// Contents resolves a path at a ref. An empty ref reads the head commit. A blob
// yields a file result with content; a tree yields a directory listing.
func (s *RepoService) Contents(repo *Repo, path, ref string) (git.PathResult, error) {
	gr, err := s.open(repo)
	if err != nil {
		return git.PathResult{}, ErrGitNotFound
	}
	defer gr.Release()
	if ref == "" {
		ref = "HEAD"
	}
	res, err := gr.PathAt(ref, path)
	if err != nil {
		return git.PathResult{}, gitErr(err)
	}
	return res, nil
}

// WriteFileInput holds the inputs for the file-write domain operation.
type WriteFileInput struct {
	Path           string
	Content        []byte // nil for delete
	Message        string
	AuthorName     string
	AuthorEmail    string
	Branch         string
	CurrentBlobSHA string // must match current blob if non-empty
}

// WriteFileResult is returned by WriteFile and DeleteFile.
type WriteFileResult struct {
	CommitSHA string
	BlobSHA   string // empty on delete
}

// WriteFile creates or updates a file in the repository, creating a new commit
// on top of the branch. Returns ErrConflict if CurrentBlobSHA is set but does
// not match the actual current blob.
func (s *RepoService) WriteFile(repo *Repo, in WriteFileInput) (*WriteFileResult, error) {
	gr, err := s.openOrInit(repo)
	if err != nil {
		return nil, err
	}
	defer gr.Release()
	if err := checkCurrentBlob(gr, in); err != nil {
		return nil, err
	}
	res, err := gr.WriteFile(git.FileWriteInput{
		Path:        in.Path,
		Content:     in.Content,
		Message:     in.Message,
		AuthorName:  in.AuthorName,
		AuthorEmail: in.AuthorEmail,
		Branch:      in.Branch,
	})
	if err != nil {
		return nil, gitErr(err)
	}
	return &WriteFileResult{CommitSHA: res.CommitSHA, BlobSHA: res.BlobSHA}, nil
}

// DeleteFile removes a file from the repository, creating a new commit.
func (s *RepoService) DeleteFile(repo *Repo, in WriteFileInput) (*WriteFileResult, error) {
	gr, err := s.open(repo)
	if err != nil {
		return nil, err
	}
	defer gr.Release()
	if err := checkCurrentBlob(gr, in); err != nil {
		return nil, err
	}
	res, err := gr.DeleteFile(git.FileWriteInput{
		Path:        in.Path,
		Message:     in.Message,
		AuthorName:  in.AuthorName,
		AuthorEmail: in.AuthorEmail,
		Branch:      in.Branch,
	})
	if err != nil {
		return nil, gitErr(err)
	}
	return &WriteFileResult{CommitSHA: res.CommitSHA}, nil
}

// checkCurrentBlob enforces the compare-and-swap a caller asks for by setting
// CurrentBlobSHA: the path must hold a file whose blob is exactly that sha on
// the target branch. Any other state, a missing path included, is ErrConflict.
// An unset CurrentBlobSHA skips the check.
func checkCurrentBlob(gr *git.Repo, in WriteFileInput) error {
	if in.CurrentBlobSHA == "" {
		return nil
	}
	ref := in.Branch
	if ref == "" {
		ref = "HEAD"
	}
	cur, err := gr.PathAt(ref, in.Path)
	if err != nil || cur.IsDir || string(cur.Entry.SHA) != in.CurrentBlobSHA {
		return ErrConflict
	}
	return nil
}

// openOrInit opens the repository's bare git store, initializing it if it does
// not yet exist. Used by WriteFile so the first file create also works on a
// freshly-created (but never-pushed) repository.
func (s *RepoService) openOrInit(repo *Repo) (*git.Repo, error) {
	gr, err := s.gitStore.Open(repo.PK)
	if err == nil {
		return gr, nil
	}
	if errors.Is(err, git.ErrRepoNotFound) {
		return s.gitStore.Init(repo.PK)
	}
	return nil, err
}

// open opens the repository's bare git store. A repository whose bare store was
// never created is treated as empty, the same as one with no commits.
func (s *RepoService) open(repo *Repo) (*git.Repo, error) {
	gr, err := s.gitStore.Open(repo.PK)
	if errors.Is(err, git.ErrRepoNotFound) {
		return nil, ErrEmptyRepo
	}
	if err != nil {
		return nil, err
	}
	return gr, nil
}

// canSee reports whether the viewer may see the repository on ownership
// alone. Public repositories are visible to everyone; a private repository is
// visible to its owner. The point lookups go through viewerCanSee, which also
// admits collaborators; this cheap check keeps the list filters query-free.
func canSee(row *store.RepoRow, viewerPK int64) bool {
	return !row.Private || (viewerPK != 0 && viewerPK == row.OwnerPK)
}

// viewerCanSee is canSee plus the collaborator grants: a private repository
// is visible to anyone with a collaborator row, whatever the permission
// level.
func (s *RepoService) viewerCanSee(ctx context.Context, row *store.RepoRow, viewerPK int64) (bool, error) {
	if canSee(row, viewerPK) {
		return true, nil
	}
	if viewerPK == 0 {
		return false, nil
	}
	_, err := s.store.CollaboratorByRepo(ctx, row.PK, viewerPK)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// RepoPermission resolves the viewer's effective role on repo: "admin" for
// the owner, the collaborator grant otherwise (the legacy "read"/"write"
// names normalize to pull/push), and "pull" for any other viewer the
// repository is visible to. An empty role means no access.
func (s *RepoService) RepoPermission(ctx context.Context, viewerPK int64, repo *Repo) (string, error) {
	if viewerPK == 0 {
		return "", nil
	}
	if viewerPK == repo.OwnerPK {
		return "admin", nil
	}
	c, err := s.store.CollaboratorByRepo(ctx, repo.PK, viewerPK)
	if err == nil {
		switch c.Permission {
		case "read":
			return "pull", nil
		case "write":
			return "push", nil
		default:
			return c.Permission, nil
		}
	}
	if !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	if repo.Private {
		return "", nil
	}
	return "pull", nil
}

// gitErr maps the git layer's sentinels to the domain's. A never-initialized or
// commitless repository becomes ErrEmptyRepo; a missing ref, object, or path
// becomes ErrGitNotFound.
func gitErr(err error) error {
	switch {
	case errors.Is(err, git.ErrRepoNotFound), errors.Is(err, git.ErrEmptyRepository):
		return ErrEmptyRepo
	case errors.Is(err, git.ErrObjectNotFound), errors.Is(err, git.ErrPathNotFound):
		return ErrGitNotFound
	case errors.Is(err, git.ErrBlobTooLarge):
		return ErrBlobTooLarge
	default:
		return err
	}
}

// CompareResult is the three-dot comparison between two branches. Files and
// Commits are the unique changes from base to head; Additions, Deletions, and
// ChangedFiles are the aggregated line counts. Commits is capped at
// MaxCompareCommits; TotalCommits is the real size of the range, so a capped
// result still reports honest counts.
type CompareResult struct {
	Base         git.Branch
	Head         git.Branch
	MergeBase    git.SHA
	Commits      []git.Commit
	TotalCommits int
	Behind       int
	Files        []git.FileChange
	Additions    int
	Deletions    int
	ChangedFiles int

	// BaseCommit and MergeBaseCommit are the full commit objects the compare
	// endpoint renders as base_commit and merge_base_commit. MergeBaseCommit
	// is zero when the two ends share no history.
	BaseCommit      git.Commit
	MergeBaseCommit git.Commit
}

// MaxCompareCommits caps how many commits a comparison loads, GitHub's own
// compare ceiling. A compare across a release boundary can span tens of
// thousands of commits; listing every one before render is unbounded work for a
// page nobody scrolls to the end of.
const MaxCompareCommits = 250

// Compare resolves base and head as branch names and computes the three-dot
// comparison between them. ErrGitNotFound is returned when either branch does
// not exist in the repository. When the two branches share no common history, a
// CompareResult with empty Commits and Files is returned rather than an error.
//
// The cost is three git subprocesses in the common case: the merge base, then
// the commit list and the file diff in parallel (independent reads of immutable
// objects). The per-file counts ChangedFiles already carries supply the
// additions/deletions totals, so no separate diff --numstat runs. The commit
// list is capped at MaxCompareCommits; when the cap bites, one extra rev-list
// --count establishes the honest TotalCommits.
func (s *RepoService) Compare(ctx context.Context, repo *Repo, base, head string) (*CompareResult, error) {
	return s.compare(ctx, repo, base, head, false)
}

// CompareDirect is Compare over the two-dot form: the files are the diff
// between the two trees themselves rather than between head and the merge
// base, so changes only on the base side show up reversed. The commit list is
// the same base..head walk both forms share. Unrelated histories still
// produce the direct diff; only the merge base is absent.
func (s *RepoService) CompareDirect(ctx context.Context, repo *Repo, base, head string) (*CompareResult, error) {
	return s.compare(ctx, repo, base, head, true)
}

func (s *RepoService) compare(ctx context.Context, repo *Repo, base, head string, direct bool) (*CompareResult, error) {
	baseBranch, err := s.compareEnd(repo, base)
	if err != nil {
		return nil, ErrGitNotFound
	}
	headBranch, err := s.compareEnd(repo, head)
	if err != nil {
		return nil, ErrGitNotFound
	}
	mb, ok, err := s.gitStore.MergeBase(ctx, repo.PK, baseBranch.Commit, headBranch.Commit)
	if err != nil {
		return nil, err
	}
	baseCommit, err := s.GetCommit(repo, baseBranch.Commit)
	if err != nil {
		return nil, err
	}
	if !ok && !direct {
		// The three-dot diff is against the merge base; with no common history
		// there is nothing to diff. The direct form needs no ancestor, so it
		// proceeds without one.
		return &CompareResult{Base: baseBranch, Head: headBranch, BaseCommit: baseCommit}, nil
	}

	var (
		commits                     []git.Commit
		files                       []git.FileChange
		ahead, behind               int
		commitsErr, fdErr, countErr error
		wg                          sync.WaitGroup
	)
	wg.Add(3)
	go func() {
		defer wg.Done()
		// One past the cap so a result of exactly the cap is distinguishable
		// from a truncated one without a second subprocess in the common case.
		commits, commitsErr = s.gitStore.CommitsBetweenN(ctx, repo.PK, baseBranch.Commit, headBranch.Commit, MaxCompareCommits+1)
	}()
	go func() {
		defer wg.Done()
		if direct {
			files, fdErr = s.gitStore.ChangedFilesDirect(ctx, repo.PK, baseBranch.Commit, headBranch.Commit)
		} else {
			files, fdErr = s.gitStore.ChangedFiles(ctx, repo.PK, baseBranch.Commit, headBranch.Commit)
		}
	}()
	go func() {
		defer wg.Done()
		// behind_by has no cheaper source than the symmetric count, and when
		// the commit list is capped the honest ahead_by comes from here too.
		ahead, behind, countErr = s.gitStore.AheadBehind(ctx, repo.PK, baseBranch.Commit, headBranch.Commit)
	}()
	wg.Wait()
	if commitsErr != nil {
		return nil, commitsErr
	}
	if fdErr != nil {
		return nil, fdErr
	}
	if countErr != nil {
		return nil, countErr
	}

	total := ahead
	if len(commits) > MaxCompareCommits {
		commits = commits[len(commits)-MaxCompareCommits:]
	}

	var mergeBaseCommit git.Commit
	if ok {
		mergeBaseCommit, err = s.GetCommit(repo, mb)
		if err != nil {
			return nil, err
		}
	}

	var add, del int
	for i := range files {
		add += files[i].Additions
		del += files[i].Deletions
	}
	return &CompareResult{
		Base:            baseBranch,
		Head:            headBranch,
		MergeBase:       mb,
		Commits:         commits,
		TotalCommits:    total,
		Behind:          behind,
		Files:           files,
		Additions:       add,
		Deletions:       del,
		ChangedFiles:    len(files),
		BaseCommit:      baseCommit,
		MergeBaseCommit: mergeBaseCommit,
	}, nil
}

// compareEnd resolves one end of a comparison. GitHub accepts a branch, a tag,
// or a commit id on either side, so a name that is not a branch falls through
// to general rev resolution; the Branch wrapper just carries the name and the
// commit it landed on.
func (s *RepoService) compareEnd(repo *Repo, rev string) (git.Branch, error) {
	if b, err := s.GetBranch(repo, rev); err == nil {
		return b, nil
	}
	c, err := s.GetCommit(repo, rev)
	if err != nil {
		return git.Branch{}, ErrGitNotFound
	}
	return git.Branch{Name: rev, Commit: c.SHA}, nil
}

func repoFromRow(r *store.RepoRow, owner *User) *Repo {
	return &Repo{
		PK:              r.PK,
		OwnerPK:         r.OwnerPK,
		ID:              r.DBID,
		Owner:           owner,
		Name:            r.Name,
		Description:     r.Description,
		Homepage:        r.Homepage,
		Private:         r.Private,
		Fork:            r.Fork,
		DefaultBranch:   r.DefaultBranch,
		HasIssues:       r.HasIssues,
		HasProjects:     r.HasProjects,
		HasWiki:         r.HasWiki,
		HasDownloads:    r.HasDownloads,
		Archived:        r.Archived,
		Disabled:        r.Disabled,
		IsTemplate:      r.IsTemplate,
		OpenIssuesCount: r.OpenIssuesCount,
		PushedAt:        r.PushedAt,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
		Topics:          r.Topics,

		AllowSquashMerge:         r.AllowSquashMerge,
		AllowMergeCommit:         r.AllowMergeCommit,
		AllowRebaseMerge:         r.AllowRebaseMerge,
		AllowAutoMerge:           r.AllowAutoMerge,
		DeleteBranchOnMerge:      r.DeleteBranchOnMerge,
		AllowUpdateBranch:        r.AllowUpdateBranch,
		WebCommitSignoffRequired: r.WebCommitSignoffRequired,
		ForkOfPK:                 r.ForkOfPK,
	}
}

// Blame returns every source line of path at ref annotated with the commit
// that last changed it. It returns ErrGitNotFound when the path does not exist
// at the ref, and ErrEmptyRepo when the repository has no commits.
func (s *RepoService) Blame(repo *Repo, ref, path string) ([]git.BlameLine, error) {
	gr, err := s.open(repo)
	if err != nil {
		return nil, gitErr(err)
	}
	defer gr.Release()
	lines, err := gr.Blame(ref, path)
	if err != nil {
		return nil, gitErr(err)
	}
	return lines, nil
}

// CommitPatch returns the unified diff patch of sha against its first parent.
// For the initial commit (no parents) it returns an empty string. The caller
// renders it through the markup pipeline as a diff block.
func (s *RepoService) CommitPatch(repo *Repo, sha string) (string, error) {
	gr, err := s.open(repo)
	if err != nil {
		return "", gitErr(err)
	}
	defer gr.Release()
	patch, err := gr.CommitPatch(sha)
	if err != nil {
		return "", gitErr(err)
	}
	return patch, nil
}

// CommitDiff returns the commit's plain unified diff against its first parent,
// or against the empty tree for a root commit: the body /commit/{sha}.diff
// serves. Unlike CommitPatch it is one uncapped git diff subprocess, so the
// text endpoint never hands out a silently shortened diff.
func (s *RepoService) CommitDiff(ctx context.Context, repo *Repo, sha string) ([]byte, error) {
	gr, err := s.open(repo)
	if err != nil {
		return nil, gitErr(err)
	}
	commit, err := gr.Commit(sha)
	gr.Release()
	if err != nil {
		return nil, gitErr(err)
	}
	base := git.EmptyTreeSHA
	if len(commit.Parents) > 0 {
		base = commit.Parents[0]
	}
	out, err := s.gitStore.DiffDirect(ctx, repo.PK, base, commit.SHA)
	if err != nil {
		return nil, gitErr(err)
	}
	return out, nil
}

// CommitFormatPatch returns the commit as a format-patch mail, the body
// /commit/{sha}.patch serves.
func (s *RepoService) CommitFormatPatch(ctx context.Context, repo *Repo, sha string) ([]byte, error) {
	gr, err := s.open(repo)
	if err != nil {
		return nil, gitErr(err)
	}
	full, err := gr.ResolveCommit(sha)
	gr.Release()
	if err != nil {
		return nil, gitErr(err)
	}
	out, err := s.gitStore.FormatPatchCommit(ctx, repo.PK, full)
	if err != nil {
		return nil, gitErr(err)
	}
	return out, nil
}

// CompareDiff returns the raw unified diff of the compare range as text:
// head against the merge base in the canonical form, or the direct two-point
// diff when direct is set (the two-dot form). Both ends resolve as any
// commit-ish; an unresolvable end is ErrGitNotFound.
func (s *RepoService) CompareDiff(ctx context.Context, repo *Repo, base, head string, direct bool) ([]byte, error) {
	bsha, hsha, err := s.resolveEnds(repo, base, head)
	if err != nil {
		return nil, err
	}
	var out []byte
	if direct {
		out, err = s.gitStore.DiffDirect(ctx, repo.PK, bsha, hsha)
	} else {
		out, err = s.gitStore.DiffRaw(ctx, repo.PK, bsha, hsha)
	}
	if err != nil {
		return nil, gitErr(err)
	}
	return out, nil
}

// ComparePatch returns the compare range's own commits (base..head) as an
// mbox patch series, the body /compare/{basehead}.patch serves.
func (s *RepoService) ComparePatch(ctx context.Context, repo *Repo, base, head string) ([]byte, error) {
	bsha, hsha, err := s.resolveEnds(repo, base, head)
	if err != nil {
		return nil, err
	}
	out, err := s.gitStore.FormatPatch(ctx, repo.PK, bsha, hsha)
	if err != nil {
		return nil, gitErr(err)
	}
	return out, nil
}

// resolveEnds resolves the two ends of a compare range to commit ids in one
// repository open.
func (s *RepoService) resolveEnds(repo *Repo, base, head string) (git.SHA, git.SHA, error) {
	gr, err := s.open(repo)
	if err != nil {
		return "", "", gitErr(err)
	}
	defer gr.Release()
	bsha, err := gr.ResolveCommit(base)
	if err != nil {
		return "", "", gitErr(err)
	}
	hsha, err := gr.ResolveCommit(head)
	if err != nil {
		return "", "", gitErr(err)
	}
	return bsha, hsha, nil
}

// Archive streams an archive of the tree at ref to w as one git archive
// subprocess: format is "zip" or "tar", prefix the leading directory recorded
// for every entry. The revision resolves before anything streams, so a bad
// ref or an empty repository surfaces as ErrGitNotFound while the response
// can still say so.
func (s *RepoService) Archive(ctx context.Context, repo *Repo, ref, format, prefix string, w io.Writer) error {
	gr, err := s.open(repo)
	if err != nil {
		return gitErr(err)
	}
	sha, err := gr.ResolveCommit(ref)
	gr.Release()
	if err != nil {
		return gitErr(err)
	}
	if err := s.gitStore.ArchiveStream(ctx, repo.PK, format, prefix, sha, w); err != nil {
		return gitErr(err)
	}
	return nil
}

// CreateBlob stores a blob object in the repository and returns its SHA.
func (s *RepoService) CreateBlob(repo *Repo, content []byte) (*git.CreateBlobResult, error) {
	gr, err := s.openOrInit(repo)
	if err != nil {
		return nil, gitErr(err)
	}
	defer gr.Release()
	return gr.CreateBlob(git.CreateBlobInput{Content: content})
}

// CreateTree builds a new tree object in the repository.
func (s *RepoService) CreateTree(repo *Repo, baseTreeSHA string, entries []git.CreateTreeEntry) (*git.CreateTreeResult, error) {
	gr, err := s.openOrInit(repo)
	if err != nil {
		return nil, gitErr(err)
	}
	defer gr.Release()
	return gr.CreateTree(baseTreeSHA, entries)
}

// CreateGitCommit writes a new commit object to the repository without updating any branch.
func (s *RepoService) CreateGitCommit(repo *Repo, in git.CreateCommitInput) (*git.CreateCommitResult, error) {
	gr, err := s.openOrInit(repo)
	if err != nil {
		return nil, gitErr(err)
	}
	defer gr.Release()
	return gr.CreateCommit(in)
}

// CreateGitTag creates an annotated tag object in the repository.
func (s *RepoService) CreateGitTag(repo *Repo, in git.CreateTagInput) (*git.CreateTagResult, error) {
	gr, err := s.openOrInit(repo)
	if err != nil {
		return nil, gitErr(err)
	}
	defer gr.Release()
	return gr.CreateTag(in)
}

// GetGitTag reads an annotated tag object by its SHA.
func (s *RepoService) GetGitTag(repo *Repo, sha string) (*git.GetTagResult, error) {
	gr, err := s.open(repo)
	if err != nil {
		return nil, gitErr(err)
	}
	defer gr.Release()
	res, err := gr.GetTag(sha)
	if err != nil {
		return nil, gitErr(err)
	}
	return res, nil
}

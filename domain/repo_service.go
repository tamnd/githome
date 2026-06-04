package domain

import (
	"context"
	"errors"
	"time"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
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
)

// RepoStore is the slice of the store the repo service needs. The write path
// (the post-receive sink) adds the repo-by-pk lookup, the pushed_at touch, and
// the job enqueue; enqueuing through the store keeps the domain on its single
// store dependency rather than importing the worker package.
type RepoStore interface {
	RepoByOwnerName(ctx context.Context, owner, name string) (*store.RepoRow, error)
	RepoByPK(ctx context.Context, pk int64) (*store.RepoRow, error)
	UserByPK(ctx context.Context, pk int64) (*store.UserRow, error)
	TouchRepoPushedAt(ctx context.Context, pk int64, at time.Time) error
	EnqueueJob(ctx context.Context, j *store.JobRow) (bool, error)
}

// RepoService resolves repositories and reads their git data. It pairs the
// metadata store with the git object store: GetRepo authorizes and assembles
// the domain Repo, and the git-data methods open the repository's bare git
// store through the internal pk GetRepo carried.
type RepoService struct {
	store    RepoStore
	gitStore *git.Store
}

// NewRepoService builds a RepoService over the metadata store and the git store.
func NewRepoService(st RepoStore, gs *git.Store) *RepoService {
	return &RepoService{store: st, gitStore: gs}
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

// DefaultBranchRef resolves the repository's head branch. It returns ErrEmptyRepo
// when the repository has no commits, which the caller renders as a null
// default_branch ref rather than an error.
func (s *RepoService) DefaultBranchRef(repo *Repo) (git.Branch, error) {
	gr, err := s.open(repo)
	if err != nil {
		return git.Branch{}, err
	}
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
	if opts.From == "" {
		opts.From = "HEAD"
	}
	cs, err := gr.Log(opts)
	if err != nil {
		return nil, gitErr(err)
	}
	return cs, nil
}

// GetTree loads a tree by any revision, optionally walking the whole subtree.
func (s *RepoService) GetTree(repo *Repo, rev string, recursive bool) (git.Tree, error) {
	gr, err := s.open(repo)
	if err != nil {
		return git.Tree{}, ErrGitNotFound
	}
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
	if ref == "" {
		ref = "HEAD"
	}
	res, err := gr.PathAt(ref, path)
	if err != nil {
		return git.PathResult{}, gitErr(err)
	}
	return res, nil
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

// canSee reports whether the viewer may see the repository. Public repositories
// are visible to everyone; a private repository is visible only to its owner.
// Finer-grained collaborator and organization access arrives with its
// milestone.
func canSee(row *store.RepoRow, viewerPK int64) bool {
	return !row.Private || (viewerPK != 0 && viewerPK == row.OwnerPK)
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
	default:
		return err
	}
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
	}
}

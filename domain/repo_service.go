package domain

import (
	"context"
	"errors"
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
)

// RepoStore is the slice of the store the repo service needs. The write path
// (the post-receive sink) adds the repo-by-pk lookup, the pushed_at touch, and
// the job enqueue; enqueuing through the store keeps the domain on its single
// store dependency rather than importing the worker package.
type RepoStore interface {
	RepoByOwnerName(ctx context.Context, owner, name string) (*store.RepoRow, error)
	RepoByPK(ctx context.Context, pk int64) (*store.RepoRow, error)
	RepoByDBID(ctx context.Context, dbID int64) (*store.RepoRow, error)
	ReposByOwner(ctx context.Context, ownerPK int64) ([]*store.RepoRow, error)
	UserByPK(ctx context.Context, pk int64) (*store.UserRow, error)
	UserByLogin(ctx context.Context, login string) (*store.UserRow, error)
	InsertRepo(ctx context.Context, r *store.RepoRow) error
	UpdateRepo(ctx context.Context, pk int64, p store.RepoPatch) (*store.RepoRow, error)
	SoftDeleteRepo(ctx context.Context, pk int64) error
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
	if !canSee(row, viewerPK) {
		return nil, ErrRepoNotFound
	}
	ownerRow, err := s.store.UserByPK(ctx, row.OwnerPK)
	if err != nil {
		return nil, err
	}
	return repoFromRow(row, userFromRow(ownerRow)), nil
}

// RepoInput holds the caller-supplied fields for creating a new repository.
type RepoInput struct {
	Name          string
	Description   *string
	Homepage      *string
	Private       bool
	AutoInit      bool   // init with a README commit
	DefaultBranch string // default "main"
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
	if _, err := s.gitStore.Init(row.PK); err != nil {
		return nil, err
	}
	owner := userFromRow(ownerRow)
	return repoFromRow(row, owner), nil
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
	}
	updated, err := s.store.UpdateRepo(ctx, row.PK, sp)
	if err != nil {
		return nil, err
	}
	ownerRow, err := s.store.UserByPK(ctx, updated.OwnerPK)
	if err != nil {
		return nil, err
	}
	return repoFromRow(updated, userFromRow(ownerRow)), nil
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
	Files        []git.FileChange
	Additions    int
	Deletions    int
	ChangedFiles int
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
	baseBranch, err := s.GetBranch(repo, base)
	if err != nil {
		return nil, ErrGitNotFound
	}
	headBranch, err := s.GetBranch(repo, head)
	if err != nil {
		return nil, ErrGitNotFound
	}
	mb, ok, err := s.gitStore.MergeBase(ctx, repo.PK, baseBranch.Commit, headBranch.Commit)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &CompareResult{Base: baseBranch, Head: headBranch}, nil
	}

	var (
		commits           []git.Commit
		files             []git.FileChange
		commitsErr, fdErr error
		wg                sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		// One past the cap so a result of exactly the cap is distinguishable
		// from a truncated one without a second subprocess in the common case.
		commits, commitsErr = s.gitStore.CommitsBetweenN(ctx, repo.PK, baseBranch.Commit, headBranch.Commit, MaxCompareCommits+1)
	}()
	go func() {
		defer wg.Done()
		files, fdErr = s.gitStore.ChangedFiles(ctx, repo.PK, baseBranch.Commit, headBranch.Commit)
	}()
	wg.Wait()
	if commitsErr != nil {
		return nil, commitsErr
	}
	if fdErr != nil {
		return nil, fdErr
	}

	total := len(commits)
	if len(commits) > MaxCompareCommits {
		commits = commits[len(commits)-MaxCompareCommits:]
		if ahead, _, err := s.gitStore.AheadBehind(ctx, repo.PK, baseBranch.Commit, headBranch.Commit); err == nil {
			total = ahead
		}
	}

	var add, del int
	for i := range files {
		add += files[i].Additions
		del += files[i].Deletions
	}
	return &CompareResult{
		Base:         baseBranch,
		Head:         headBranch,
		MergeBase:    mb,
		Commits:      commits,
		TotalCommits: total,
		Files:        files,
		Additions:    add,
		Deletions:    del,
		ChangedFiles: len(files),
	}, nil
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

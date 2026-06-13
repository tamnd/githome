package domain

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/go-git/go-billy/v5/util"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
)

// fakeRepoStore is an in-memory RepoStore for the service tests. The git data
// lives in a real git.Store; only the metadata lookups are faked. The write
// path records the pushed_at touch and the enqueued jobs so the push-sink test
// can assert on them.
type fakeRepoStore struct {
	repos      map[string]*store.RepoRow
	users      map[int64]*store.UserRow
	redirects  map[string]int64
	pushedAt   map[int64]time.Time
	jobs       []store.JobRow
	events     []store.EventRow
	dedupeSeen map[string]bool
	// collaborators maps a [repoPK, userPK] pair to the stored permission so a
	// test can grant a non-owner a role and exercise the write gate.
	collaborators map[[2]int64]string
}

func (f *fakeRepoStore) InsertEvent(_ context.Context, e *store.EventRow) error {
	e.PK = int64(len(f.events) + 1)
	f.events = append(f.events, *e)
	return nil
}

func (f *fakeRepoStore) RepoByOwnerName(_ context.Context, owner, name string) (*store.RepoRow, error) {
	r, ok := f.repos[strings.ToLower(owner)+"/"+strings.ToLower(name)]
	if !ok {
		return nil, store.ErrNotFound
	}
	return r, nil
}

func (f *fakeRepoStore) RepoByPK(_ context.Context, pk int64) (*store.RepoRow, error) {
	for _, r := range f.repos {
		if r.PK == pk {
			return r, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeRepoStore) RepoByDBID(_ context.Context, dbID int64) (*store.RepoRow, error) {
	for _, r := range f.repos {
		if r.DBID == dbID {
			return r, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeRepoStore) RepoByRedirect(ctx context.Context, owner, name string) (*store.RepoRow, error) {
	pk, ok := f.redirects[strings.ToLower(owner)+"/"+strings.ToLower(name)]
	if !ok {
		return nil, store.ErrNotFound
	}
	return f.RepoByPK(ctx, pk)
}

func (f *fakeRepoStore) UpsertRepoRedirect(_ context.Context, oldOwner, oldName string, repoPK int64) error {
	if f.redirects == nil {
		f.redirects = map[string]int64{}
	}
	f.redirects[strings.ToLower(oldOwner)+"/"+strings.ToLower(oldName)] = repoPK
	return nil
}

func (f *fakeRepoStore) ReposByOwner(_ context.Context, ownerPK int64) ([]*store.RepoRow, error) {
	var out []*store.RepoRow
	for _, r := range f.repos {
		if r.OwnerPK == ownerPK {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeRepoStore) ListPublicRepos(_ context.Context, sinceDBID int64, limit int) ([]*store.RepoRow, error) {
	var out []*store.RepoRow
	for _, r := range f.repos {
		if r.DBID > sinceDBID && !r.Private {
			out = append(out, r)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeRepoStore) UserByPK(_ context.Context, pk int64) (*store.UserRow, error) {
	u, ok := f.users[pk]
	if !ok {
		return nil, store.ErrNotFound
	}
	return u, nil
}

func (f *fakeRepoStore) TouchRepoPushedAt(_ context.Context, pk int64, at time.Time) error {
	if f.pushedAt == nil {
		f.pushedAt = map[int64]time.Time{}
	}
	f.pushedAt[pk] = at
	return nil
}

func (f *fakeRepoStore) UserByLogin(_ context.Context, login string) (*store.UserRow, error) {
	for _, u := range f.users {
		if strings.EqualFold(u.Login, login) {
			return u, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeRepoStore) InsertRepo(_ context.Context, r *store.RepoRow) error {
	key := strings.ToLower(r.Name)
	if _, ok := f.repos[key]; ok {
		return store.ErrNotFound
	}
	r.PK = int64(len(f.repos) + 100)
	r.DBID = r.PK * 10
	f.repos[key] = r
	return nil
}

func (f *fakeRepoStore) UpdateRepo(_ context.Context, pk int64, p store.RepoPatch) (*store.RepoRow, error) {
	for key, r := range f.repos {
		if r.PK == pk {
			if p.Name != nil {
				r.Name = *p.Name
				// Rekey the owner/name index the way the real store's
				// case-insensitive lookup would re-resolve it.
				if owner, ok := f.users[r.OwnerPK]; ok {
					delete(f.repos, key)
					f.repos[strings.ToLower(owner.Login)+"/"+strings.ToLower(r.Name)] = r
				}
			}
			if p.Description != nil {
				r.Description = p.Description
			}
			if p.Homepage != nil {
				r.Homepage = p.Homepage
			}
			if p.DefaultBranch != nil {
				r.DefaultBranch = *p.DefaultBranch
			}
			if p.Private != nil {
				r.Private = *p.Private
			}
			if p.HasIssues != nil {
				r.HasIssues = *p.HasIssues
			}
			if p.HasProjects != nil {
				r.HasProjects = *p.HasProjects
			}
			if p.HasWiki != nil {
				r.HasWiki = *p.HasWiki
			}
			if p.Archived != nil {
				r.Archived = *p.Archived
			}
			if p.IsTemplate != nil {
				r.IsTemplate = *p.IsTemplate
			}
			if p.AllowSquashMerge != nil {
				r.AllowSquashMerge = *p.AllowSquashMerge
			}
			if p.AllowMergeCommit != nil {
				r.AllowMergeCommit = *p.AllowMergeCommit
			}
			if p.AllowRebaseMerge != nil {
				r.AllowRebaseMerge = *p.AllowRebaseMerge
			}
			if p.AllowAutoMerge != nil {
				r.AllowAutoMerge = *p.AllowAutoMerge
			}
			if p.DeleteBranchOnMerge != nil {
				r.DeleteBranchOnMerge = *p.DeleteBranchOnMerge
			}
			if p.AllowUpdateBranch != nil {
				r.AllowUpdateBranch = *p.AllowUpdateBranch
			}
			if p.WebCommitSignoffRequired != nil {
				r.WebCommitSignoffRequired = *p.WebCommitSignoffRequired
			}
			return r, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeRepoStore) CollaboratorByRepo(_ context.Context, repoPK, userPK int64) (*store.CollaboratorRow, error) {
	if perm, ok := f.collaborators[[2]int64{repoPK, userPK}]; ok {
		return &store.CollaboratorRow{RepoPK: repoPK, UserPK: userPK, Permission: perm}, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeRepoStore) CountForks(_ context.Context, pk int64) (int64, error) {
	var n int64
	for _, r := range f.repos {
		if r.ForkOfPK != nil && *r.ForkOfPK == pk {
			n++
		}
	}
	return n, nil
}

func (f *fakeRepoStore) ForksOf(_ context.Context, pk int64) ([]*store.RepoRow, error) {
	var out []*store.RepoRow
	for _, r := range f.repos {
		if r.ForkOfPK != nil && *r.ForkOfPK == pk {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeRepoStore) ReposByCollaborator(_ context.Context, _ int64) ([]*store.RepoRow, error) {
	return nil, nil
}

func (f *fakeRepoStore) ReposByTeamMember(_ context.Context, _ int64) ([]*store.RepoRow, error) {
	return nil, nil
}

func (f *fakeRepoStore) SoftDeleteRepo(_ context.Context, pk int64) error {
	for key, r := range f.repos {
		if r.PK == pk {
			delete(f.repos, key)
			return nil
		}
	}
	return store.ErrNotFound
}

func (f *fakeRepoStore) EnqueueJob(_ context.Context, j *store.JobRow) (bool, error) {
	if j.DedupeKey != "" {
		if f.dedupeSeen[j.DedupeKey] {
			return true, nil
		}
		if f.dedupeSeen == nil {
			f.dedupeSeen = map[string]bool{}
		}
		f.dedupeSeen[j.DedupeKey] = true
	}
	f.jobs = append(f.jobs, *j)
	return false, nil
}

// commitInto initializes a worktree repository at dir and writes one commit so
// the read methods have a head, tree, and blob to resolve. git.Store.Open works
// the same on the resulting repository as on a bare one.
func commitInto(t *testing.T, dir string) {
	t.Helper()
	r, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if err := util.WriteFile(wt.Filesystem, "README.md", []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	sig := &object.Signature{Name: "Octo Cat", Email: "octo@example.com", When: when}
	if _, err := wt.Commit("initial commit", &gogit.CommitOptions{Author: sig, Committer: sig}); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// newFixture wires a RepoService over a fake metadata store and a real git store.
// It seeds one owner (octocat) and three repositories: a populated public repo,
// a private repo, and an empty (initialized but commitless) repo.
func newFixture(t *testing.T) (*RepoService, *fakeRepoStore) {
	t.Helper()
	gs := git.NewStore(t.TempDir())

	const ownerPK = int64(10)
	users := map[int64]*store.UserRow{
		ownerPK: {PK: ownerPK, DBID: 100, Login: "octocat", Type: "User"},
	}
	repos := map[string]*store.RepoRow{
		"octocat/hello":  {PK: 5, DBID: 50, OwnerPK: ownerPK, Name: "hello", DefaultBranch: "master"},
		"octocat/secret": {PK: 6, DBID: 60, OwnerPK: ownerPK, Name: "secret", Private: true, DefaultBranch: "master"},
		"octocat/blank":  {PK: 7, DBID: 70, OwnerPK: ownerPK, Name: "blank", DefaultBranch: "main"},
	}
	commitInto(t, gs.Dir(5))
	commitInto(t, gs.Dir(6))
	if _, err := gs.Init(7); err != nil {
		t.Fatalf("Init blank: %v", err)
	}

	st := &fakeRepoStore{repos: repos, users: users}
	return NewRepoService(st, gs), st
}

func TestGetRepoVisibility(t *testing.T) {
	svc, _ := newFixture(t)
	ctx := context.Background()

	repo, err := svc.GetRepo(ctx, 0, "OctoCat", "Hello")
	if err != nil {
		t.Fatalf("public GetRepo: %v", err)
	}
	if repo.ID != 50 || repo.FullName() != "octocat/hello" || repo.Owner.Login != "octocat" {
		t.Fatalf("public repo = %+v owner=%+v", repo, repo.Owner)
	}

	if _, err := svc.GetRepo(ctx, 0, "octocat", "nope"); !errors.Is(err, ErrRepoNotFound) {
		t.Errorf("missing repo err = %v, want ErrRepoNotFound", err)
	}

	// A private repo is invisible to anonymous and to other users, and reported
	// as not found rather than forbidden.
	if _, err := svc.GetRepo(ctx, 0, "octocat", "secret"); !errors.Is(err, ErrRepoNotFound) {
		t.Errorf("private repo (anon) err = %v, want ErrRepoNotFound", err)
	}
	if _, err := svc.GetRepo(ctx, 99, "octocat", "secret"); !errors.Is(err, ErrRepoNotFound) {
		t.Errorf("private repo (other) err = %v, want ErrRepoNotFound", err)
	}
	if _, err := svc.GetRepo(ctx, 10, "octocat", "secret"); err != nil {
		t.Errorf("private repo (owner) err = %v, want visible", err)
	}
}

func TestRenameLeavesRedirect(t *testing.T) {
	svc, st := newFixture(t)
	ctx := context.Background()

	// The owner renames hello to greetings: the old name must keep resolving
	// through RepoRedirect, pointing at the repository under its new name.
	newName := "greetings"
	if _, err := svc.UpdateRepo(ctx, 10, "octocat", "hello", RepoPatch{Name: &newName}); err != nil {
		t.Fatalf("UpdateRepo rename: %v", err)
	}
	moved, err := svc.RepoRedirect(ctx, 0, "octocat", "hello")
	if err != nil {
		t.Fatalf("RepoRedirect: %v", err)
	}
	if moved.Name != "greetings" || moved.Owner.Login != "octocat" {
		t.Fatalf("redirect resolved to %s/%s, want octocat/greetings", moved.Owner.Login, moved.Name)
	}

	// A case-only rename records nothing: the direct lookup still hits at any
	// casing, so a redirect row would only shadow future name reuse.
	cased := "Greetings"
	if _, err := svc.UpdateRepo(ctx, 10, "octocat", "greetings", RepoPatch{Name: &cased}); err != nil {
		t.Fatalf("UpdateRepo case-only rename: %v", err)
	}
	if _, ok := st.redirects["octocat/greetings"]; ok {
		t.Error("case-only rename recorded a redirect")
	}

	// A redirect to a private repository stays invisible to other viewers:
	// not found, never a confirming 301.
	secretName := "classified"
	if _, err := svc.UpdateRepo(ctx, 10, "octocat", "secret", RepoPatch{Name: &secretName}); err != nil {
		t.Fatalf("UpdateRepo rename secret: %v", err)
	}
	if _, err := svc.RepoRedirect(ctx, 0, "octocat", "secret"); !errors.Is(err, ErrRepoNotFound) {
		t.Errorf("private redirect (anon) err = %v, want ErrRepoNotFound", err)
	}
	if moved, err := svc.RepoRedirect(ctx, 10, "octocat", "secret"); err != nil || moved.Name != "classified" {
		t.Errorf("private redirect (owner) = %v, %v, want classified", moved, err)
	}

	// An unknown old name is a plain miss.
	if _, err := svc.RepoRedirect(ctx, 0, "octocat", "never-existed"); !errors.Is(err, ErrRepoNotFound) {
		t.Errorf("unknown redirect err = %v, want ErrRepoNotFound", err)
	}
}

func TestReadsOnPopulatedRepo(t *testing.T) {
	svc, _ := newFixture(t)
	repo, err := svc.GetRepo(context.Background(), 0, "octocat", "hello")
	if err != nil {
		t.Fatal(err)
	}

	head, err := svc.DefaultBranchRef(repo)
	if err != nil {
		t.Fatalf("DefaultBranchRef: %v", err)
	}
	branches, err := svc.ListBranches(repo)
	if err != nil || len(branches) != 1 || branches[0].Name != head.Name {
		t.Fatalf("ListBranches = %+v, %v (head %+v)", branches, err, head)
	}

	c, err := svc.GetCommit(repo, "HEAD")
	if err != nil || c.SHA != head.Commit || c.Message != "initial commit" {
		t.Fatalf("GetCommit = %+v, %v", c, err)
	}

	res, err := svc.Contents(repo, "README.md", "")
	if err != nil {
		t.Fatalf("Contents: %v", err)
	}
	if res.IsDir || res.File == nil || string(res.File.Content) != "# Hello\n" {
		t.Fatalf("Contents = %+v", res)
	}

	blob, err := svc.GetBlob(repo, res.Entry.SHA)
	if err != nil || string(blob.Content) != "# Hello\n" {
		t.Fatalf("GetBlob = %+v, %v", blob, err)
	}
}

func TestReadsOnEmptyRepo(t *testing.T) {
	svc, _ := newFixture(t)
	repo, err := svc.GetRepo(context.Background(), 0, "octocat", "blank")
	if err != nil {
		t.Fatal(err)
	}

	branches, err := svc.ListBranches(repo)
	if err != nil || len(branches) != 0 {
		t.Errorf("ListBranches on empty = %+v, %v", branches, err)
	}
	tags, err := svc.ListTags(repo)
	if err != nil || len(tags) != 0 {
		t.Errorf("ListTags on empty = %+v, %v", tags, err)
	}
	if _, err := svc.DefaultBranchRef(repo); !errors.Is(err, ErrEmptyRepo) {
		t.Errorf("DefaultBranchRef on empty err = %v, want ErrEmptyRepo", err)
	}
	if _, err := svc.GetCommit(repo, "HEAD"); !errors.Is(err, ErrEmptyRepo) {
		t.Errorf("GetCommit on empty err = %v, want ErrEmptyRepo", err)
	}
	if _, err := svc.Contents(repo, "README.md", ""); !errors.Is(err, ErrEmptyRepo) {
		t.Errorf("Contents on empty err = %v, want ErrEmptyRepo", err)
	}
	// A blob lookup by id resolves no object: that is a not-found, not emptiness.
	if _, err := svc.GetBlob(repo, "0123456789abcdef0123456789abcdef01234567"); !errors.Is(err, ErrGitNotFound) {
		t.Errorf("GetBlob(missing) err = %v, want ErrGitNotFound", err)
	}
}

package presenter

import (
	"encoding/json"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/restmodel"
)

// Repository renders the full repository object. It is pure: the same domain
// repo, URL config, node-id format, and permissions always produce the same
// bytes. perm is the actor's effective access, or nil to omit the permissions
// block (anonymous requests). Language and license are always null for now;
// counters Githome does not track yet report zero.
func (b *URLBuilder) Repository(r *domain.Repo, format nodeid.Format, perm *restmodel.RepoPermissions) restmodel.Repository {
	owner := r.Owner.Login
	base := b.RepoAPI(owner, r.Name)

	out := restmodel.Repository{
		ID:       r.ID,
		NodeID:   nodeid.Encode(nodeid.KindRepository, r.ID, format),
		Name:     r.Name,
		FullName: r.FullName(),
		Owner:    b.SimpleUser(r.Owner, format),
		Private:  r.Private,
		HTMLURL:  b.RepoHTML(owner, r.Name),
		Desc:     r.Description,
		Fork:     r.Fork,
		URL:      base,

		ForksURL:         base + "/forks",
		KeysURL:          base + "/keys{/key_id}",
		CollaboratorsURL: base + "/collaborators{/collaborator}",
		TeamsURL:         base + "/teams",
		HooksURL:         base + "/hooks",
		IssueEventsURL:   base + "/issues/events{/number}",
		EventsURL:        base + "/events",
		AssigneesURL:     base + "/assignees{/user}",
		BranchesURL:      base + "/branches{/branch}",
		TagsURL:          base + "/tags",
		BlobsURL:         base + "/git/blobs{/sha}",
		GitTagsURL:       base + "/git/tags{/sha}",
		GitRefsURL:       base + "/git/refs{/sha}",
		TreesURL:         base + "/git/trees{/sha}",
		StatusesURL:      base + "/statuses/{sha}",
		LanguagesURL:     base + "/languages",
		StargazersURL:    base + "/stargazers",
		ContributorsURL:  base + "/contributors",
		SubscribersURL:   base + "/subscribers",
		SubscriptionURL:  base + "/subscription",
		CommitsURL:       base + "/commits{/sha}",
		GitCommitsURL:    base + "/git/commits{/sha}",
		CommentsURL:      base + "/comments{/number}",
		IssueCommentURL:  base + "/issues/comments{/number}",
		ContentsURL:      base + "/contents/{+path}",
		CompareURL:       base + "/compare/{base}...{head}",
		MergesURL:        base + "/merges",
		ArchiveURL:       base + "/{archive_format}{/ref}",
		DownloadsURL:     base + "/downloads",
		IssuesURL:        base + "/issues{/number}",
		PullsURL:         base + "/pulls{/number}",
		MilestonesURL:    base + "/milestones{/number}",
		NotificationsURL: base + "/notifications{?since,all,participating}",
		LabelsURL:        base + "/labels{/name}",
		ReleasesURL:      base + "/releases{/id}",
		DeploymentsURL:   base + "/deployments",

		CreatedAt: restmodel.NewTime(r.CreatedAt),
		UpdatedAt: restmodel.NewTime(r.UpdatedAt),

		GitURL:   b.RepoGitProto(owner, r.Name),
		SSHURL:   b.RepoGitSSH(owner, r.Name),
		CloneURL: b.RepoGitHTTP(owner, r.Name),
		SVNURL:   b.RepoHTML(owner, r.Name),

		Homepage:        r.Homepage,
		Size:            0,
		StargazersCount: 0,
		WatchersCount:   0,
		Language:        nil,

		HasIssues:      r.HasIssues,
		HasProjects:    r.HasProjects,
		HasDownloads:   r.HasDownloads,
		HasWiki:        r.HasWiki,
		HasPages:       false,
		HasDiscussions: false,

		ForksCount:      0,
		MirrorURL:       nil,
		Archived:        r.Archived,
		Disabled:        r.Disabled,
		OpenIssuesCount: r.OpenIssuesCount,
		License:         nil,

		AllowForking:             true,
		IsTemplate:               r.IsTemplate,
		WebCommitSignoffRequired: r.WebCommitSignoffRequired,
		Topics:                   []string{},
		Visibility:               visibility(r.Private),

		Forks:         0,
		OpenIssues:    r.OpenIssuesCount,
		Watchers:      0,
		DefaultBranch: r.DefaultBranch,

		Permissions: perm,
	}
	if r.PushedAt != nil {
		t := restmodel.NewTime(*r.PushedAt)
		out.PushedAt = &t
	}
	return out
}

// RepoDetail carries the extras only the single-repository responses render:
// the fork counts, the organization block, and the resolved fork parent and
// source. The handler assembles it; the presenter stays pure.
type RepoDetail struct {
	NetworkCount     int
	SubscribersCount int
	Organization     *domain.User
	Parent           *restmodel.Repository
	Source           *restmodel.Repository
}

// RepositoryFull renders the single-repository shape: the list shape plus the
// always-null temp_clone_token, the network and subscriber counts, the
// organization for org-owned repos, the fork parent/source, and, when the
// caller administers the repository, the merge-policy settings. GitHub scopes
// the merge settings to admins, so they key off perm.Admin.
func (b *URLBuilder) RepositoryFull(r *domain.Repo, format nodeid.Format, perm *restmodel.RepoPermissions, det RepoDetail) restmodel.Repository {
	out := b.Repository(r, format, perm)
	out.TempCloneToken = json.RawMessage("null")

	network, subs := det.NetworkCount, det.SubscribersCount
	out.NetworkCount = &network
	out.SubscribersCount = &subs
	out.ForksCount = network
	out.Forks = network

	if det.Organization != nil {
		org := b.SimpleUser(det.Organization, format)
		out.Organization = &org
	}
	out.Parent = det.Parent
	out.Source = det.Source

	if perm != nil && perm.Admin {
		squash, mergeCommit := r.AllowSquashMerge, r.AllowMergeCommit
		rebase, autoMerge := r.AllowRebaseMerge, r.AllowAutoMerge
		deleteBranch, updateBranch := r.DeleteBranchOnMerge, r.AllowUpdateBranch
		out.AllowSquashMerge = &squash
		out.AllowMergeCommit = &mergeCommit
		out.AllowRebaseMerge = &rebase
		out.AllowAutoMerge = &autoMerge
		out.DeleteBranchOnMerge = &deleteBranch
		out.AllowUpdateBranch = &updateBranch
	}
	return out
}

// OwnerPermissions is the all-true permission block GitHub returns for a
// repository's owner or an admin.
func OwnerPermissions() *restmodel.RepoPermissions {
	return &restmodel.RepoPermissions{Admin: true, Maintain: true, Push: true, Triage: true, Pull: true}
}

// ReadPermissions is the pull-only block for an authenticated user with read
// access to a repository they do not administer.
func ReadPermissions() *restmodel.RepoPermissions {
	return &restmodel.RepoPermissions{Pull: true}
}

// visibility maps the private flag to GitHub's visibility string.
func visibility(private bool) string {
	if private {
		return "private"
	}
	return "public"
}

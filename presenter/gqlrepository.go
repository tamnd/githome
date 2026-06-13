package presenter

import (
	"encoding/json"
	"strings"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/gqlmodel"
)

// GQLRepository renders a domain repository into the GraphQL Repository shape.
// branch is the resolved default-branch ref, or nil for a repository with no
// commits, in which case defaultBranchRef and pushedAt come back null. format
// selects the node-ID encoding.
func (b *URLBuilder) GQLRepository(r *domain.Repo, branch *git.Branch, format nodeid.Format) gqlmodel.Repository {
	perm := gqlmodel.RepositoryPermissionAdmin
	dbID := int32(r.ID)
	repo := gqlmodel.Repository{
		ID:                  nodeid.Encode(nodeid.KindRepository, r.ID, format),
		Name:                r.Name,
		NameWithOwner:       r.FullName(),
		Description:         r.Description,
		IsPrivate:           r.Private,
		IsFork:              r.Fork,
		IsArchived:          r.Archived,
		IsEmpty:             r.PushedAt == nil,
		IsInOrganization:    false, // Githome does not yet model organizations
		ForkCount:           0,     // not stored
		StargazerCount:      0,     // not stored
		HomepageURL:         gqlHomepageURL(r.Homepage),
		CreatedAt:           gqlmodel.NewDateTime(r.CreatedAt),
		UpdatedAt:           gqlmodel.NewDateTime(r.UpdatedAt),
		URL:                 gqlmodel.URI(b.RepoHTML(r.Owner.Login, r.Name)),
		SSHURL:              gqlmodel.GitSSHRemote(b.RepoGitSSH(r.Owner.Login, r.Name)),
		DatabaseID:          &dbID,
		Visibility:          gqlVisibility(r.Private),
		ViewerPermission:    &perm, // all authenticated users get ADMIN on their own repos
		ViewerCanAdminister: true,
		ViewerDefaultMergeMethod: gqlmodel.PullRequestMergeMethodMerge,
		HasIssuesEnabled:    r.HasIssues,
		HasWikiEnabled:      r.HasWiki,
		HasProjectsEnabled:  r.HasProjects,
		HasDiscussionsEnabled: false, // discussions are not modeled
		IsTemplate:          r.IsTemplate,
		IsMirror:            false, // Githome does not mirror
		DeleteBranchOnMerge: r.DeleteBranchOnMerge,
		AutoMergeAllowed:    true,
		MergeCommitAllowed:  true,
		SquashMergeAllowed:  true,
		RebaseMergeAllowed:  true,
		RepositoryTopics:    b.gqlRepositoryTopics(r),
		Watchers:            &gqlmodel.UserConnection{Nodes: []*gqlmodel.User{}, TotalCount: 0},
		Languages:           &gqlmodel.LanguageConnection{Nodes: []*gqlmodel.Language{}, Edges: []*gqlmodel.LanguageEdge{}, PageInfo: &gqlmodel.PageInfo{}, TotalCount: 0, TotalSize: 0},
		RepoOwner:           r.Owner.Login,
		RepoName:            r.Name,
		ForkParentPK:        r.ForkOfPK,
	}
	if r.PushedAt != nil {
		pushed := gqlmodel.NewDateTime(*r.PushedAt)
		repo.PushedAt = &pushed
	}
	if branch != nil {
		repo.DefaultBranchRef = GQLRef(r.ID, "refs/heads/"+branch.Name, branch.Name, branch.Commit)
	}
	return repo
}

// gqlVisibility maps a repository's private flag to GitHub's RepositoryVisibility
// enum. Githome does not model the INTERNAL tier (it has no enterprise org
// scope), so a repository is either PUBLIC or PRIVATE.
func gqlVisibility(private bool) gqlmodel.RepositoryVisibility {
	if private {
		return gqlmodel.RepositoryVisibilityPrivate
	}
	return gqlmodel.RepositoryVisibilityPublic
}

// gqlRepositoryTopics parses the repository's stored topic list (a JSON array)
// into a RepositoryTopicConnection. A repository with no topics yields an empty
// connection, never null, matching the non-null schema field.
func (b *URLBuilder) gqlRepositoryTopics(r *domain.Repo) *gqlmodel.RepositoryTopicConnection {
	conn := &gqlmodel.RepositoryTopicConnection{
		Nodes:    []*gqlmodel.RepositoryTopic{},
		PageInfo: &gqlmodel.PageInfo{},
	}
	var names []string
	if r.Topics != "" {
		_ = json.Unmarshal([]byte(r.Topics), &names)
	}
	for _, name := range names {
		if name == "" {
			continue
		}
		conn.Nodes = append(conn.Nodes, &gqlmodel.RepositoryTopic{
			ID:    "RT_" + r.Owner.Login + "/" + r.Name + "/" + name,
			Topic: &gqlmodel.Topic{ID: "T_" + name, Name: name},
			URL:   gqlmodel.URI(b.HTML("topics", name)),
		})
	}
	conn.TotalCount = int32(len(conn.Nodes))
	return conn
}

// GQLRef renders a git reference into the GraphQL Ref shape. repoID is the
// repository's public database ID used to encode the node ID, the same id the
// REST presenter encodes, so a node_id from either API names the same ref;
// qualifiedName is the full ref path (refs/heads/main); shortName is the bare
// ref name (main); sha is the target commit's SHA.
func GQLRef(repoID int64, qualifiedName, shortName, sha string) *gqlmodel.Ref {
	prefix := ""
	if i := strings.LastIndex(qualifiedName, "/"); i >= 0 {
		prefix = qualifiedName[:i+1]
	}
	ref := &gqlmodel.Ref{
		ID:     nodeid.EncodeGitObject("ref", repoID, qualifiedName),
		Name:   shortName,
		Prefix: prefix,
	}
	if sha != "" {
		ref.Target = &gqlmodel.Commit{
			ID:  nodeid.EncodeGitObject("commit", repoID, sha),
			Oid: gqlmodel.GitObjectID(sha),
		}
	}
	return ref
}

// GQLUser renders a domain user into the GraphQL User shape.
func (b *URLBuilder) GQLUser(u *domain.User, format nodeid.Format) *gqlmodel.User {
	if u == nil {
		return nil
	}
	dbID := int32(u.ID)
	out := &gqlmodel.User{
		ID:           nodeid.Encode(nodeid.KindUser, u.ID, format),
		Login:        u.Login,
		Name:         u.Name,
		Email:        u.Email,
		Bio:          u.Bio,
		DatabaseID:   &dbID,
		URL:          gqlmodel.URI(b.UserHTML(u.Login)),
		AvatarURL:    gqlmodel.URI(b.HTML("avatars", "u", int64str(u.ID))),
		ResourcePath: gqlmodel.URI("/" + u.Login),
		CreatedAt:    gqlmodel.NewDateTime(u.CreatedAt),
		UpdatedAt:    gqlmodel.NewDateTime(u.UpdatedAt),
	}
	return out
}

// GQLUserConnection renders a slice of domain users into a UserConnection.
func (b *URLBuilder) GQLUserConnection(users []*domain.User, format nodeid.Format) *gqlmodel.UserConnection {
	nodes := make([]*gqlmodel.User, 0, len(users))
	for _, u := range users {
		if n := b.GQLUser(u, format); n != nil {
			nodes = append(nodes, n)
		}
	}
	return &gqlmodel.UserConnection{Nodes: nodes, TotalCount: int32(len(nodes))}
}

// GQLMilestone renders a domain milestone into the GraphQL Milestone shape.
func (b *URLBuilder) GQLMilestone(owner, repo string, m *domain.Milestone, format nodeid.Format) *gqlmodel.Milestone {
	if m == nil {
		return nil
	}
	out := &gqlmodel.Milestone{
		ID:          nodeid.Encode(nodeid.KindMilestone, m.ID, format),
		Number:      int32(m.Number),
		Title:       m.Title,
		Description: m.Description,
		State:       m.State,
		URL:         gqlmodel.URI(b.RepoHTML(owner, repo) + "/milestone/" + int64str(m.Number)),
	}
	if m.DueOn != nil {
		due := gqlmodel.NewDateTime(*m.DueOn)
		out.DueOn = &due
	}
	return out
}

// GQLRelease renders a domain release into the GraphQL Release shape. isLatest
// is whether this is the repository's most recent published release.
func (b *URLBuilder) GQLRelease(owner, repo string, rel *domain.Release, isLatest bool, format nodeid.Format) *gqlmodel.Release {
	if rel == nil {
		return nil
	}
	tagPath := rel.TagName
	out := &gqlmodel.Release{
		ID:           nodeid.Encode(nodeid.KindRelease, rel.ID, format),
		Name:         rel.Name,
		TagName:      rel.TagName,
		URL:          gqlmodel.URI(b.RepoHTML(owner, repo) + "/releases/tag/" + tagPath),
		CreatedAt:    gqlmodel.NewDateTime(rel.CreatedAt),
		IsLatest:     isLatest,
		IsPrerelease: rel.Prerelease,
		IsDraft:      rel.Draft,
	}
	if rel.PublishedAt != nil {
		pub := gqlmodel.NewDateTime(*rel.PublishedAt)
		out.PublishedAt = &pub
	}
	return out
}

// GQLRepositoryOwner renders a repository's owner into the GraphQL
// RepositoryOwner shape. The concrete value is always a *gqlmodel.User so
// inline fragments dispatch; a nil user renders to null.
func (b *URLBuilder) GQLRepositoryOwner(u *domain.User, format nodeid.Format) gqlmodel.RepositoryOwner {
	if u == nil {
		return nil
	}
	return b.GQLUser(u, format)
}

// gqlHomepageURL converts a nullable homepage string into a nullable URI.
func gqlHomepageURL(s *string) *gqlmodel.URI {
	if s == nil || *s == "" {
		return nil
	}
	u := gqlmodel.URI(*s)
	return &u
}

// int64str is a tiny helper for strconv.FormatInt(n, 10) used in multiple
// places without importing strconv in every caller.
func int64str(n int64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := make([]byte, 20)
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = digits[n%10]
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

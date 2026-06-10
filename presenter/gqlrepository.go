package presenter

import (
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
	repo := gqlmodel.Repository{
		ID:               nodeid.Encode(nodeid.KindRepository, r.ID, format),
		Name:             r.Name,
		NameWithOwner:    r.FullName(),
		Description:      r.Description,
		IsPrivate:        r.Private,
		IsFork:           r.Fork,
		IsArchived:       r.Archived,
		IsEmpty:          r.PushedAt == nil,
		IsInOrganization: false, // Githome does not yet model organizations
		ForkCount:        0,     // not stored; resolver can extend later
		StargazerCount:   0,     // not stored; resolver can extend later
		HomepageURL:      gqlHomepageURL(r.Homepage),
		CreatedAt:        gqlmodel.NewDateTime(r.CreatedAt),
		UpdatedAt:        gqlmodel.NewDateTime(r.UpdatedAt),
		URL:                gqlmodel.URI(b.RepoHTML(r.Owner.Login, r.Name)),
		SSHURL:             gqlmodel.URI(b.RepoGitSSH(r.Owner.Login, r.Name)),
		RepoOwner:          r.Owner.Login,
		RepoName:           r.Name,
		AutoMergeAllowed:   false,
		MergeCommitAllowed: true,
		SquashMergeAllowed: true,
		RebaseMergeAllowed: true,
	}
	if r.PushedAt != nil {
		pushed := gqlmodel.NewDateTime(*r.PushedAt)
		repo.PushedAt = &pushed
	}
	if branch != nil {
		repo.DefaultBranchRef = GQLRef(r.PK, "refs/heads/"+branch.Name, branch.Name, branch.Commit)
	}
	return repo
}

// GQLRef renders a git reference into the GraphQL Ref shape. repoPK is the
// internal repository PK used to encode the node ID; qualifiedName is the full
// ref path (refs/heads/main); shortName is the bare ref name (main); sha is the
// target commit's SHA.
func GQLRef(repoPK int64, qualifiedName, shortName, sha string) *gqlmodel.Ref {
	prefix := ""
	if i := strings.LastIndex(qualifiedName, "/"); i >= 0 {
		prefix = qualifiedName[:i+1]
	}
	return &gqlmodel.Ref{
		ID:     nodeid.EncodeRef(repoPK, qualifiedName),
		Name:   shortName,
		Prefix: prefix,
		Target: &gqlmodel.GitObject{Oid: gqlmodel.GitObjectID(sha)},
	}
}

// GQLUser renders a domain user into the GraphQL User shape.
func (b *URLBuilder) GQLUser(u *domain.User, format nodeid.Format) *gqlmodel.User {
	if u == nil {
		return nil
	}
	out := &gqlmodel.User{
		ID:        nodeid.Encode(nodeid.KindUser, u.ID, format),
		Login:     u.Login,
		Name:      u.Name,
		Email:     u.Email,
		Bio:       u.Bio,
		URL:       gqlmodel.URI(b.UserHTML(u.Login)),
		AvatarURL: gqlmodel.URI(b.HTML("avatars", "u", int64str(u.ID))),
		CreatedAt: gqlmodel.NewDateTime(u.CreatedAt),
		UpdatedAt: gqlmodel.NewDateTime(u.UpdatedAt),
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
	return &gqlmodel.Milestone{
		ID:     nodeid.Encode(nodeid.KindMilestone, m.ID, format),
		Number: int32(m.Number),
		Title:  m.Title,
		State:  m.State,
		URL:    gqlmodel.URI(b.RepoHTML(owner, repo) + "/milestone/" + int64str(m.Number)),
	}
}

// GQLRepositoryOwner renders a repository's owner into the GraphQL
// RepositoryOwner shape.
func (b *URLBuilder) GQLRepositoryOwner(u *domain.User, _ nodeid.Format) *gqlmodel.RepositoryOwner {
	if u == nil {
		return &gqlmodel.RepositoryOwner{}
	}
	return &gqlmodel.RepositoryOwner{
		Login:     u.Login,
		URL:       gqlmodel.URI(b.UserHTML(u.Login)),
		AvatarURL: gqlmodel.URI(b.HTML("avatars", "u", int64str(u.ID))),
	}
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

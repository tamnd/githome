package presenter

import (
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/gqlmodel"
)

// GQLRepository renders a domain repository into the GraphQL Repository shape.
// branch is the resolved default-branch ref, or nil for a repository with no
// commits, in which case defaultBranchRef and pushedAt come back null just as
// GitHub returns them. format selects the node-ID encoding.
func (b *URLBuilder) GQLRepository(r *domain.Repo, branch *git.Branch, format nodeid.Format) gqlmodel.Repository {
	repo := gqlmodel.Repository{
		ID:            nodeid.Encode(nodeid.KindRepository, r.ID, format),
		Name:          r.Name,
		NameWithOwner: r.FullName(),
		Description:   r.Description,
		IsPrivate:     r.Private,
		CreatedAt:     gqlmodel.NewDateTime(r.CreatedAt),
		URL:           gqlmodel.URI(b.RepoHTML(r.Owner.Login, r.Name)),
	}
	if r.PushedAt != nil {
		pushed := gqlmodel.NewDateTime(*r.PushedAt)
		repo.PushedAt = &pushed
	}
	if branch != nil {
		repo.DefaultBranchRef = GQLRef(r.PK, branch.Name, branch.Commit)
	}
	return repo
}

// GQLRef renders a git ref into the GraphQL Ref shape. repoPK is the repository's
// internal primary key, used to encode the ref's node ID.
func GQLRef(repoPK int64, name, sha string) *gqlmodel.Ref {
	return &gqlmodel.Ref{
		ID:     nodeid.EncodeRef(repoPK, name),
		Name:   name,
		Target: &gqlmodel.GitObject{Oid: gqlmodel.GitObjectID(sha)},
	}
}

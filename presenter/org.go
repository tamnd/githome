package presenter

import (
	"strconv"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/restmodel"
)

// Organization renders the org profile for GET /orgs/{org}. Orgs share the
// users table, so u is the backing user account; the bio doubles as the org
// description.
func (b *URLBuilder) Organization(u *domain.User, format nodeid.Format) restmodel.Organization {
	base := b.API("orgs", u.Login)
	return restmodel.Organization{
		Login:            u.Login,
		ID:               u.ID,
		NodeID:           nodeid.Encode(nodeid.KindOrganization, u.ID, format),
		URL:              base,
		ReposURL:         base + "/repos",
		EventsURL:        base + "/events",
		HooksURL:         base + "/hooks",
		IssuesURL:        base + "/issues",
		MembersURL:       base + "/members{/member}",
		PublicMembersURL: base + "/public_members{/member}",
		AvatarURL:        b.HTML("avatars", "u", strconv.FormatInt(u.ID, 10)),
		Description:      u.Bio,
		Name:             u.Name,
		Company:          u.Company,
		Blog:             u.Blog,
		Location:         u.Location,
		Email:            u.Email,
		TwitterUsername:  u.TwitterUsername,
		IsVerified:       false,
		HasOrgProjects:   true,
		HasRepoProjects:  true,
		PublicRepos:      u.PublicRepos,
		PublicGists:      u.PublicGists,
		Followers:        u.Followers,
		Following:        u.Following,
		HTMLURL:          b.UserHTML(u.Login),
		CreatedAt:        restmodel.NewTime(u.CreatedAt),
		UpdatedAt:        restmodel.NewTime(u.UpdatedAt),
		Type:             "Organization",
	}
}

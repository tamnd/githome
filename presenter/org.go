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

// OrgSimple renders the trimmed org shape used inside list payloads such as
// GET /user/orgs.
func (b *URLBuilder) OrgSimple(u *domain.User, format nodeid.Format) restmodel.OrganizationSimple {
	base := b.API("orgs", u.Login)
	return restmodel.OrganizationSimple{
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
	}
}

// OrgMembership renders one membership for GET /user/memberships/orgs. The
// org is the member's organization and user is the member account.
func (b *URLBuilder) OrgMembership(org, user *domain.User, role string, format nodeid.Format) restmodel.OrgMembership {
	orgBase := b.API("orgs", org.Login)
	return restmodel.OrgMembership{
		URL:             orgBase + "/memberships/" + user.Login,
		State:           "active",
		Role:            role,
		OrganizationURL: orgBase,
		Organization:    b.OrgSimple(org, format),
		User:            b.SimpleUser(user, format),
	}
}

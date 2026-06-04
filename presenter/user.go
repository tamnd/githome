package presenter

import (
	"strconv"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/restmodel"
)

// SimpleUser renders the embedded actor object for u. It is pure: the same
// domain user, URL config, and node-id format always produce the same bytes.
func (b *URLBuilder) SimpleUser(u *domain.User, format nodeid.Format) restmodel.SimpleUser {
	base := b.UserAPI(u.Login)
	return restmodel.SimpleUser{
		Login:             u.Login,
		ID:                u.ID,
		NodeID:            nodeid.Encode(nodeid.KindUser, u.ID, format),
		AvatarURL:         b.HTML("avatars", "u", strconv.FormatInt(u.ID, 10)),
		GravatarID:        emptyString(),
		URL:               base,
		HTMLURL:           b.UserHTML(u.Login),
		FollowersURL:      base + "/followers",
		FollowingURL:      base + "/following{/other_user}",
		GistsURL:          base + "/gists{/gist_id}",
		StarredURL:        base + "/starred{/owner}{/repo}",
		SubscriptionsURL:  base + "/subscriptions",
		OrganizationsURL:  base + "/orgs",
		ReposURL:          base + "/repos",
		EventsURL:         base + "/events{/privacy}",
		ReceivedEventsURL: base + "/received_events",
		Type:              u.Type,
		SiteAdmin:         u.SiteAdmin,
	}
}

// User renders the full profile. When authenticated is true (GET /user for the
// viewer themselves), the private counters GitHub only shows the account owner
// are included; otherwise they are omitted.
func (b *URLBuilder) User(u *domain.User, format nodeid.Format, authenticated bool) restmodel.User {
	out := restmodel.User{
		SimpleUser:      b.SimpleUser(u, format),
		Name:            u.Name,
		Company:         u.Company,
		Blog:            u.Blog,
		Location:        u.Location,
		Email:           u.Email,
		Hireable:        u.Hireable,
		Bio:             u.Bio,
		TwitterUsername: u.TwitterUsername,
		PublicRepos:     u.PublicRepos,
		PublicGists:     u.PublicGists,
		Followers:       u.Followers,
		Following:       u.Following,
		CreatedAt:       restmodel.NewTime(u.CreatedAt),
		UpdatedAt:       restmodel.NewTime(u.UpdatedAt),
	}
	if authenticated {
		// Githome does not track private repository or gist counters yet, so the
		// owner-only fields report zero. They are present (not omitted) to match
		// GitHub's authenticated-user shape.
		zero := 0
		no := false
		out.PrivateGists = &zero
		out.TotalPrivateRepos = &zero
		out.OwnedPrivateRepos = &zero
		out.DiskUsage = &zero
		out.Collaborators = &zero
		out.TwoFactorAuthentication = &no
	}
	return out
}

func emptyString() *string {
	s := ""
	return &s
}

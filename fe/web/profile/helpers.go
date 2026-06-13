package profile

import (
	"strconv"
	"strings"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// helpers.go holds the small mappers the profile build reuses: the identity
// header and its vcard, the tab strip, the avatar URL, and the request parsers for
// the tab's page and the owner search qualifier. The header maps the optional
// account fields (name, bio, company, location, blog, social, joined) into
// resolved strings with their links built, so the template only decides whether a
// string is present.

// header maps an account into the identity card. The organization flag swaps the
// glyph and drops the hireable line; every optional field becomes a resolved
// string, empty when the account did not set it, so the template never reaches
// into a pointer. The blog row is normalized to an absolute URL so a bare
// "example.com" still links; the social row drops a leading @ so the handle reads
// once.
func (h *Handlers) header(u *domain.User) view.ProfileHeaderVM {
	hdr := view.ProfileHeaderVM{
		Login:       u.Login,
		AvatarURL:   h.avatar(u),
		IsOrg:       strings.EqualFold(u.Type, "Organization"),
		PublicRepos: u.PublicRepos,
		Followers:   u.Followers,
		Following:   u.Following,
	}
	if u.Name != nil {
		hdr.Name = *u.Name
	}
	if u.Bio != nil {
		hdr.Bio = strings.TrimSpace(*u.Bio)
	}
	if u.Company != nil {
		hdr.Company = strings.TrimSpace(*u.Company)
	}
	if u.Location != nil {
		hdr.Location = strings.TrimSpace(*u.Location)
	}
	if u.Email != nil {
		hdr.Email = strings.TrimSpace(*u.Email)
	}
	if b := strings.TrimSpace(u.Blog); b != "" {
		hdr.Blog = b
		hdr.BlogURL = normalizeURL(b)
	}
	if u.TwitterUsername != nil {
		if handle := strings.TrimPrefix(strings.TrimSpace(*u.TwitterUsername), "@"); handle != "" {
			hdr.TwitterHandle = handle
			hdr.TwitterURL = "https://twitter.com/" + handle
		}
	}
	if !u.CreatedAt.IsZero() {
		hdr.Joined = "Joined " + u.CreatedAt.UTC().Format("Jan 2006")
		hdr.JoinedISO = u.CreatedAt.UTC().Format(time.RFC3339)
	}
	return hdr
}

// tabs builds the strip the profile wears: the overview, the repositories tab, and
// the stars tab, the surfaces the domain backs. The repositories tab carries the
// public-repo count badge; the others carry none. The followers and following
// surfaces are reached from the identity card's count line rather than the strip,
// the way GitHub lays them out, so they are not strip entries; their bodies still
// render in the main column when their ?tab= is active. Each tab's URL is the
// canonical /{owner} or /{owner}?tab=…, so a click never lands on a redundant
// ?tab=overview.
func (h *Handlers) tabs(u *domain.User, active string) []view.ProfileTab {
	return []view.ProfileTab{
		{
			Key:      view.ProfileOverview,
			Label:    "Overview",
			Icon:     "book",
			URL:      route.ProfileTab(u.Login, view.ProfileOverview),
			IsActive: active == view.ProfileOverview,
		},
		{
			Key:      view.ProfileRepositories,
			Label:    "Repositories",
			Icon:     "repo",
			URL:      route.ProfileTab(u.Login, view.ProfileRepositories),
			IsActive: active == view.ProfileRepositories,
			Count:    u.PublicRepos,
			HasCount: true,
		},
		{
			Key:      view.ProfileStars,
			Label:    "Stars",
			Icon:     "star",
			URL:      route.ProfileTab(u.Login, view.ProfileStars),
			IsActive: active == view.ProfileStars,
		},
	}
}

// avatar builds the account's avatar URL through the presenter, the same builder
// the REST and Events surfaces use, so the picture on the profile matches the one
// the API serves.
func (h *Handlers) avatar(u *domain.User) string {
	return h.urls.HTML("avatars", "u", strconv.FormatInt(u.ID, 10))
}

// ownerQualifier returns the search qualifier that scopes a repository search to
// one owner: org: for an organization, user: for a user. The domain search
// resolves both by login, so either would match; choosing by type keeps the
// composed query readable.
func ownerQualifier(u *domain.User) string {
	if strings.EqualFold(u.Type, "Organization") {
		return "org:" + u.Login
	}
	return "user:" + u.Login
}

// pageParam reads the ?page= one-based page number, defaulting to 1 for a missing,
// non-numeric, or below-one value, the same tolerance the search pager applies.
func pageParam(c *mizu.Ctx) int {
	n, err := strconv.Atoi(c.Query("page"))
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// normalizeURL prefixes a bare host (no scheme) with https:// so a blog entered as
// "example.com" still links. A value that already names a scheme is returned
// unchanged.
func normalizeURL(s string) string {
	if strings.Contains(s, "://") {
		return s
	}
	return "https://" + s
}

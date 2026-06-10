package settings

// profile.go holds the account profile settings section: the form that edits
// the viewer's display name, bio, location, company, website, and social
// handles, and the save that writes those fields through the domain and
// redirects back. SSH keys remain a registered route with an honest-absence
// stub, since the key store is not backed yet; the tokens section lives in
// tokens.go.

import (
	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

// Profile renders the profile settings form, prefilled with the viewer's
// current account fields so they only edit what they want to change.
func (h *Handlers) Profile(c *mizu.Ctx) error {
	v, ok := h.gate(c)
	if !ok {
		return h.signInBounce(c)
	}
	pk := webmw.ViewerID(c.Context())
	u, err := h.users.Viewer(c.Context(), pk)
	if err != nil {
		h.log.Error("profile settings: load viewer", "err", err)
		return h.notFound(c)
	}
	vm := view.ProfileSettingsVM{
		Chrome:          h.view.Chrome(c, "Profile settings"),
		Nav:             h.nav(v, route.ProfileSettings()),
		Action:          route.ProfileSettings(),
		Name:            strDeref(u.Name),
		Email:           strDeref(u.Email),
		Bio:             strDeref(u.Bio),
		Location:        strDeref(u.Location),
		Company:         strDeref(u.Company),
		Blog:            u.Blog,
		TwitterUsername: strDeref(u.TwitterUsername),
	}
	return h.render.Page(c, "settings/profile", vm)
}

// SaveProfile validates and writes the submitted profile fields, then redirects
// back to the form with a flash notice.
func (h *Handlers) SaveProfile(c *mizu.Ctx) error {
	if _, ok := h.gate(c); !ok {
		return h.signInBounce(c)
	}
	fields := domain.ProfileFields{
		Name:            formString(c, "name"),
		Email:           formString(c, "email"),
		Bio:             formString(c, "bio"),
		Location:        formString(c, "location"),
		Company:         formString(c, "company"),
		Blog:            formString(c, "blog"),
		TwitterUsername: formString(c, "twitter_username"),
	}
	pk := webmw.ViewerID(c.Context())
	if err := h.users.UpdateProfile(c.Context(), pk, fields); err != nil {
		h.log.Error("profile settings: save", "err", err)
		h.flash.Add(c, "error", "Could not save your profile. Please try again.")
		return redirect(c, route.ProfileSettings())
	}
	h.flash.Add(c, "success", "Profile updated.")
	return redirect(c, route.ProfileSettings())
}

// Keys renders the SSH and GPG keys stub. The key store is not backed today, so
// this page shows an honest-absence message rather than an empty list that looks
// like everything is working.
func (h *Handlers) Keys(c *mizu.Ctx) error {
	v, ok := h.gate(c)
	if !ok {
		return h.signInBounce(c)
	}
	vm := struct {
		Chrome view.Chrome
		Nav    view.SettingsNav
	}{
		Chrome: h.view.Chrome(c, "SSH and GPG keys"),
		Nav:    h.nav(v, route.SettingsKeys()),
	}
	return h.render.Page(c, "settings/keys", vm)
}

// strDeref returns the string a pointer points to, or "" for nil.
func strDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

package settings

// sections.go holds the honest-absence stubs for the account-settings sections
// github.com exposes that Githome does not yet back: emails, notification
// preferences, password and authentication, organizations, authorized
// applications, and the developer-settings landing. Each renders inside the
// settings chrome and nav with a clear "not available yet" message, so the
// documented URL resolves to a recognizable page rather than the site-wide 404
// it used to hit, and the section reads as planned rather than broken. The
// nav-linked ones join the sidebar alongside the backed sections (see nav in
// handlers.go); applications and the developer landing are reachable by their
// documented URL only, matching where github.com files them under a separate
// developer-settings group. See 2005/review/01 R01-50.

import (
	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
)

// AccountSection is one stubbed account-settings section: the URL that selects
// it (exported so the mount can register the route), the page title, and the
// blankslate heading and description the stub shows.
type AccountSection struct {
	Path        string
	title       string
	heading     string
	description string
}

// AccountSections returns the known stubbed sections so the mount can register
// a route for each. The handler is Section.
func AccountSections() []AccountSection {
	return accountSections
}

// accountSections lists the known stubbed sections.
var accountSections = []AccountSection{
	{
		Path:        route.SettingsNotifications(),
		title:       "Notification settings",
		heading:     "Notification preferences are not available yet.",
		description: "Per-event notification routing is planned for a future milestone. Your notifications inbox already collects the threads you are subscribed to.",
	},
	{
		Path:        route.SettingsEmails(),
		title:       "Email settings",
		heading:     "Email management is not available yet.",
		description: "Adding and verifying email addresses is planned for a future milestone. Your account uses the address it was created with in the meantime.",
	},
	{
		Path:        route.SettingsSecurity(),
		title:       "Password and authentication",
		heading:     "Password and authentication settings are not available yet.",
		description: "Changing your password and enrolling two-factor authentication are planned for a future milestone.",
	},
	{
		Path:        route.SettingsOrganizations(),
		title:       "Organizations",
		heading:     "Organization membership is not available yet.",
		description: "Organizations and teams are planned for a future milestone.",
	},
	{
		Path:        route.SettingsApplications(),
		title:       "Authorized applications",
		heading:     "Authorized applications are not available yet.",
		description: "Reviewing and revoking the OAuth applications you have authorized is planned for a future milestone.",
	},
	{
		Path:        route.SettingsDevelopers(),
		title:       "Developer settings",
		heading:     "Developer settings are not available yet.",
		description: "OAuth and GitHub App management is planned for a future milestone. You can mint a personal access token from the tokens page today.",
	},
}

// sectionStubVM is the view model the section stub template renders.
type sectionStubVM struct {
	Chrome      view.Chrome
	Nav         view.SettingsNav
	Title       string
	Heading     string
	Description string
}

// Section returns the handler for one stubbed section: it gates the viewer the
// same as every settings page, then renders the stub inside the settings chrome
// and nav.
func (h *Handlers) Section(sec AccountSection) mizu.Handler {
	return func(c *mizu.Ctx) error {
		v, ok := h.gate(c)
		if !ok {
			return h.signInBounce(c)
		}
		vm := sectionStubVM{
			Chrome:      h.view.Chrome(c, sec.title),
			Nav:         h.nav(v, sec.Path),
			Title:       sec.title,
			Heading:     sec.heading,
			Description: sec.description,
		}
		return h.render.Page(c, "settings/section", vm)
	}
}

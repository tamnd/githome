package route

import "strconv"

// settings.go holds the URL builders for the settings surfaces: the account
// settings tree under /settings and the repository settings tree under
// /{owner}/{repo}/settings. They are pure string functions like the rest of
// fe/route, so a settings link in a template and the route that serves it cannot
// drift. Githome backs the account profile, appearance, SSH keys stub, and
// tokens stub sections today, plus a repository's webhooks. The unbacked
// sections are absent from the nav rather than linking to dead pages.
// See implementation/13.

// AccountSettings is the account settings root, /settings. It redirects to the
// first backed section so a bookmark of the bare root keeps working.
func AccountSettings() string {
	return "/settings"
}

// Appearance is the account appearance preference, /settings/appearance, where a
// viewer picks their color mode and the light and dark themes. It is the one
// account section Githome backs, since the preference rides cookies the
// color-mode middleware already reads.
func Appearance() string {
	return "/settings/appearance"
}

// ProfileSettings is the account profile settings page, /settings/profile,
// where a signed-in viewer can update their display name, bio, location, and
// the other public profile fields.
func ProfileSettings() string {
	return "/settings/profile"
}

// SettingsKeys is the SSH and GPG keys page, /settings/keys. Githome shows a
// stub today since the key store is not yet backed; the route is registered so
// the nav link is live and the honest-absence message is visible.
func SettingsKeys() string {
	return "/settings/keys"
}

// SettingsTokens is the personal access tokens page, /settings/tokens: the
// list of the viewer's classic tokens and the form that mints a new one.
func SettingsTokens() string {
	return "/settings/tokens"
}

// SettingsTokenNew is the mint-a-token form, /settings/tokens/new, the
// dedicated page github.com shows for creating a classic token. It is a literal
// segment registered before the {id}/delete route, so "new" is never read as a
// token id.
func SettingsTokenNew() string {
	return SettingsTokens() + "/new"
}

// SettingsTokenDelete is the delete-token POST target,
// /settings/tokens/{id}/delete. Deleting is a POST, never a GET, so a crawler
// or a prefetch cannot revoke a credential.
func SettingsTokenDelete(id int64) string {
	return SettingsTokens() + "/" + strconv.FormatInt(id, 10) + "/delete"
}

// The remaining account-settings sections are documented github.com URLs that
// Githome does not yet back. Each resolves to an honest-absence stub inside the
// settings chrome rather than a site-wide 404, so the URL is recognized and the
// section reads as planned rather than broken. See 2005/review/01 R01-50.

// SettingsEmails is the email-addresses page, /settings/emails.
func SettingsEmails() string {
	return "/settings/emails"
}

// SettingsNotifications is the notification preferences page,
// /settings/notifications (distinct from the /notifications inbox).
func SettingsNotifications() string {
	return "/settings/notifications"
}

// SettingsSecurity is the password and authentication page,
// /settings/security.
func SettingsSecurity() string {
	return "/settings/security"
}

// SettingsOrganizations is the organization membership page,
// /settings/organizations.
func SettingsOrganizations() string {
	return "/settings/organizations"
}

// SettingsApplications is the authorized applications page,
// /settings/applications.
func SettingsApplications() string {
	return "/settings/applications"
}

// SettingsDevelopers is the developer settings landing, /settings/developers.
func SettingsDevelopers() string {
	return "/settings/developers"
}

// RepoSettings is a repository's settings root, /{owner}/{repo}/settings. It
// redirects to the first backed section (the webhooks list).
func RepoSettings(owner, name string) string {
	return Repo(owner, name) + "/settings"
}

// RepoHooks is a repository's webhooks list, /{owner}/{repo}/settings/hooks.
func RepoHooks(owner, name string) string {
	return Repo(owner, name) + "/settings/hooks"
}

// RepoHookNew is the new-webhook form, /{owner}/{repo}/settings/hooks/new. It is
// a literal segment registered before the {hook} id route, so "new" is never
// read as an id.
func RepoHookNew(owner, name string) string {
	return RepoHooks(owner, name) + "/new"
}

// RepoHook is one webhook's edit page, /{owner}/{repo}/settings/hooks/{hook},
// keyed by its public id.
func RepoHook(owner, name string, hookID int64) string {
	return RepoHooks(owner, name) + "/" + strconv.FormatInt(hookID, 10)
}

// RepoHookDelete is the delete-webhook POST target,
// /{owner}/{repo}/settings/hooks/{hook}/delete. Deleting is a POST, never a GET,
// so a crawler or a prefetch cannot remove a hook.
func RepoHookDelete(owner, name string, hookID int64) string {
	return RepoHook(owner, name, hookID) + "/delete"
}

// RepoHookDelivery is one recorded delivery's detail page,
// /{owner}/{repo}/settings/hooks/{hook}/deliveries/{delivery}.
func RepoHookDelivery(owner, name string, hookID, deliveryID int64) string {
	return RepoHook(owner, name, hookID) + "/deliveries/" + strconv.FormatInt(deliveryID, 10)
}

// RepoHookRedeliver is the replay-delivery POST target,
// /{owner}/{repo}/settings/hooks/{hook}/deliveries/{delivery}/redeliver.
func RepoHookRedeliver(owner, name string, hookID, deliveryID int64) string {
	return RepoHookDelivery(owner, name, hookID, deliveryID) + "/redeliver"
}

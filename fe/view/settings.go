package view

import (
	"slices"
	"strings"
)

// settings.go holds the view models for the settings surfaces: the shared
// sidebar both the account and the repository settings pages wear, the account
// appearance preference (the color mode and the light and dark themes), and a
// repository's webhooks (the list, the create-and-edit form, and a recorded
// delivery's detail). It is pure data with every URL precomputed in the handler
// through fe/route; the template prints fields and never reaches back into the
// domain. Githome backs only this slice of GitHub's settings today, so the
// unbacked sections get no nav entry rather than a disabled link to nowhere. See
// implementation/13.

// SettingsNav is the sidebar a settings page wears: a heading naming the account
// or repository the settings belong to, the link the heading points at, and the
// section links. The strip is text only, the way GitHub's settings sidebar is, so
// it needs no per-item icon. A section Githome does not back is simply not in
// Items, never a disabled row.
type SettingsNav struct {
	Heading    string
	HeadingURL string
	Items      []SettingsNavItem
}

// SettingsNavItem is one section link in the settings sidebar: its label, the URL
// that selects it, and whether it is the current section.
type SettingsNavItem struct {
	Label    string
	URL      string
	IsActive bool
}

// AppearanceOption is one choice in a settings select or radio group: the stored
// value, the human label, and whether it is the current selection. The account
// appearance form and the webhook content-type select both render through it.
type AppearanceOption struct {
	Value    string
	Label    string
	Selected bool
}

// AppearanceVM is the account appearance page: the color mode (auto, light, or
// dark) and the theme used under each of the light and dark slots. The form posts
// to Action and the handler writes the three preference cookies the color-mode
// middleware reads, so the choice survives with no JavaScript and no account
// column. The lists are built from the closed catalogs below, so the form can
// only ever offer a theme the asset build actually generated.
type AppearanceVM struct {
	Chrome Chrome
	Nav    SettingsNav

	Action      string
	Modes       []AppearanceOption
	LightThemes []AppearanceOption
	DarkThemes  []AppearanceOption
}

// HookListVM is a repository's webhooks list: a row per hook with its delivery
// target, its active state, the events it fires on, and the status of its most
// recent delivery. Empty drives the blankslate for a repository with no hooks.
type HookListVM struct {
	Chrome Chrome
	Nav    SettingsNav

	RepoFullName string
	NewURL       string
	Hooks        []HookRowVM
	Empty        bool
}

// HookRowVM is one row in the webhooks list. Target is the delivery URL shown as
// the row title and linking to the hook's edit page; Events is the human summary
// of the subscription; the status pair reads the most recent delivery so a viewer
// sees at a glance whether the endpoint is answering.
type HookRowVM struct {
	URL         string
	Target      string
	Active      bool
	Events      string
	StatusIcon  string
	StatusKind  string
	StatusLabel string
}

// HookFormVM is the shared create-and-edit webhook form. IsNew picks the heading,
// the submit label, and whether the delete control and the delivery history show.
// The secret is never rendered back: HasSecret reports only whether one is set, so
// the field is always blank and a save that leaves it blank keeps the stored
// secret (the handler reads a separate clear control to remove it). FormError
// carries an inline validation message so a bad URL re-renders the filled form
// rather than an error page.
type HookFormVM struct {
	Chrome Chrome
	Nav    SettingsNav

	Title        string
	Action       string
	IsNew        bool
	DeleteAction string
	FormError    string

	PayloadURL  string
	ContentType string
	HasSecret   bool
	InsecureSSL bool
	Active      bool

	ContentTypes []AppearanceOption
	Events       []HookEventChoice
	Everything   bool

	Deliveries []HookDeliveryRowVM
}

// HookEventChoice is one event checkbox on the webhook form: the stored event
// name, its human label, and whether the hook currently subscribes to it.
type HookEventChoice struct {
	Value   string
	Label   string
	Checked bool
}

// HookDeliveryRowVM is one entry in a webhook's delivery history (and the header
// of the delivery detail page): the event, the status of the attempt, whether it
// was a manual redelivery, and the link to its full record. RedeliverAction is the
// POST target that replays it.
type HookDeliveryRowVM struct {
	URL             string
	GUID            string
	Event           string
	StatusIcon      string
	StatusKind      string
	StatusLabel     string
	Redelivery      bool
	DeliveredAt     string
	DeliveredISO    string
	RedeliverAction string
}

// HookDeliveryDetailVM is one recorded delivery in full: the request and response
// headers and bodies the worker stored, so an integrator can see exactly what
// Githome sent and what the endpoint answered. BackURL returns to the hook.
type HookDeliveryDetailVM struct {
	Chrome Chrome
	Nav    SettingsNav

	BackURL string
	Row     HookDeliveryRowVM

	RequestHeaders  []HeaderKV
	RequestBody     string
	ResponseHeaders []HeaderKV
	ResponseBody    string
}

// HeaderKV is one header line in a delivery's request or response, kept as an
// ordered pair so the template prints the headers without ranging a map (whose
// order Go randomizes). The handler sorts them for a stable view.
type HeaderKV struct {
	Name  string
	Value string
}

// The closed catalogs the appearance form offers. They mirror the nine palettes
// the asset build generates and the modes the color-mode middleware validates, so
// the write side here and the read side in webmw agree on the same closed sets;
// a value outside them is rejected by the handler before a cookie is written. See
// fe/webmw/colormode.go.

var appearanceModes = []AppearanceOption{
	{Value: "auto", Label: "Sync with system"},
	{Value: "light", Label: "Light"},
	{Value: "dark", Label: "Dark"},
}

var lightThemeCatalog = []AppearanceOption{
	{Value: "light", Label: "Light default"},
	{Value: "light_high_contrast", Label: "Light high contrast"},
	{Value: "light_colorblind", Label: "Light Protanopia and Deuteranopia"},
	{Value: "light_tritanopia", Label: "Light Tritanopia"},
}

var darkThemeCatalog = []AppearanceOption{
	{Value: "dark", Label: "Dark default"},
	{Value: "dark_dimmed", Label: "Dark dimmed"},
	{Value: "dark_high_contrast", Label: "Dark high contrast"},
	{Value: "dark_colorblind", Label: "Dark Protanopia and Deuteranopia"},
	{Value: "dark_tritanopia", Label: "Dark Tritanopia"},
}

// hookEventCatalog is the closed set of events the webhook form offers as
// individual checkboxes. It lists exactly the events Githome's domain can emit
// today (the EventService records nothing else), so the form never advertises an
// event that can never fire. The wildcard is offered separately as "everything".
var hookEventCatalog = []HookEventChoice{
	{Value: "push", Label: "Pushes"},
	{Value: "pull_request", Label: "Pull requests"},
	{Value: "pull_request_review", Label: "Pull request reviews"},
	{Value: "issues", Label: "Issues"},
	{Value: "issue_comment", Label: "Issue comments"},
}

var hookContentTypes = []AppearanceOption{
	{Value: "json", Label: "application/json"},
	{Value: "form", Label: "application/x-www-form-urlencoded"},
}

// AppearanceModeOptions returns the mode radio group with selected marked.
func AppearanceModeOptions(selected string) []AppearanceOption {
	return markSelected(appearanceModes, selected)
}

// LightThemeOptions returns the light-theme select with selected marked.
func LightThemeOptions(selected string) []AppearanceOption {
	return markSelected(lightThemeCatalog, selected)
}

// DarkThemeOptions returns the dark-theme select with selected marked.
func DarkThemeOptions(selected string) []AppearanceOption {
	return markSelected(darkThemeCatalog, selected)
}

// HookContentTypeOptions returns the content-type select with selected marked.
func HookContentTypeOptions(selected string) []AppearanceOption {
	return markSelected(hookContentTypes, selected)
}

// markSelected copies a catalog and marks the option whose value equals selected,
// so the caller's catalog stays immutable across requests.
func markSelected(catalog []AppearanceOption, selected string) []AppearanceOption {
	out := make([]AppearanceOption, len(catalog))
	copy(out, catalog)
	for i := range out {
		out[i].Selected = out[i].Value == selected
	}
	return out
}

// HookEventChoices returns the event checkboxes with the hook's current
// subscription checked. A subscription carrying the wildcard checks nothing here;
// the form drives the wildcard through its own "everything" control, read with
// HookSubscribesAll.
func HookEventChoices(events []string) []HookEventChoice {
	set := map[string]bool{}
	for _, e := range events {
		set[e] = true
	}
	out := make([]HookEventChoice, len(hookEventCatalog))
	copy(out, hookEventCatalog)
	for i := range out {
		out[i].Checked = set[out[i].Value]
	}
	return out
}

// HookEventNames returns the closed set of individual event names the form
// offers, for the handler to validate a submission against.
func HookEventNames() []string {
	out := make([]string, len(hookEventCatalog))
	for i, e := range hookEventCatalog {
		out[i] = e.Value
	}
	return out
}

// HookSubscribesAll reports whether a subscription is the wildcard, the state the
// form shows as "send me everything".
func HookSubscribesAll(events []string) bool {
	return slices.Contains(events, "*")
}

// EventsSummary renders a subscription as the one-line human summary the list
// row shows: "everything" for the wildcard, otherwise the events joined with a
// comma. An empty subscription reads as the push default the domain stores.
func EventsSummary(events []string) string {
	if HookSubscribesAll(events) {
		return "everything"
	}
	if len(events) == 0 {
		return "push"
	}
	return strings.Join(events, ", ")
}

// ValidMode reports whether m is one of the three modes the appearance form
// offers, so the handler rejects a forged value before writing the cookie.
func ValidMode(m string) bool {
	return optionHasValue(appearanceModes, m)
}

// ValidLightTheme reports whether t is one of the light themes the form offers.
func ValidLightTheme(t string) bool {
	return optionHasValue(lightThemeCatalog, t)
}

// ValidDarkTheme reports whether t is one of the dark themes the form offers.
func ValidDarkTheme(t string) bool {
	return optionHasValue(darkThemeCatalog, t)
}

func optionHasValue(catalog []AppearanceOption, value string) bool {
	for _, o := range catalog {
		if o.Value == value {
			return true
		}
	}
	return false
}

// HookStatusGlyph maps a delivery or last-response status string the domain
// reports to the icon and the style kind the row wears: a green check for OK, a
// muted dot for a hook that has not fired yet, and a red circle for a failure.
// The icon names are already in the registry, so they need no separate
// registration. The status strings are the ones domain.deliveryStatus and the
// last-response summary produce.
func HookStatusGlyph(status string) (icon, kind string) {
	switch status {
	case "OK":
		return "check-circle", "success"
	case "", "unused":
		return "dot-fill", "muted"
	default:
		return "x-circle", "danger"
	}
}

package view

// profile.go holds the user and organization profile view models: the identity
// header (avatar, name, the vcard of company, location, blog, social, and the
// joined date), the tab strip, the overview (popular repositories and the recent
// activity feed), and the repositories tab. It is pure data with every URL
// precomputed in the handler through fe/route; the template prints fields and
// switches on the active tab. The surfaces Githome's domain does not back yet
// (the follow button, the contribution graph, the pinned-repo picker, the
// per-tab stars) are simply absent rather than shown disabled, so the profile
// never advertises a capability that is not there. See implementation/12 sections
// 5, 6, and 7.

// The profile tab keys. They are the ?tab= values and the keys the strip and the
// builder dispatch on. Overview is the default and carries no query, so a
// bookmarked bare /{owner} keeps working.
const (
	ProfileOverview     = "overview"
	ProfileRepositories = "repositories"
)

// ProfileHeaderVM is the identity card a profile wears: the login and display
// name, the avatar, the organization flag that swaps the icon and drops the
// person-only vcard rows, the bio, and the vcard details. Each vcard row is a
// resolved string with its optional link already built, so the template only
// decides whether the string is present. Counts are plain integers; the followers
// and following lists are not a backed surface, so the header shows the numbers
// without linking them, never a dead link.
type ProfileHeaderVM struct {
	Login     string
	Name      string
	AvatarURL string
	IsOrg     bool

	Bio string // the account's short bio, shown as escaped plain text

	Company  string
	Location string

	Blog    string // the display text for the website row
	BlogURL string // the href for the website row, normalized to an absolute URL

	Email string // shown only when the account made it public

	TwitterHandle string // without the leading @, shown only when set
	TwitterURL    string

	Joined    string // the human "Joined Jan 2006" line
	JoinedISO string // the machine datetime for the relative-time element

	PublicRepos int
	Followers   int
	Following   int
}

// ProfileTab is one entry in the tab strip: its key, label, the URL that selects
// it, whether it is current, and an optional count badge (the repositories tab
// shows the public-repo total). Overview carries no count.
type ProfileTab struct {
	Key      string
	Label    string
	Icon     string
	URL      string
	IsActive bool
	Count    int
	HasCount bool
}

// FeedItemVM is one entry in the activity feed: the octicon the event reads as,
// the actor, the verb phrase ("opened an issue in", "pushed to", "starred"), the
// repository the event happened in, an optional target (an issue or pull number
// with its title) the verb points at, and the timestamps. The phrase is split
// into the verb and the target so the template links the repository and the
// target independently; the handler composes both from the stored event so the
// view stays pure data. The icon name is registered in the icon set because it is
// referenced through this field rather than a template literal.
type FeedItemVM struct {
	Icon string

	ActorLogin string
	ActorURL   string

	Verb string // "opened an issue in", "pushed to", "created a branch in", ...

	RepoFullName string
	RepoURL      string

	Target     string // "#5 Fix the parser", a branch name, or empty
	TargetURL  string // the link for the target, or empty for an unlinked target
	CreatedAt  string
	CreatedISO string
}

// ProfileOverviewVM is the overview tab body: a short grid of the owner's most
// recently updated repositories and the recent-activity timeline. The Empty flag
// and the activity blankslate carry the honest empty states (a fresh account with
// no public repositories, an account with no public activity yet).
type ProfileOverviewVM struct {
	PopularRepos []RepoResultVM
	ReposURL     string // the "show all" link to the repositories tab

	Activity      []FeedItemVM
	ActivityEmpty bool

	Empty bool // true when the account has no public repos and no activity yet
}

// ProfileReposVM is the repositories tab body: the owner's visible repositories,
// the sort menu, the pager, and the blankslate for an owner with no repositories
// (or none matching the in-tab filter). It reuses the search row and sort models
// because the tab is backed by the same domain repository search scoped to the
// owner, so the two surfaces render the same row.
type ProfileReposVM struct {
	Items []RepoResultVM
	Sorts []SearchSortOption
	Pager Pager

	Empty       bool
	EmptyReason string
}

// ProfilePageVM is the whole profile page: the shell, the identity header, the tab
// strip, and exactly one tab body filled (the overview or the repositories tab).
// The template switches on ActiveTab so the unused body is zero.
type ProfilePageVM struct {
	Chrome Chrome
	Header ProfileHeaderVM

	Tabs      []ProfileTab
	ActiveTab string

	Overview ProfileOverviewVM
	Repos    ProfileReposVM
}

// ProfileTabOr validates a requested ?tab= against the two backed tabs and falls
// back to the overview when it is empty or unknown. A bad tab never errors, it
// degrades to the overview, matching the search type's tolerance for a human's
// URL.
func ProfileTabOr(raw string) string {
	switch raw {
	case ProfileRepositories:
		return ProfileRepositories
	default:
		return ProfileOverview
	}
}

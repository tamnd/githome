package route

// The profile URL builders are the one place the web front turns a login into a
// profile URL, so the header viewer menu, the activity feed actor links, and the
// repository owner links all route through the same functions and can never drift
// from the /{owner} catch-all that serves them. They follow the package rule:
// pure string functions with no router or domain dependency. The profile lives at
// the root, /{owner}, the same place github.com puts it, so a reserved top-level
// name (fe/route reserved.go) can never be read as a login. See implementation/02
// section 5 and implementation/12 sections 5 and 6.

// Profile is a user or organization profile, /{owner}. It is the overview tab by
// default; the repositories tab adds the ?tab= facet through ProfileTab.
func Profile(login string) string {
	return "/" + esc(login)
}

// ProfileTab is a profile at a named tab, /{owner}?tab={tab}. The overview tab is
// the bare profile with no query, so passing "overview" (or an empty tab) yields
// the canonical URL rather than a redundant ?tab=overview.
func ProfileTab(login, tab string) string {
	if tab == "" || tab == "overview" {
		return Profile(login)
	}
	return Profile(login) + "?tab=" + esc(tab)
}

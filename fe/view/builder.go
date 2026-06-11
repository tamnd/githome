package view

import "github.com/go-mizu/mizu"

// This file holds the per-page view models the F0 shell renders. Each is a flat
// struct embedding a Chrome, built from the request by a small method on Builder
// so a handler stays a few lines: resolve domain data, build the view model,
// render. Later milestones add their feature view models in their own files.

// HomeVM is the landing page model: the dashboard for a signed-in viewer, the
// sign-in blankslate for an anonymous one, switched on Chrome.Viewer. The
// dashboard carries the viewer's repositories for the sidebar and their recent
// activity for the feed; the fe/web/home handlers fill both, so / and /dashboard
// render the same page from the same model.
type HomeVM struct {
	Chrome Chrome

	Repos      []HomeRepoVM // the viewer's repositories, newest activity first
	ReposURL   string       // the "show all" link to the profile repositories tab
	NewRepoURL string       // the create-repository form, /new

	Feed      []FeedItemVM // the viewer's recent activity, the profile catalog's lines
	FeedEmpty bool
}

// HomeRepoVM is one repository line in the dashboard sidebar: the full name the
// link shows and the lock the private ones carry.
type HomeRepoVM struct {
	FullName string
	URL      string
	Private  bool
}

// Home builds the landing page model's shell; the home handlers fill the
// dashboard fields for a signed-in viewer.
func (b *Builder) Home(c *mizu.Ctx) HomeVM {
	title := ""
	if ViewerFrom(c.Context()) == nil {
		// Anonymous landing leads with the site name, so no page title prefix.
		title = ""
	}
	return HomeVM{Chrome: b.Chrome(c, title)}
}

// NotificationsVM is the notifications inbox model. The inbox is backed by the
// notifications domain layer when it is available; when nil, an authenticated
// viewer sees the empty-inbox blankslate. An anonymous viewer is not shown an
// inbox — mountNotifications 404s before the view model is built.
type NotificationsVM struct {
	Chrome Chrome
}

// Notifications builds the notifications inbox model for the signed-in viewer.
func (b *Builder) Notifications(c *mizu.Ctx) NotificationsVM {
	return NotificationsVM{Chrome: b.Chrome(c, "Notifications")}
}

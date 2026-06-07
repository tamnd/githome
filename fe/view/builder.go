package view

import "github.com/go-mizu/mizu"

// This file holds the per-page view models the F0 shell renders. Each is a flat
// struct embedding a Chrome, built from the request by a small method on Builder
// so a handler stays a few lines: resolve domain data, build the view model,
// render. Later milestones add their feature view models in their own files.

// HomeVM is the landing page model. F0 carries only the shell; the dashboard
// content is driven entirely off Chrome.Viewer (signed-in versus anonymous). The
// feed and the viewer's repositories arrive in a later milestone.
type HomeVM struct {
	Chrome Chrome
}

// Home builds the landing page model.
func (b *Builder) Home(c *mizu.Ctx) HomeVM {
	title := ""
	if ViewerFrom(c.Context()) == nil {
		// Anonymous landing leads with the site name, so no page title prefix.
		title = ""
	}
	return HomeVM{Chrome: b.Chrome(c, title)}
}

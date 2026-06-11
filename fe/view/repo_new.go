package view

// repo_new.go holds the create-repository form's view model: the /new page a
// signed-in viewer fills to create a repository under their own login. Like the
// rest of fe/view it is pure data; the fe/web/repo handler maps the form state
// into it and the POST handler re-fills it when validation sends the form back.

// RepoNewVM is the create-repository form. Owner is the login the repository is
// created under: Githome's create gate is the viewer themself (the same rule the
// REST create enforces), so the owner control shows the one owner the service
// will accept. The Value fields carry the submitted input back into a re-rendered
// form so a validation miss never eats what the viewer typed. The repository is
// created empty; the redirect lands on the repo home, whose quick-setup view
// walks the first push, so the form carries no init checkboxes for content the
// create service does not write.
type RepoNewVM struct {
	Chrome Chrome

	Owner  string // the viewer's login, the one owner the create accepts
	Action string // the POST target, /new

	NameValue        string
	DescriptionValue string
	Private          bool

	FormError string // the inline validation message, empty on a fresh form
}

package repo

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

// new.go is the create-repository form at /new: the page the home and the app
// header's plus menu link to. The form writes through the same domain create the
// REST POST /user/repos uses, so the page and the API agree about who may create
// what and what a fresh repository looks like. The surface is function-private
// rather than secret (every account can create a repository), so an anonymous
// request bounces to the sign-in form with return_to, the settings rule. The
// repository is created empty; the redirect lands on the repo home, whose
// quick-setup view walks the first push, so the form carries no init checkboxes
// for content the create service does not write. See Spec 2005 docs 02 and 14.

// repoNamePattern is the shape a repository name must have: the dot, dash,
// underscore, and alphanumeric set GitHub accepts, so every name survives a URL
// path segment unescaped. The all-dots names are rejected separately since "."
// and ".." are path words, not names.
var repoNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// NewForm renders the create-repository form for the signed-in viewer.
func (h *Handlers) NewForm(c *mizu.Ctx) error {
	v := view.ViewerFrom(c.Context())
	if v == nil {
		return h.signInBounce(c)
	}
	return h.render.Page(c, "repo/new", h.newRepoVM(c, v, view.RepoNewVM{}))
}

// CreateRepo handles the form post: validate the name, check it is free under
// the viewer, create through the domain service, and land on the new repo's
// home, where the quick-setup view shows the first push. A validation miss
// re-renders the form with the message inline and the input preserved.
func (h *Handlers) CreateRepo(c *mizu.Ctx) error {
	ctx := c.Context()
	v := view.ViewerFrom(ctx)
	if v == nil {
		return h.signInBounce(c)
	}

	name := formString(c, "name")
	desc := formString(c, "description")
	private := formString(c, "visibility") == "private"

	vm := view.RepoNewVM{NameValue: name, DescriptionValue: desc, Private: private}
	if msg := validateRepoName(name); msg != "" {
		vm.FormError = msg
		return h.render.Page(c, "repo/new", h.newRepoVM(c, v, vm))
	}

	// The availability check reads through the same gate the create enforces.
	// A racing duplicate still fails at the unique index and renders a 500,
	// which the form's pre-check makes vanishingly rare.
	if _, err := h.repos.GetRepo(ctx, webmw.ViewerID(ctx), v.Login, name); err == nil {
		vm.FormError = "The repository " + v.Login + "/" + name + " already exists."
		return h.render.Page(c, "repo/new", h.newRepoVM(c, v, vm))
	} else if !errors.Is(err, domain.ErrRepoNotFound) {
		return err
	}

	in := domain.RepoInput{Name: name, Private: private}
	if desc != "" {
		in.Description = &desc
	}
	repo, err := h.repos.CreateRepo(ctx, webmw.ViewerID(ctx), v.Login, in)
	if err != nil {
		return err
	}
	return c.Redirect(http.StatusSeeOther, route.Repo(v.Login, repo.Name))
}

// newRepoVM fills the form model's chrome, owner, and action around the field
// state the caller carries.
func (h *Handlers) newRepoVM(c *mizu.Ctx, v *view.Viewer, vm view.RepoNewVM) view.RepoNewVM {
	vm.Chrome = h.chrome(c, "Create a new repository")
	vm.Owner = v.Login
	vm.Action = route.NewRepo()
	return vm
}

// validateRepoName returns the inline message for a name the form cannot
// accept, or empty for a usable one.
func validateRepoName(name string) string {
	switch {
	case name == "":
		return "Repository name is required."
	case len(name) > 100:
		return "Repository name is too long (maximum is 100 characters)."
	case strings.Trim(name, ".") == "":
		return "Repository name is not valid."
	case !repoNamePattern.MatchString(name):
		return "Repository name may only contain letters, digits, dots, hyphens, and underscores."
	default:
		return ""
	}
}

// signInBounce sends an anonymous request to the sign-in form, with return_to
// carrying the page it wanted so a successful sign-in lands back on it. The
// create form is function-private, not secret, so the bounce confirms nothing.
func (h *Handlers) signInBounce(c *mizu.Ctx) error {
	return c.Redirect(http.StatusFound, route.LoginWithReturn(c.Request().URL.RequestURI()))
}

// formString reads a trimmed form value; a parse failure reads as missing.
func formString(c *mizu.Ctx, key string) string {
	form, err := c.Form()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(form.Get(key))
}

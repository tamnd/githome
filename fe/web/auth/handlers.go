// Package auth holds the Githome web front's authentication handlers: sign-in,
// sign-up, and sign-out. They live under /login, /join, and /logout and are
// gated the same way settings is (anonymous sees the form; a signed-in viewer is
// redirected). They hold no credential logic: password hashing and verification
// use bcrypt via the auth store interface, and the session cookie is issued by
// the existing webmw.Sessions. See implementation/06.
package auth

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-mizu/mizu"
	"golang.org/x/crypto/bcrypt"

	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

// PasswordStore is the narrow store interface the auth handlers use to look up
// and set password hashes. fe/web/auth never imports store directly (doc 01 §6).
type PasswordStore interface {
	PasswordHashFor(ctx context.Context, login string) (pk int64, hash string, err error)
	InsertUserWithPassword(ctx context.Context, login, email, hash string) (pk int64, err error)
	UserLoginExists(ctx context.Context, login string) (bool, error)
}

// Deps are the auth handlers' dependencies.
type Deps struct {
	Store    PasswordStore
	Sessions *webmw.Sessions
	View     *view.Builder
	Render   *render.Set
	Logger   *slog.Logger
}

// Handlers is the auth handler set. One is built at boot and shared.
type Handlers struct {
	store    PasswordStore
	sessions *webmw.Sessions
	view     *view.Builder
	render   *render.Set
	log      *slog.Logger
}

// New wires the handler set from its dependencies.
func New(d Deps) *Handlers {
	return &Handlers{
		store:    d.Store,
		sessions: d.Sessions,
		view:     d.View,
		render:   d.Render,
		log:      d.Logger,
	}
}

// loginVM is the view model for the login page.
type loginVM struct {
	view.Chrome
	Action     string
	ReturnTo   string
	LoginValue string
	Error      string
}

// joinVM is the view model for the join/sign-up page.
type joinVM struct {
	view.Chrome
	Action     string
	LoginValue string
	EmailValue string
	Error      string
	LoginError string
	EmailError string
}

// logoutVM is the view model for the logout confirmation page.
type logoutVM struct {
	view.Chrome
	Action string
}

// LoginForm renders the sign-in form. A signed-in viewer is redirected to / (or
// the return_to URL). Anonymous viewers see the form.
func (h *Handlers) LoginForm(c *mizu.Ctx) error {
	if view.ViewerFrom(c.Context()) != nil {
		return c.Redirect(http.StatusSeeOther, safeReturn(c.Request().URL.Query().Get("return_to")))
	}
	return h.render.Page(c, "auth/login", loginVM{
		Chrome:   h.view.Chrome(c, "Sign in"),
		Action:   route.LoginSession(),
		ReturnTo: c.Request().URL.Query().Get("return_to"),
	})
}

// LoginSubmit handles the sign-in POST. Verifies password, issues session cookie,
// redirects on success. Renders the form with an error on failure.
func (h *Handlers) LoginSubmit(c *mizu.Ctx) error {
	if err := c.Request().ParseForm(); err != nil {
		return err
	}
	login := strings.TrimSpace(c.Request().FormValue("login"))
	password := c.Request().FormValue("password")
	returnTo := safeReturn(c.Request().FormValue("return_to"))

	renderErr := func(msg string) error {
		return h.render.Page(c, "auth/login", loginVM{
			Chrome:     h.view.Chrome(c, "Sign in"),
			Action:     route.LoginSession(),
			ReturnTo:   returnTo,
			LoginValue: login,
			Error:      msg,
		})
	}

	if login == "" || password == "" {
		return renderErr("Username/email and password are required.")
	}

	pk, hash, err := h.store.PasswordHashFor(c.Context(), login)
	if err != nil || hash == "" {
		return renderErr("Incorrect username or password.")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return renderErr("Incorrect username or password.")
	}

	h.sessions.Issue(c, pk, time.Now())
	return c.Redirect(http.StatusSeeOther, returnTo)
}

// JoinForm renders the sign-up form. A signed-in viewer is redirected to /.
func (h *Handlers) JoinForm(c *mizu.Ctx) error {
	if view.ViewerFrom(c.Context()) != nil {
		return c.Redirect(http.StatusSeeOther, "/")
	}
	return h.render.Page(c, "auth/join", joinVM{
		Chrome: h.view.Chrome(c, "Create your account"),
		Action: route.Join(),
	})
}

// JoinSubmit handles the sign-up POST. Validates the form, creates the user,
// issues a session, and redirects to /.
func (h *Handlers) JoinSubmit(c *mizu.Ctx) error {
	if err := c.Request().ParseForm(); err != nil {
		return err
	}

	login := strings.TrimSpace(c.Request().FormValue("login"))
	email := strings.TrimSpace(c.Request().FormValue("email"))
	password := c.Request().FormValue("password")

	vm := joinVM{
		Chrome:     h.view.Chrome(c, "Create your account"),
		Action:     route.Join(),
		LoginValue: login,
		EmailValue: email,
	}

	ok := true
	if !validLogin(login) {
		vm.LoginError = "Username may only contain alphanumeric characters or single hyphens, and cannot begin or end with a hyphen."
		ok = false
	} else if route.IsReservedTop(login) {
		// A reserved top-level name can never be a login: the dispatcher would
		// route /{login} to the front's own page and the profile would be
		// unreachable (spec 02 §2.3-2.4).
		vm.LoginError = "This name is reserved."
		ok = false
	}
	if email == "" || !strings.Contains(email, "@") {
		vm.EmailError = "Enter a valid email address."
		ok = false
	}
	if utf8.RuneCountInString(password) < 8 {
		vm.Error = "Password must be at least 8 characters."
		ok = false
	}
	if !ok {
		return h.render.Page(c, "auth/join", vm)
	}

	exists, err := h.store.UserLoginExists(c.Context(), login)
	if err != nil {
		return err
	}
	if exists {
		vm.LoginError = "That username is already taken. Please choose another."
		return h.render.Page(c, "auth/join", vm)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	pk, err := h.store.InsertUserWithPassword(c.Context(), login, email, string(hash))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			vm.LoginError = "That username is already taken. Please choose another."
			return h.render.Page(c, "auth/join", vm)
		}
		return err
	}

	h.sessions.Issue(c, pk, time.Now())
	return c.Redirect(http.StatusSeeOther, "/")
}

// LogoutForm renders the sign-out confirmation page. An anonymous request is
// redirected to /.
func (h *Handlers) LogoutForm(c *mizu.Ctx) error {
	if view.ViewerFrom(c.Context()) == nil {
		return c.Redirect(http.StatusSeeOther, "/")
	}
	return h.render.Page(c, "auth/logout", logoutVM{
		Chrome: h.view.Chrome(c, "Sign out"),
		Action: route.LogoutSession(),
	})
}

// LogoutSubmit clears the session cookie and redirects to /.
func (h *Handlers) LogoutSubmit(c *mizu.Ctx) error {
	h.sessions.Clear(c)
	return c.Redirect(http.StatusSeeOther, "/")
}

// safeReturn returns url if it is a safe same-origin return URL, otherwise "/".
// It blocks open redirects by rejecting anything with a host or scheme. Browsers
// treat a backslash after the authority cut like a forward slash, so "/\evil"
// and the percent-encoded "%5C" spellings are protocol-relative escapes in
// disguise; no Githome path ever contains a backslash, so any one of them, raw
// or encoded, anywhere in the URL means we fall back to "/" instead of risking
// a parser differential (spec 02 §5.9).
func safeReturn(url string) string {
	if url == "" {
		return "/"
	}
	// Must start with / and not be //something (protocol-relative) or have a scheme.
	if !strings.HasPrefix(url, "/") || strings.HasPrefix(url, "//") {
		return "/"
	}
	if strings.ContainsRune(url, '\\') || strings.Contains(strings.ToLower(url), "%5c") {
		return "/"
	}
	return url
}

var loginRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9\-]*[a-zA-Z0-9]$|^[a-zA-Z0-9]$`)

func validLogin(login string) bool {
	if len(login) < 1 || len(login) > 39 {
		return false
	}
	if strings.Contains(login, "--") {
		return false
	}
	return loginRE.MatchString(login)
}

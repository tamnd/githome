package route

import "net/url"

// auth.go holds the URL builders for the web auth flows: sign in, sign up, sign
// out, and the OAuth/device consent pages. They are pure string functions like
// the rest of fe/route. See implementation/06.

// Login is the sign-in form, /login.
func Login() string { return "/login" }

// LoginWithReturn is the sign-in form carrying the page to come back to after
// signing in, /login?return_to={url}. It is the bounce target for the
// function-private surfaces (settings, notifications) an anonymous request
// hits. The auth handlers validate return_to before honoring it (same-origin
// paths only), so the builder only escapes it.
func LoginWithReturn(returnTo string) string {
	if returnTo == "" || returnTo == "/" {
		return Login()
	}
	return Login() + "?return_to=" + url.QueryEscape(returnTo)
}

// LoginSession is the sign-in POST target, /login/session.
func LoginSession() string { return "/login/session" }

// Logout is the sign-out form, /logout.
func Logout() string { return "/logout" }

// LogoutSession is the sign-out POST target, /logout/session.
func LogoutSession() string { return "/logout/session" }

// Join is the sign-up form, /join.
func Join() string { return "/join" }

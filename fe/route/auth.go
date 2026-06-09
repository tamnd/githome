package route

// auth.go holds the URL builders for the web auth flows: sign in, sign up, sign
// out, and the OAuth/device consent pages. They are pure string functions like
// the rest of fe/route. See implementation/06.

// Login is the sign-in form, /login.
func Login() string { return "/login" }

// LoginSession is the sign-in POST target, /login/session.
func LoginSession() string { return "/login/session" }

// Logout is the sign-out form, /logout.
func Logout() string { return "/logout" }

// LogoutSession is the sign-out POST target, /logout/session.
func LogoutSession() string { return "/logout/session" }

// Join is the sign-up form, /join.
func Join() string { return "/join" }

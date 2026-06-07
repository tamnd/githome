package webmw

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
)

// cookieCSRF holds the per-browser CSRF token. It is not HttpOnly: the token is
// not a secret on its own, it is the second half of a double submit, and the
// rendered forms read its value from the server-injected hidden field, not from
// JavaScript. See implementation/06.
const cookieCSRF = "csrf_token"

// csrfFormField is the hidden input name the shell renders into every mutating
// form, so a no-JS form carries the token exactly as an htmx request does.
const csrfFormField = "_csrf"

// CSRF is the double-submit guard. On a safe request it ensures a token cookie
// exists and puts the token on the context for the forms to echo. On a mutating
// request it requires the submitted token to match the cookie, rejecting a
// mismatch with the rendered 403 page. It holds the render set so the rejection
// is a themed page rather than a bare status.
type CSRF struct {
	render *render.Set
}

// NewCSRF returns the guard, rendering rejections through r.
func NewCSRF(r *render.Set) *CSRF {
	return &CSRF{render: r}
}

// Middleware applies the guard.
func (g *CSRF) Middleware() mizu.Middleware {
	return func(next mizu.Handler) mizu.Handler {
		return func(c *mizu.Ctx) error {
			token := g.ensureToken(c)
			setCtx(view.WithCSRF(c.Context(), token), c)

			if isSafeMethod(c.Request().Method) {
				return next(c)
			}
			submitted := submittedToken(c)
			if submitted == "" || subtle.ConstantTimeCompare([]byte(submitted), []byte(token)) != 1 {
				return g.render.Forbidden(c, nil)
			}
			return next(c)
		}
	}
}

// ensureToken returns the request's CSRF token, minting and setting the cookie
// when none is present. A freshly minted token is set on the response so the same
// request's rendered forms and the next request's submission both see it.
func (g *CSRF) ensureToken(c *mizu.Ctx) string {
	if ck, err := c.Request().Cookie(cookieCSRF); err == nil && ck.Value != "" {
		return ck.Value
	}
	token := newToken()
	http.SetCookie(c.Writer(), &http.Cookie{
		Name:     cookieCSRF,
		Value:    token,
		Path:     "/",
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
	})
	return token
}

// submittedToken reads the token from the form field on a normal form post or
// from the header an htmx request sends.
func submittedToken(c *mizu.Ctx) string {
	if h := c.Request().Header.Get("X-CSRF-Token"); h != "" {
		return h
	}
	form, err := c.Form()
	if err != nil {
		return ""
	}
	return form.Get(csrfFormField)
}

// isSafeMethod reports whether the method only reads, so no token is required.
func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

// newToken returns 32 bytes of randomness, base64url encoded. crypto/rand cannot
// fail in practice on the supported platforms; a read error panics rather than
// returning a predictable token, which would weaken the guard silently.
func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("webmw: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

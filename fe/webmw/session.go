// Package webmw holds the Githome web front's middleware: the signed session
// cookie that resolves the viewer, the color-mode cookie, the CSRF guard, the
// one-shot flash cookie, and the panic recovery that renders the HTML error page.
// Each is a mizu.Middleware. They import fe/view to set the request-scoped values
// the view builder reads; the import stays one way (webmw imports view, not the
// reverse). See implementation/06.
package webmw

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/view"
)

// ViewerLookup resolves a user primary key to the shell's viewer model. The mount
// wiring adapts domain.UserService.Viewer to this shape, which keeps webmw free
// of a domain import.
type ViewerLookup func(ctx context.Context, userPK int64) (*view.Viewer, error)

// Sessions issues and verifies the front's signed session cookie and exposes the
// middleware that loads the viewer. The cookie carries the user primary key and
// an expiry, signed with HMAC-SHA256 over the front's session key, so a tampered
// or expired cookie resolves to anonymous rather than to a user. Login and logout
// (a later milestone) reuse Issue and Clear.
type Sessions struct {
	key        []byte
	cookieName string
	ttl        time.Duration
	lookup     ViewerLookup
}

// DefaultSessionCookie is the cookie name the front uses for its session. It is
// distinct from any API credential, which the front never reads.
const DefaultSessionCookie = "githome_session"

// NewSessions returns a Sessions signing with key (the front's session secret,
// at least 32 bytes) and resolving viewers through lookup. ttl bounds how long a
// session cookie stays valid; a zero ttl defaults to thirty days.
func NewSessions(key []byte, ttl time.Duration, lookup ViewerLookup) *Sessions {
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	return &Sessions{key: key, cookieName: DefaultSessionCookie, ttl: ttl, lookup: lookup}
}

// Middleware loads the viewer for the request from the session cookie and stores
// it on the context. A missing, malformed or expired cookie, or a lookup that
// finds no user, leaves the viewer nil (anonymous); none of those is an error, so
// public pages render either way. A lookup that errors for an infrastructure
// reason is propagated so the recover layer can turn it into a 500.
func (s *Sessions) Middleware() mizu.Middleware {
	return func(next mizu.Handler) mizu.Handler {
		return func(c *mizu.Ctx) error {
			ck, err := c.Request().Cookie(s.cookieName)
			if err != nil || ck.Value == "" {
				return next(c)
			}
			pk, ok := s.verify(ck.Value, time.Now())
			if !ok {
				return next(c)
			}
			v, err := s.lookup(c.Context(), pk)
			if err != nil {
				return err
			}
			if v != nil {
				setCtx(view.WithViewer(c.Context(), v), c)
			}
			return next(c)
		}
	}
}

// Issue writes the session cookie for userPK. It is called by the login handler
// once credentials check out.
func (s *Sessions) Issue(c *mizu.Ctx, userPK int64, now time.Time) {
	exp := now.Add(s.ttl)
	value := s.sign(userPK, exp)
	http.SetCookie(c.Writer(), &http.Cookie{
		Name:     s.cookieName,
		Value:    value,
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
	})
}

// Clear deletes the session cookie. It is called by the logout handler.
func (s *Sessions) Clear(c *mizu.Ctx) {
	http.SetCookie(c.Writer(), &http.Cookie{
		Name:     s.cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
	})
}

// sign returns the cookie value: "<pk>.<expUnix>.<mac>", where mac is the
// base64url HMAC-SHA256 of "<pk>.<expUnix>" under the session key.
func (s *Sessions) sign(userPK int64, exp time.Time) string {
	payload := strconv.FormatInt(userPK, 10) + "." + strconv.FormatInt(exp.Unix(), 10)
	return payload + "." + s.mac(payload)
}

// verify checks the signature and the expiry, returning the user pk on success.
// It compares the MAC in constant time so a forged cookie cannot be probed.
func (s *Sessions) verify(value string, now time.Time) (int64, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return 0, false
	}
	payload := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(parts[2]), []byte(s.mac(payload))) {
		return 0, false
	}
	pk, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || pk <= 0 {
		return 0, false
	}
	expUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || now.Unix() >= expUnix {
		return 0, false
	}
	return pk, true
}

func (s *Sessions) mac(payload string) string {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

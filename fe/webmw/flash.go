package webmw

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/view"
)

// cookieFlash carries one-shot flash messages between a mutation and the page
// that reports its outcome. The value is signed with the front's session key so a
// crafted cookie cannot put attacker-chosen text in the flash region; the
// template escapes the text either way, but signing keeps the channel honest. See
// implementation/06.
const cookieFlash = "githome_flash"

// Flash reads, verifies and clears the flash cookie, and lets a handler set a new
// flash before redirecting. One Flash is constructed at boot with the session key
// and shared.
type Flash struct {
	key []byte
}

// NewFlash returns a Flash signing with key (the front's session secret).
func NewFlash(key []byte) *Flash {
	return &Flash{key: key}
}

// Middleware reads the flash cookie, puts the verified messages on the context
// for the shell to render, and clears the cookie so each flash shows once. A
// missing or tampered cookie yields no flashes and is not an error.
func (f *Flash) Middleware() mizu.Middleware {
	return func(next mizu.Handler) mizu.Handler {
		return func(c *mizu.Ctx) error {
			ck, err := c.Request().Cookie(cookieFlash)
			if err != nil || ck.Value == "" {
				return next(c)
			}
			msgs, ok := f.decode(ck.Value)
			if ok && len(msgs) > 0 {
				setCtx(view.WithFlashes(c.Context(), msgs), c)
			}
			// Clear regardless: a present cookie has now been consumed.
			f.clear(c)
			return next(c)
		}
	}
}

// Add appends a flash to be shown on the next page. It reads any flash already
// staged on this response (or the inbound cookie) so several adds before a
// redirect accumulate rather than overwrite.
func (f *Flash) Add(c *mizu.Ctx, kind, message string) {
	var msgs []view.Flash
	if ck, err := c.Request().Cookie(cookieFlash); err == nil && ck.Value != "" {
		if prev, ok := f.decode(ck.Value); ok {
			msgs = prev
		}
	}
	msgs = append(msgs, view.Flash{Kind: kind, Message: message})
	http.SetCookie(c.Writer(), &http.Cookie{
		Name:     cookieFlash,
		Value:    f.encode(msgs),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
	})
}

func (f *Flash) clear(c *mizu.Ctx) {
	http.SetCookie(c.Writer(), &http.Cookie{
		Name:     cookieFlash,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
	})
}

// encode serializes the messages to "<base64 json>.<mac>".
func (f *Flash) encode(msgs []view.Flash) string {
	b, err := json.Marshal(msgs)
	if err != nil {
		// view.Flash is two strings, so marshaling cannot fail; guard anyway.
		return ""
	}
	payload := base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + f.mac(payload)
}

// decode verifies the signature and returns the messages.
func (f *Flash) decode(value string) ([]view.Flash, bool) {
	dot := strings.LastIndexByte(value, '.')
	if dot < 0 {
		return nil, false
	}
	payload, sig := value[:dot], value[dot+1:]
	if !hmac.Equal([]byte(sig), []byte(f.mac(payload))) {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, false
	}
	var msgs []view.Flash
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return nil, false
	}
	return msgs, true
}

func (f *Flash) mac(payload string) string {
	h := hmac.New(sha256.New, f.key)
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

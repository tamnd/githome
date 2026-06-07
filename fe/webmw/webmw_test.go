package webmw

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/assets"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func ctxFor(method, target string, mutate func(*http.Request)) (*mizu.Ctx, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, nil)
	if mutate != nil {
		mutate(req)
	}
	return mizu.NewCtx(rec, req, nil), rec
}

// run invokes mw around a handler that records whether it ran, returning the
// terminal context so the test can read what the middleware stored.
func run(mw mizu.Middleware, c *mizu.Ctx) (ran bool, err error) {
	h := mw(func(*mizu.Ctx) error { ran = true; return nil })
	err = h(c)
	return ran, err
}

func TestSessionSignVerifyRoundTrip(t *testing.T) {
	s := NewSessions(testKey, time.Hour, nil)
	now := time.Unix(1_700_000_000, 0)
	value := s.sign(42, now.Add(time.Hour))

	pk, ok := s.verify(value, now)
	if !ok || pk != 42 {
		t.Fatalf("verify = (%d, %v), want (42, true)", pk, ok)
	}
}

func TestSessionVerifyRejectsTamperAndExpiry(t *testing.T) {
	s := NewSessions(testKey, time.Hour, nil)
	now := time.Unix(1_700_000_000, 0)
	value := s.sign(42, now.Add(time.Hour))

	// Flip the payload but keep the old signature.
	tampered := strings.Replace(value, "42.", "43.", 1)
	if _, ok := s.verify(tampered, now); ok {
		t.Error("a tampered cookie must not verify")
	}

	// A cookie signed with a different key must not verify.
	other := NewSessions([]byte("ffffffffffffffffffffffffffffffff"), time.Hour, nil)
	if _, ok := s.verify(other.sign(42, now.Add(time.Hour)), now); ok {
		t.Error("a cookie signed with another key must not verify")
	}

	// An expired cookie must not verify.
	expired := s.sign(42, now.Add(-time.Minute))
	if _, ok := s.verify(expired, now); ok {
		t.Error("an expired cookie must not verify")
	}
}

func TestSessionMiddlewareResolvesViewer(t *testing.T) {
	s := NewSessions(testKey, time.Hour, func(_ context.Context, _ int64) (*view.Viewer, error) {
		return &view.Viewer{Login: "octocat"}, nil
	})
	value := s.sign(7, time.Now().Add(time.Hour))
	c, _ := ctxFor(http.MethodGet, "/", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: DefaultSessionCookie, Value: value})
	})

	var got *view.Viewer
	h := s.Middleware()(func(c *mizu.Ctx) error {
		got = view.ViewerFrom(c.Context())
		return nil
	})
	if err := h(c); err != nil {
		t.Fatalf("middleware: %v", err)
	}
	if got == nil || got.Login != "octocat" {
		t.Fatalf("viewer = %+v, want octocat", got)
	}
}

func TestSessionMiddlewareAnonymousWithoutCookie(t *testing.T) {
	s := NewSessions(testKey, time.Hour, func(_ context.Context, _ int64) (*view.Viewer, error) {
		t.Fatal("lookup must not run without a valid cookie")
		return nil, nil
	})
	c, _ := ctxFor(http.MethodGet, "/", nil)
	var got *view.Viewer
	h := s.Middleware()(func(c *mizu.Ctx) error {
		got = view.ViewerFrom(c.Context())
		return nil
	})
	if err := h(c); err != nil {
		t.Fatalf("middleware: %v", err)
	}
	if got != nil {
		t.Fatalf("viewer = %+v, want nil", got)
	}
}

func TestColorModeValidatesCookies(t *testing.T) {
	c, _ := ctxFor(http.MethodGet, "/", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: cookieColorMode, Value: "dark"})
		r.AddCookie(&http.Cookie{Name: cookieLightTheme, Value: "light_high_contrast"})
		r.AddCookie(&http.Cookie{Name: cookieDarkTheme, Value: "dark_dimmed"})
	})
	var got view.ColorMode
	h := ColorMode()(func(c *mizu.Ctx) error {
		got = view.ColorModeFrom(c.Context())
		return nil
	})
	if err := h(c); err != nil {
		t.Fatalf("middleware: %v", err)
	}
	if got.Mode != "dark" || got.Light != "light_high_contrast" || got.Dark != "dark_dimmed" {
		t.Fatalf("color mode = %+v", got)
	}
}

func TestColorModeRejectsInvalidAndCrossSlot(t *testing.T) {
	c, _ := ctxFor(http.MethodGet, "/", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: cookieColorMode, Value: "neon"})         // invalid mode
		r.AddCookie(&http.Cookie{Name: cookieLightTheme, Value: "dark_dimmed"}) // dark theme in light slot
		r.AddCookie(&http.Cookie{Name: cookieDarkTheme, Value: "made_up"})      // unknown theme
	})
	var got view.ColorMode
	h := ColorMode()(func(c *mizu.Ctx) error {
		got = view.ColorModeFrom(c.Context())
		return nil
	})
	_ = h(c)
	want := view.DefaultColorMode()
	if got != want {
		t.Fatalf("color mode = %+v, want default %+v", got, want)
	}
}

func newRenderSet(t *testing.T) *render.Set {
	t.Helper()
	s, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	return s
}

func TestCSRFAllowsSafeAndSetsToken(t *testing.T) {
	g := NewCSRF(newRenderSet(t))
	c, rec := ctxFor(http.MethodGet, "/", nil)
	var token string
	h := g.Middleware()(func(c *mizu.Ctx) error {
		token = view.CSRFFrom(c.Context())
		return nil
	})
	if err := h(c); err != nil {
		t.Fatalf("middleware: %v", err)
	}
	if token == "" {
		t.Fatal("a safe request should mint and expose a CSRF token")
	}
	if !strings.Contains(rec.Header().Get("Set-Cookie"), cookieCSRF) {
		t.Fatal("a safe request should set the CSRF cookie")
	}
}

func TestCSRFRejectsMismatchedPost(t *testing.T) {
	g := NewCSRF(newRenderSet(t))
	c, rec := ctxFor(http.MethodPost, "/settings", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: cookieCSRF, Value: "realtoken"})
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Body = http.NoBody
	})
	// Form carries the wrong token.
	c.Request().PostForm = url.Values{csrfFormField: {"wrongtoken"}}

	ran, err := run(g.Middleware(), c)
	if err != nil {
		t.Fatalf("middleware: %v", err)
	}
	if ran {
		t.Fatal("a mismatched token must not reach the handler")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestCSRFAcceptsMatchingPost(t *testing.T) {
	g := NewCSRF(newRenderSet(t))
	c, _ := ctxFor(http.MethodPost, "/settings", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: cookieCSRF, Value: "realtoken"})
	})
	c.Request().PostForm = url.Values{csrfFormField: {"realtoken"}}

	ran, err := run(g.Middleware(), c)
	if err != nil {
		t.Fatalf("middleware: %v", err)
	}
	if !ran {
		t.Fatal("a matching token must reach the handler")
	}
}

func TestFlashRoundTrip(t *testing.T) {
	f := NewFlash(testKey)
	msgs := []view.Flash{{Kind: "success", Message: "Saved"}, {Kind: "error", Message: "Nope"}}
	encoded := f.encode(msgs)
	got, ok := f.decode(encoded)
	if !ok || len(got) != 2 || got[0].Message != "Saved" || got[1].Kind != "error" {
		t.Fatalf("decode = (%+v, %v)", got, ok)
	}

	// A tampered flash cookie is ignored.
	if _, ok := f.decode(encoded + "x"); ok {
		t.Error("a tampered flash cookie must not decode")
	}
}

func TestFlashMiddlewareReadsAndClears(t *testing.T) {
	f := NewFlash(testKey)
	value := f.encode([]view.Flash{{Kind: "info", Message: "Hello"}})
	c, rec := ctxFor(http.MethodGet, "/", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: cookieFlash, Value: value})
	})
	var got []view.Flash
	h := f.Middleware()(func(c *mizu.Ctx) error {
		got = view.FlashesFrom(c.Context())
		return nil
	})
	if err := h(c); err != nil {
		t.Fatalf("middleware: %v", err)
	}
	if len(got) != 1 || got[0].Message != "Hello" {
		t.Fatalf("flashes = %+v", got)
	}
	// The cookie is cleared so the flash shows once.
	sc := rec.Header().Get("Set-Cookie")
	if !strings.Contains(sc, cookieFlash) || !strings.Contains(sc, "Max-Age=0") {
		t.Fatalf("flash cookie not cleared: %q", sc)
	}
}

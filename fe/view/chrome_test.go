package view

import (
	"net/http/httptest"
	"testing"

	"github.com/go-mizu/mizu"
)

// ctxWith builds a mizu.Ctx whose request context carries the given middleware
// values, the way the real middleware would have set them.
func ctxWith(setup func(c *mizu.Ctx)) *mizu.Ctx {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/octocat/hello?tab=stars", nil)
	c := mizu.NewCtx(rec, req, nil)
	if setup != nil {
		setup(c)
	}
	return c
}

func TestChromeReadsContext(t *testing.T) {
	b := NewBuilder("Githome")
	c := ctxWith(func(c *mizu.Ctx) {
		r := c.Request()
		ctx := r.Context()
		ctx = WithViewer(ctx, &Viewer{Login: "octocat"})
		ctx = WithColorMode(ctx, ColorMode{Mode: "dark", Light: "light", Dark: "dark_dimmed"})
		ctx = WithCSRF(ctx, "tok")
		ctx = WithFlashes(ctx, []Flash{{Kind: "success", Message: "Saved"}})
		*r = *r.WithContext(ctx)
	})

	ch := b.Chrome(c, "Hello")
	if ch.Title != "Hello" || ch.SiteName != "Githome" {
		t.Errorf("title/site = %q/%q", ch.Title, ch.SiteName)
	}
	if ch.Viewer == nil || ch.Viewer.Login != "octocat" {
		t.Errorf("viewer = %+v", ch.Viewer)
	}
	if ch.ColorMode.Mode != "dark" || ch.ColorMode.Dark != "dark_dimmed" {
		t.Errorf("color mode = %+v", ch.ColorMode)
	}
	if ch.CSRFToken != "tok" {
		t.Errorf("csrf = %q", ch.CSRFToken)
	}
	if len(ch.Flashes) != 1 || ch.Flashes[0].Message != "Saved" {
		t.Errorf("flashes = %+v", ch.Flashes)
	}
	// CurrentPath is the full request URI, so a sign-in link can return here.
	if ch.CurrentPath != "/octocat/hello?tab=stars" {
		t.Errorf("current path = %q", ch.CurrentPath)
	}
}

func TestChromeDefaultsWhenContextEmpty(t *testing.T) {
	b := NewBuilder("")
	ch := b.Chrome(ctxWith(nil), "")

	if ch.SiteName != "Githome" {
		t.Errorf("empty site name should default to Githome, got %q", ch.SiteName)
	}
	if ch.Viewer != nil {
		t.Errorf("viewer should be nil without a session, got %+v", ch.Viewer)
	}
	// Color mode falls back to the OS-following default with no cookie.
	if ch.ColorMode != DefaultColorMode() {
		t.Errorf("color mode = %+v, want default", ch.ColorMode)
	}
}

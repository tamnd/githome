package render

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/assets"
)

// These tests are the rendered-content arm of the oracle for the F0 shell: they
// render the real templates against the real embedded asset manifest and pin the
// invariants the spec promises, the no-flash theme attributes, the resolved
// hashed asset URLs, the signed-in versus anonymous shell, the htmx fragment
// contract, and the themed error pages. See implementation/15 sections 2 and 3.

// tChrome mirrors the field names the base layout reads. The test defines its own
// shape rather than importing fe/view so the render package's tests stay
// independent of the view package; html/template binds by field name, so a
// structurally matching struct renders identically.
type tChrome struct {
	Title       string
	SiteName    string
	ColorMode   tColorMode
	Viewer      *tViewer
	CSRFToken   string
	CurrentPath string
	Flashes     []tFlash
	HideAuth    bool
}

type tColorMode struct{ Mode, Light, Dark string }
type tViewer struct{ Login, Name, AvatarURL string }
type tFlash struct{ Kind, Message string }

// tHome mirrors view.HomeVM the same way: the dashboard fields the signed-in
// branch reads, left at their zero values so the test pins the shell, not the
// dashboard content (the fe/web/home tests own that).
type tHome struct {
	Chrome     tChrome
	Repos      []tHomeRepo
	ReposURL   string
	NewRepoURL string
	Feed       []tFeedItem
	FeedEmpty  bool
}

type tHomeRepo struct {
	FullName, URL string
	Private       bool
}

type tFeedItem struct {
	Icon, ActorLogin, ActorURL, Verb         string
	RepoFullName, RepoURL, Target, TargetURL string
	CreatedAt, CreatedISO                    string
}

func newTestSet(t *testing.T) *Set {
	t.Helper()
	s, err := New(assets.FS(), false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func newCtx(method, target string) (*mizu.Ctx, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, nil)
	return mizu.NewCtx(rec, req, nil), rec
}

func anonChrome() tChrome {
	return tChrome{
		SiteName:  "Githome",
		ColorMode: tColorMode{Mode: "auto", Light: "light", Dark: "dark"},
	}
}

func TestPageHomeAnonymous(t *testing.T) {
	s := newTestSet(t)
	c, rec := newCtx(http.MethodGet, "/")
	if err := s.Page(c, "home/index", tHome{Chrome: anonChrome()}); err != nil {
		t.Fatalf("Page: %v", err)
	}
	body := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	mustContain(t, body, "<!DOCTYPE html>")
	// No-flash theme attributes drive the active theme from CSS alone.
	mustContain(t, body, `data-color-mode="auto"`)
	mustContain(t, body, `data-light-theme="light"`)
	mustContain(t, body, `data-dark-theme="dark"`)
	// The asset URL is the content-hashed path from the manifest, not the logical
	// name, so a cache-busting deploy never serves a stale bundle.
	mustContain(t, body, `href="/assets/app.`)
	mustContain(t, body, `.css"`)
	mustContain(t, body, `src="/assets/app.`)
	// Anonymous viewers see the sign-in call to action, not the avatar menu.
	mustContain(t, body, "Sign in")
	if strings.Contains(body, "Sign out") {
		t.Error("anonymous page must not show the sign-out control")
	}
}

func TestPageFlashBanners(t *testing.T) {
	s := newTestSet(t)
	ch := anonChrome()
	ch.Flashes = []tFlash{{Kind: "success", Message: "Repository created."}, {Kind: "error", Message: "Nope."}}
	c, rec := newCtx(http.MethodGet, "/")
	if err := s.Page(c, "home/index", tHome{Chrome: ch}); err != nil {
		t.Fatalf("Page: %v", err)
	}
	body := rec.Body.String()

	// Each kind leads with its role octicon and wraps the text in the message
	// span the flex layout stretches.
	mustContain(t, body, `class="flash flash-success"`)
	mustContain(t, body, `class="flash flash-error"`)
	mustContain(t, body, assets.Icons["check-circle"].Body)
	mustContain(t, body, assets.Icons["stop"].Body)
	mustContain(t, body, `<span class="flash-message">Repository created.</span>`)
	// The dismiss button ships in the markup for app.ts to wire; CSS hides it
	// until the js-enhanced flag confirms the wiring ran.
	mustContain(t, body, `data-flash-close aria-label="Dismiss this message"`)
}

func TestPageHomeSignedIn(t *testing.T) {
	s := newTestSet(t)
	ch := anonChrome()
	ch.Viewer = &tViewer{Login: "octocat"}
	ch.CSRFToken = "tok123"
	c, rec := newCtx(http.MethodGet, "/")
	if err := s.Page(c, "home/index", tHome{Chrome: ch}); err != nil {
		t.Fatalf("Page: %v", err)
	}
	body := rec.Body.String()

	mustContain(t, body, "Welcome back, octocat")
	mustContain(t, body, "Sign out")
	// The sign-out form carries the CSRF token as a hidden field, so it works with
	// scripting off.
	mustContain(t, body, `name="_csrf" value="tok123"`)
	// The signed-in header links to the viewer's profile.
	mustContain(t, body, `href="/octocat"`)
}

func TestPageETagRevalidation(t *testing.T) {
	s := newTestSet(t)
	render := func(inm string) (*httptest.ResponseRecorder, string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if inm != "" {
			req.Header.Set("If-None-Match", inm)
		}
		if err := s.Page(mizu.NewCtx(rec, req, nil), "home/index", tHome{Chrome: anonChrome()}); err != nil {
			t.Fatalf("Page: %v", err)
		}
		return rec, rec.Header().Get("ETag")
	}

	// A fresh render carries the validator and the revalidate-always policy.
	first, etag := render("")
	if etag == "" || !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) {
		t.Fatalf("ETag = %q, want a quoted strong validator", etag)
	}
	if cc := first.Header().Get("Cache-Control"); cc != "private, no-cache" {
		t.Errorf("Cache-Control = %q, want private, no-cache", cc)
	}
	if first.Code != http.StatusOK || first.Body.Len() == 0 {
		t.Fatalf("fresh render: code=%d bodyLen=%d", first.Code, first.Body.Len())
	}

	// The same page hashes to the same tag, so the revisit ships no body.
	hit, hitTag := render(etag)
	if hit.Code != http.StatusNotModified {
		t.Fatalf("matching If-None-Match: code = %d, want 304", hit.Code)
	}
	if hit.Body.Len() != 0 {
		t.Errorf("304 must carry no body, got %d bytes", hit.Body.Len())
	}
	if hitTag != etag {
		t.Errorf("304 ETag = %q, want %q", hitTag, etag)
	}

	// List and weak forms still match; a stale tag gets the full page again.
	if rec, _ := render(`"stale", W/` + etag); rec.Code != http.StatusNotModified {
		t.Errorf("list with weak match: code = %d, want 304", rec.Code)
	}
	if rec, _ := render("*"); rec.Code != http.StatusNotModified {
		t.Errorf("wildcard: code = %d, want 304", rec.Code)
	}
	if rec, _ := render(`"stale"`); rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Errorf("stale tag: code=%d bodyLen=%d, want a full 200", rec.Code, rec.Body.Len())
	}

	// 304 is a GET/HEAD answer only.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("If-None-Match", etag)
	if err := s.Page(mizu.NewCtx(rec, req, nil), "home/index", tHome{Chrome: anonChrome()}); err != nil {
		t.Fatalf("Page: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("POST with matching tag: code = %d, want 200", rec.Code)
	}
}

func TestFragmentSkipsETag(t *testing.T) {
	s := newTestSet(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("HX-Request", "true")
	if err := s.Page(mizu.NewCtx(rec, req, nil), "home/index", tHome{Chrome: anonChrome()}); err != nil {
		t.Fatalf("Page: %v", err)
	}
	// An htmx swap is part of a page, not a cacheable document of its own.
	if got := rec.Header().Get("ETag"); got != "" {
		t.Errorf("fragment ETag = %q, want none", got)
	}
}

func TestFragmentOmitsLayout(t *testing.T) {
	s := newTestSet(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("HX-Request", "true")
	c := mizu.NewCtx(rec, req, nil)

	if err := s.Page(c, "home/index", tHome{Chrome: anonChrome()}); err != nil {
		t.Fatalf("Page: %v", err)
	}
	body := rec.Body.String()

	// A fragment is the content only: no document shell, but the content is there.
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("htmx fragment must not include the document shell")
	}
	if strings.Contains(body, "<header") {
		t.Error("htmx fragment must not include the app header")
	}
	mustContain(t, body, "blankslate")
}

func TestNotFoundIsThemedAnd404(t *testing.T) {
	s := newTestSet(t)
	c, rec := newCtx(http.MethodGet, "/does-not-exist")
	if err := s.NotFound(c); err != nil {
		t.Fatalf("NotFound: %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	// The error page is a full, themed page (fallback chrome) so it keeps the
	// shell and the no-flash theme attributes.
	mustContain(t, body, "<!DOCTYPE html>")
	mustContain(t, body, `data-color-mode="auto"`)
	mustContain(t, body, "404")
	// The 404 copy is deliberately identical for missing and private resources.
	mustContain(t, body, "not the web page you are looking for")
}

func TestServerErrorIs500(t *testing.T) {
	s := newTestSet(t)
	c, rec := newCtx(http.MethodGet, "/boom")
	if err := s.ServerError(c, nil); err != nil {
		t.Fatalf("ServerError: %v", err)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	mustContain(t, rec.Body.String(), "500")
}

// mustOcticon unwraps the helper for the tests; a malformed argument list is a
// test bug, not a case under test.
func mustOcticon(t *testing.T, name string, args ...any) string {
	t.Helper()
	got, err := octicon(name, args...)
	if err != nil {
		t.Fatalf("octicon(%q, %v): %v", name, args, err)
	}
	return string(got)
}

func TestOcticonKnownAndMissing(t *testing.T) {
	known := mustOcticon(t, "mark-github", 16)
	if !strings.Contains(known, "<svg") || !strings.Contains(known, "viewBox=\"0 0 16 16\"") {
		t.Errorf("known icon should render an svg: %q", known)
	}
	if !strings.Contains(known, assets.Icons["mark-github"].Body) {
		t.Error("known icon should embed its registered path body")
	}

	// An unknown icon renders a visible placeholder naming the miss, never an
	// empty string or the raw input, so a typo is caught in review.
	missing := mustOcticon(t, "no-such-icon", 16)
	if !strings.Contains(missing, "octicon-missing") || !strings.Contains(missing, "no-such-icon") {
		t.Errorf("missing icon should render a labeled placeholder: %q", missing)
	}

	// A zero size falls back to 16 rather than rendering a zero-sized glyph,
	// and the size argument is optional outright.
	if !strings.Contains(mustOcticon(t, "repo", 0), `width="16"`) {
		t.Error("zero size should fall back to 16")
	}
	if !strings.Contains(mustOcticon(t, "repo"), `width="16"`) {
		t.Error("omitted size should fall back to 16")
	}
}

func TestOcticonGridsAndLabel(t *testing.T) {
	// A 24px render uses the 24-grid drawing when the set has one, so big
	// renders are not upscaled 16-grid glyphs.
	big := mustOcticon(t, "mark-github", 24)
	if !strings.Contains(big, `viewBox="0 0 24 24"`) {
		t.Errorf("24px render should use the 24-grid drawing: %q", big)
	}
	if !strings.Contains(big, assets.Icons24["mark-github"].Body) {
		t.Error("24px render should embed the 24-grid body")
	}

	// An icon that only exists on the 16 grid still renders at any size.
	if _, ok := assets.Icons24["feed-issue-reopen"]; ok {
		t.Fatal("test premise broken: feed-issue-reopen now has a 24-grid drawing, pick another icon")
	}
	only16 := mustOcticon(t, "feed-issue-reopen", 24)
	if !strings.Contains(only16, `viewBox="0 0 17 16"`) {
		t.Errorf("a 16-grid-only icon keeps its own viewBox at 24px: %q", only16)
	}

	// A non-square glyph keeps its aspect ratio: the pixel width scales with
	// the grid width instead of assuming a square.
	wordmark := mustOcticon(t, "logo-github", 16)
	wm := assets.Icons["logo-github"]
	if want := fmt.Sprintf(`width="%d" height="16" viewBox="0 0 %d %d"`, 16*wm.Width/wm.Height, wm.Width, wm.Height); !strings.Contains(wordmark, want) {
		t.Errorf("wordmark glyph should scale by its own grid, want %s in %q", want, wordmark)
	}

	// A decorative icon is aria-hidden; a labeled one reads as an image.
	if !strings.Contains(mustOcticon(t, "repo", 16), `aria-hidden="true"`) {
		t.Error("unlabeled icon must be aria-hidden")
	}
	labeled := mustOcticon(t, "repo", 16, "Repository")
	for _, want := range []string{`role="img"`, `aria-label="Repository"`, `<title>Repository</title>`} {
		if !strings.Contains(labeled, want) {
			t.Errorf("labeled icon is missing %s: %q", want, labeled)
		}
	}
	if strings.Contains(labeled, "aria-hidden") {
		t.Errorf("labeled icon must not be aria-hidden: %q", labeled)
	}

	// A label can also come before the size; argument order is free.
	if got := mustOcticon(t, "repo", "Repository", 24); !strings.Contains(got, `height="24"`) {
		t.Errorf("label-then-size order should still set the size: %q", got)
	}

	// A malformed argument fails the render instead of degrading silently.
	if _, err := octicon("repo", 1.5); err == nil {
		t.Error("a non-int, non-string argument must error")
	}
}

func TestColorModeAttrsEscapes(t *testing.T) {
	got := string(colorModeAttrs("auto", "light", `da"rk`))
	if strings.Contains(got, `da"rk`) {
		t.Errorf("attribute value must be escaped: %q", got)
	}
}

func TestIsFragment(t *testing.T) {
	// Header form.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("HX-Request", "true")
	if !IsFragment(mizu.NewCtx(httptest.NewRecorder(), req, nil)) {
		t.Error("HX-Request: true should be a fragment")
	}
	// Query form.
	c, _ := newCtx(http.MethodGet, "/?_fragment=1")
	if !IsFragment(c) {
		t.Error("?_fragment=1 should be a fragment")
	}
	// Plain navigation is a full page.
	c, _ = newCtx(http.MethodGet, "/")
	if IsFragment(c) {
		t.Error("a plain request should render the full page")
	}
}

func mustContain(t *testing.T, body, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Errorf("rendered output missing %q", want)
	}
}

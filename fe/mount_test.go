package fe_test

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe"
	"github.com/tamnd/githome/fe/assets"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

// buildServer wires the web front exactly as the binary does and returns an
// httptest server. lookup is the viewer resolver; pass one that returns a viewer
// to simulate a signed-in session, or nil for an always-anonymous front.
func buildServer(t *testing.T, lookup webmw.ViewerLookup) (*httptest.Server, *webmw.Sessions) {
	t.Helper()
	rs, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	if lookup == nil {
		lookup = func(context.Context, int64) (*view.Viewer, error) { return nil, nil }
	}
	sessions := webmw.NewSessions(testKey, time.Hour, lookup)
	root := mizu.NewRouter()
	handler := fe.Mount(root, fe.Deps{
		Render:   rs,
		View:     view.NewBuilder("Githome"),
		Sessions: sessions,
		CSRF:     webmw.NewCSRF(rs),
		Flash:    webmw.NewFlash(testKey),
		Logger:   nil,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, sessions
}

func get(t *testing.T, srv *httptest.Server, path string, cookies ...*http.Cookie) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	// Do not follow redirects, so the test sees the front's own responses.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, string(body)
}

func TestHomeAnonymousNoJS(t *testing.T) {
	srv, _ := buildServer(t, nil)
	resp, body := get(t, srv, "/")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q", ct)
	}
	// The page is a full document with the theme attributes and the sign-in CTA,
	// rendered with no JavaScript involved.
	for _, want := range []string{"<!DOCTYPE html>", `data-color-mode=`, "Sign in", "Githome"} {
		if !strings.Contains(body, want) {
			t.Errorf("home page missing %q", want)
		}
	}
	// A CSRF cookie is planted on the first GET so a later form post can carry it.
	if !hasSetCookie(resp, "csrf_token") {
		t.Error("home GET should set the CSRF cookie")
	}
}

func TestHomeSignedIn(t *testing.T) {
	lookup := func(_ context.Context, pk int64) (*view.Viewer, error) {
		if pk != 7 {
			return nil, nil
		}
		return &view.Viewer{Login: "octocat"}, nil
	}
	srv, sessions := buildServer(t, lookup)
	cookie := mintSession(t, sessions, 7)

	resp, body := get(t, srv, "/", cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Welcome back, octocat") {
		t.Error("signed-in home should greet the viewer")
	}
	if !strings.Contains(body, "Sign out") {
		t.Error("signed-in home should show the sign-out control")
	}
}

func TestUnknownPathRendersThemed404(t *testing.T) {
	srv, _ := buildServer(t, nil)
	// None of these is mounted: a repo sub-page the front does not serve yet, a
	// settings section that does not exist, and a top-level path no route owns.
	// Each renders the full themed 404, never the mux's plain-text one.
	for _, path := range []string{"/octocat/repo/wiki", "/settings/no-such-section", "/no-such-page"} {
		resp, body := get(t, srv, path)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s content-type = %q, want text/html", path, ct)
		}
		// The 404 is a full page with the shell chrome around it.
		for _, want := range []string{"<!DOCTYPE html>", `data-color-mode=`, "Githome", "This is not the web page"} {
			if !strings.Contains(body, want) {
				t.Errorf("404 page for %s missing %q", path, want)
			}
		}
	}
}

func TestUnknownAPIPathStaysJSON(t *testing.T) {
	srv, _ := buildServer(t, nil)
	// The REST surface leaves the root 404 to the front when both share the
	// router, so an unknown /api path must keep the GitHub-shaped JSON body
	// rather than an HTML page.
	resp, body := get(t, srv, "/api/v3/no-such-endpoint")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	if !strings.Contains(body, `"Not Found"`) {
		t.Errorf("API 404 body = %q, want the GitHub-shaped message", body)
	}
}

func TestTrailingSlashRedirects(t *testing.T) {
	srv, _ := buildServer(t, nil)
	cases := []struct{ path, want string }{
		{"/octocat/repo/", "/octocat/repo"},
		{"/login/", "/login"},
		{"/octocat/repo/?tab=readme", "/octocat/repo?tab=readme"},
	}
	// A path with doubled slashes is cleaned by the mux's own redirect first,
	// so only the canonical single-trailing-slash form is asserted here.
	for _, tc := range cases {
		resp, _ := get(t, srv, tc.path)
		if resp.StatusCode != http.StatusMovedPermanently {
			t.Errorf("GET %s = %d, want 301", tc.path, resp.StatusCode)
			continue
		}
		if got := resp.Header.Get("Location"); got != tc.want {
			t.Errorf("GET %s Location = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestAnonymousAuthOnlyRoutesBounceToLogin(t *testing.T) {
	srv, _ := buildServer(t, nil)
	// The function-private surfaces (settings, notifications) exist for every
	// account, so an anonymous request is bounced to the sign-in form with
	// return_to instead of a 404 that would pretend the page is not there.
	for _, path := range []string{"/settings/profile", "/notifications"} {
		resp, _ := get(t, srv, path)
		if resp.StatusCode != http.StatusFound {
			t.Errorf("anonymous GET %s = %d, want 302", path, resp.StatusCode)
			continue
		}
		want := "/login?return_to=" + strings.ReplaceAll(path, "/", "%2F")
		if got := resp.Header.Get("Location"); got != want {
			t.Errorf("anonymous GET %s Location = %q, want %q", path, got, want)
		}
	}
}

func TestWrongMethodRendersThemed405(t *testing.T) {
	srv, _ := buildServer(t, nil)
	// /settings/keys is GET-only, so a POST is a method mismatch the mux
	// detects. The page must be the themed 405 with the mux's Allow header
	// still on it.
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/settings/keys", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
	allow := resp.Header.Get("Allow")
	if !strings.Contains(allow, http.MethodGet) {
		t.Errorf("Allow = %q, want it to list GET", allow)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	for _, want := range []string{"<!DOCTYPE html>", "405", "That method is not allowed here."} {
		if !strings.Contains(string(body), want) {
			t.Errorf("405 page missing %q", want)
		}
	}
}

func TestAssetServedImmutable(t *testing.T) {
	srv, _ := buildServer(t, nil)
	hashed := manifestEntry(t, "app.css")

	resp, body := get(t, srv, "/assets/"+hashed)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("content-type = %q, want text/css", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("cache-control = %q, want immutable", cc)
	}
	if resp.Header.Get("ETag") == "" {
		t.Error("asset response is missing the ETag validator")
	}
	if len(body) == 0 {
		t.Error("asset body should not be empty")
	}
}

// TestAssetRevalidatesWith304 checks the conditional-GET path the streaming
// handler gets from http.ServeContent: a second request that echoes the ETag in
// If-None-Match gets an empty 304, so a revalidation past the immutable cache
// ships no bytes.
func TestAssetRevalidatesWith304(t *testing.T) {
	srv, _ := buildServer(t, nil)
	hashed := manifestEntry(t, "app.css")

	resp, _ := get(t, srv, "/assets/"+hashed)
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("asset response is missing the ETag validator")
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/assets/"+hashed, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("If-None-Match", etag)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotModified {
		t.Fatalf("status = %d, want 304 for a matching If-None-Match", resp2.StatusCode)
	}
	if len(body2) != 0 {
		t.Errorf("304 must not ship a body, got %d bytes", len(body2))
	}
}

func TestAssetManifestNotServed(t *testing.T) {
	srv, _ := buildServer(t, nil)
	resp, _ := get(t, srv, "/assets/manifest.json")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (manifest must not be served)", resp.StatusCode)
	}
}

func TestAssetTraversalRejected(t *testing.T) {
	srv, _ := buildServer(t, nil)
	resp, _ := get(t, srv, "/assets/..%2f..%2fmount.go")
	if resp.StatusCode == http.StatusOK {
		t.Fatal("path traversal must not serve a file outside the asset tree")
	}
}

// mintSession issues a session cookie for pk by running Issue against a throwaway
// context and lifting the cookie off the response, so the test never needs the
// package's unexported signing.
func mintSession(t *testing.T, s *webmw.Sessions, pk int64) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	c := mizu.NewCtx(rec, req, nil)
	s.Issue(c, pk, time.Now())
	res := rec.Result()
	for _, ck := range res.Cookies() {
		if ck.Name == webmw.DefaultSessionCookie {
			return ck
		}
	}
	t.Fatal("Issue did not set a session cookie")
	return nil
}

func hasSetCookie(resp *http.Response, name string) bool {
	for _, ck := range resp.Cookies() {
		if ck.Name == name {
			return true
		}
	}
	return false
}

// manifestEntry reads the hashed file name for a logical asset out of the
// embedded manifest, so the asset test asks for the same file the page links to.
func manifestEntry(t *testing.T, logical string) string {
	t.Helper()
	b, err := fs.ReadFile(assets.FS(), "manifest.json")
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	v, ok := m[logical]
	if !ok {
		t.Fatalf("manifest has no entry for %q", logical)
	}
	return v
}

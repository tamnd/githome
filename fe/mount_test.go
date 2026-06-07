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
	fe.Mount(root, fe.Deps{
		Render:   rs,
		View:     view.NewBuilder("Githome"),
		Sessions: sessions,
		CSRF:     webmw.NewCSRF(rs),
		Flash:    webmw.NewFlash(testKey),
		Logger:   nil,
	})
	srv := httptest.NewServer(root)
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
	if len(body) == 0 {
		t.Error("asset body should not be empty")
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

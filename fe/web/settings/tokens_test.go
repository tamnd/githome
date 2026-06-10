package settings

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
	"github.com/tamnd/githome/fe/webmw"
)

// fakeTokens is a TokenService that records calls and serves canned tokens, so
// the handler tests stay off the auth service and the database.
type fakeTokens struct {
	tokens  []PAT
	listErr error

	createdNote   string
	createdScopes []string
	deletedID     int64
	deleteErr     error
}

func (f *fakeTokens) CreatePAT(_ context.Context, _ int64, note string, scopes []string) (string, error) {
	f.createdNote, f.createdScopes = note, scopes
	return "ghp_FAKEPLAINTEXTFAKEPLAINTEXTFAKE0abcdef", nil
}

func (f *fakeTokens) ListPATs(_ context.Context, _ int64) ([]PAT, error) {
	return f.tokens, f.listErr
}

func (f *fakeTokens) DeletePAT(_ context.Context, _, id int64) error {
	f.deletedID = id
	return f.deleteErr
}

// tokensFixture mounts the tokens routes with a fake token service, signed in
// or anonymous, the same shape as the main settings fixture.
type tokensFixture struct {
	srv    *httptest.Server
	flash  *fakeFlash
	tokens *fakeTokens
}

func newTokensFixture(t *testing.T, viewer *view.Viewer, tokens *fakeTokens) tokensFixture {
	t.Helper()

	renderSet, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	flash := &fakeFlash{}
	deps := Deps{
		Render: renderSet,
		View:   view.NewBuilder("Githome"),
		Flash:  flash,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if tokens != nil {
		deps.Tokens = tokens
	}
	h := New(deps)

	root := mizu.NewRouter()
	inject := func(next mizu.Handler) mizu.Handler {
		return func(c *mizu.Ctx) error {
			r := c.Request()
			*r = *r.WithContext(view.WithViewer(r.Context(), viewer))
			return next(c)
		}
	}
	page := root.With(webmw.ColorMode()).With(inject)
	page.Get("/settings/tokens", h.Tokens)
	page.Post("/settings/tokens", h.CreateToken)
	page.Post("/settings/tokens/{id}/delete", h.DeleteToken)

	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)
	return tokensFixture{srv: srv, flash: flash, tokens: tokens}
}

func TestTokensPageAnonymousIs404(t *testing.T) {
	fx := newTokensFixture(t, nil, &fakeTokens{})
	resp, _ := get(t, fx.srv, "/settings/tokens")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("anonymous tokens page: status %d, want 404", resp.StatusCode)
	}
}

func TestTokensPageStubWithoutService(t *testing.T) {
	fx := newTokensFixture(t, signedIn(), nil)
	resp, body := get(t, fx.srv, "/settings/tokens")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "not available yet") {
		t.Errorf("unbacked tokens page lost its honest-absence stub:\n%s", body)
	}
	if strings.Contains(body, "Generate new token") {
		t.Errorf("unbacked tokens page must not offer the mint form:\n%s", body)
	}
}

func TestTokensPageListsTokens(t *testing.T) {
	lastUsed := time.Now().Add(-2 * time.Hour)
	fx := newTokensFixture(t, signedIn(), &fakeTokens{tokens: []PAT{
		{ID: 7, Note: "ci runner", Scopes: "repo", LastEight: "a1b2c3d4", CreatedAt: time.Now().Add(-48 * time.Hour), LastUsedAt: &lastUsed},
		{ID: 3, Note: "laptop", Scopes: "gist, repo", LastEight: "ffffffff", CreatedAt: time.Now().Add(-90 * 24 * time.Hour)},
	}})
	resp, body := get(t, fx.srv, "/settings/tokens")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{
		"ci runner", "laptop",
		"a1b2c3d4", "ffffffff",
		"gist, repo",
		`action="/settings/tokens/7/delete"`,
		`action="/settings/tokens/3/delete"`,
		"never used",
		"Generate new token",
		`name="note"`,
		`name="scopes"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("tokens page is missing %q", want)
		}
	}
}

func TestCreateTokenShowsPlaintextOnce(t *testing.T) {
	ft := &fakeTokens{}
	fx := newTokensFixture(t, signedIn(), ft)
	resp, err := noRedirectClient().PostForm(fx.srv.URL+"/settings/tokens", url.Values{
		"note":   {"deploy bot"},
		"scopes": {"repo", "workflow"},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	body := string(b)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// The mint renders the plaintext in this very response rather than
	// redirecting, so the secret is shown once and never staged in a cookie.
	if !strings.Contains(body, "ghp_FAKEPLAINTEXTFAKEPLAINTEXTFAKE0abcdef") {
		t.Errorf("create response did not show the new token:\n%s", body)
	}
	if !strings.Contains(body, "copy your new token now") {
		t.Errorf("create response is missing the show-once warning")
	}
	if ft.createdNote != "deploy bot" {
		t.Errorf("created note = %q, want deploy bot", ft.createdNote)
	}
	if len(ft.createdScopes) != 2 || ft.createdScopes[0] != "repo" || ft.createdScopes[1] != "workflow" {
		t.Errorf("created scopes = %v, want [repo workflow]", ft.createdScopes)
	}
}

func TestCreateTokenRequiresNote(t *testing.T) {
	ft := &fakeTokens{}
	fx := newTokensFixture(t, signedIn(), ft)
	resp, err := noRedirectClient().PostForm(fx.srv.URL+"/settings/tokens", url.Values{
		"scopes": {"repo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(b), "Give the token a note") {
		t.Errorf("missing-note create did not render the inline error:\n%s", b)
	}
	if ft.createdNote != "" {
		t.Errorf("missing-note create still minted a token")
	}
}

func TestDeleteTokenRedirectsWithFlash(t *testing.T) {
	ft := &fakeTokens{}
	fx := newTokensFixture(t, signedIn(), ft)
	resp := postForm(t, fx.srv, "/settings/tokens/42/delete", url.Values{})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/settings/tokens" {
		t.Errorf("redirect location = %q, want /settings/tokens", got)
	}
	if ft.deletedID != 42 {
		t.Errorf("deleted id = %d, want 42", ft.deletedID)
	}
	if fx.flash.last().kind != "success" {
		t.Errorf("flash kind = %q, want success", fx.flash.last().kind)
	}
}

func TestDeleteMissingTokenFlashesError(t *testing.T) {
	ft := &fakeTokens{deleteErr: errors.New("not found")}
	fx := newTokensFixture(t, signedIn(), ft)
	resp := postForm(t, fx.srv, "/settings/tokens/42/delete", url.Values{})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", resp.StatusCode)
	}
	if fx.flash.last().kind != "error" {
		t.Errorf("flash kind = %q, want error", fx.flash.last().kind)
	}
}

package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-mizu/mizu"
	"golang.org/x/crypto/bcrypt"

	"github.com/tamnd/githome/api/rest"
	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/assets"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
	webauth "github.com/tamnd/githome/fe/web/auth"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/store"
)

// deviceFlowServer wires the full device-flow surface the way cmd/githome
// does: the REST OAuth endpoints and the FE approval page share one router,
// one store, and one auth service, with the gh app seeded at boot.
func deviceFlowServer(t *testing.T) (*httptest.Server, *auth.Service) {
	t.Helper()
	ctx := context.Background()

	st, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "githome.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("correct horse"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertUserWithPassword(ctx, "octocat", "octo@example.com", string(hash)); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	authSvc := auth.NewService(st, "https://git.test.internal")
	t.Cleanup(authSvc.Close)
	if err := authSvc.EnsureFirstPartyApps(ctx); err != nil {
		t.Fatalf("seed apps: %v", err)
	}

	root := mizu.NewRouter()
	rest.Mount(root, rest.Deps{Ready: st, Auth: authSvc, WebFront: true})

	renderSet, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	users := domain.NewUserService(st)
	lookup := func(ctx context.Context, pk int64) (*view.Viewer, error) {
		u, err := users.Viewer(ctx, pk)
		if err != nil {
			if errors.Is(err, domain.ErrUserNotFound) {
				return nil, nil
			}
			return nil, err
		}
		return &view.Viewer{Login: u.Login}, nil
	}
	sessions := webmw.NewSessions([]byte("0123456789abcdef0123456789abcdef"), 0, lookup)
	vb := view.NewBuilder("Githome")
	ah := webauth.New(webauth.Deps{
		Store:    st,
		Sessions: sessions,
		View:     vb,
		Render:   renderSet,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	oh := webauth.NewOAuthHandlers(authSvc, renderSet, vb)

	page := root.With(sessions.Middleware())
	page.Post("/login/session", ah.LoginSubmit)
	page.Get("/login/device", oh.DeviceForm)
	page.Post("/login/device", oh.DeviceSubmit)

	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)
	return srv, authSvc
}

// postForm posts form values with Accept: application/json and decodes the
// JSON response into out.
func postOAuthForm(t *testing.T, srv *httptest.Server, path string, form url.Values, out any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

// TestDeviceFlowEndToEnd walks the whole gh login path against one server: the
// device-code request, the browser-side approval on /login/device, and the
// token poll that mints the gho_ credential.
func TestDeviceFlowEndToEnd(t *testing.T) {
	srv, authSvc := deviceFlowServer(t)

	// 1. The device asks for its codes, exactly as gh does at login.
	var codes struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
	}
	postOAuthForm(t, srv, "/login/device/code", url.Values{
		"client_id": {auth.GHCLIClientID},
		"scope":     {"repo gist"},
	}, &codes)
	if codes.DeviceCode == "" || codes.UserCode == "" {
		t.Fatalf("device code response incomplete: %+v", codes)
	}
	if !strings.HasSuffix(codes.VerificationURI, "/login/device") {
		t.Fatalf("verification_uri = %q, want .../login/device", codes.VerificationURI)
	}

	// 2. The user signs in on the web and the login response carries the
	// session cookie. The cookie is Secure, so the test grabs it from the 303
	// by hand rather than through a jar (httptest serves plain http).
	session := signIn(t, srv)

	// An anonymous visit to the approval page bounces to login.
	anon, err := noRedirect().Get(srv.URL + "/login/device")
	if err != nil {
		t.Fatal(err)
	}
	_ = anon.Body.Close()
	if anon.StatusCode != http.StatusSeeOther || !strings.Contains(anon.Header.Get("Location"), "/login?return_to=") {
		t.Fatalf("anonymous /login/device: status %d location %q", anon.StatusCode, anon.Header.Get("Location"))
	}

	// 3. The signed-in user types the code and approves the device.
	form := url.Values{"user_code": {codes.UserCode}, "action": {"approve"}}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/login/device", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	approve, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	body, _ := io.ReadAll(approve.Body)
	_ = approve.Body.Close()
	if approve.StatusCode != http.StatusOK || !strings.Contains(string(body), "Device connected") {
		t.Fatalf("approve: status %d body:\n%s", approve.StatusCode, body)
	}

	// 4. The device's next poll exchanges the device code for a user token.
	var tok struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Scope       string `json:"scope"`
		Error       string `json:"error"`
	}
	postOAuthForm(t, srv, "/login/oauth/access_token", url.Values{
		"client_id":   {auth.GHCLIClientID},
		"device_code": {codes.DeviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}, &tok)
	if tok.Error != "" {
		t.Fatalf("token poll: %s", tok.Error)
	}
	if !strings.HasPrefix(tok.AccessToken, auth.PrefixOAuth) {
		t.Fatalf("access_token = %q, want a %s token", tok.AccessToken, auth.PrefixOAuth)
	}
	if tok.TokenType != "bearer" {
		t.Errorf("token_type = %q, want bearer", tok.TokenType)
	}

	// 5. The minted token authenticates as the approving user.
	actor, err := authSvc.Authenticate(context.Background(), "token "+tok.AccessToken)
	if err != nil {
		t.Fatalf("authenticate minted token: %v", err)
	}
	if actor.UserLogin != "octocat" {
		t.Errorf("token resolves to %q, want octocat", actor.UserLogin)
	}
	if !actor.Scopes.Has("gist") || !actor.Scopes.Has("repo") {
		t.Errorf("token scopes = %q, want repo and gist", actor.Scopes.Header())
	}
}

// TestDeviceDenyEndToEnd covers the refusal path: a denied code makes the
// device's poll answer access_denied.
func TestDeviceDenyEndToEnd(t *testing.T) {
	srv, _ := deviceFlowServer(t)

	var codes struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
	}
	postOAuthForm(t, srv, "/login/device/code", url.Values{
		"client_id": {auth.GHCLIClientID},
	}, &codes)

	session := signIn(t, srv)

	form := url.Values{"user_code": {codes.UserCode}, "action": {"deny"}}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/login/device", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	deny, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(deny.Body)
	_ = deny.Body.Close()
	if deny.StatusCode != http.StatusOK || !strings.Contains(string(body), "Request denied") {
		t.Fatalf("deny: status %d body:\n%s", deny.StatusCode, body)
	}

	var tok struct {
		Error string `json:"error"`
	}
	postOAuthForm(t, srv, "/login/oauth/access_token", url.Values{
		"client_id":   {auth.GHCLIClientID},
		"device_code": {codes.DeviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}, &tok)
	if tok.Error != "access_denied" {
		t.Fatalf("poll after deny = %q, want access_denied", tok.Error)
	}
}

// TestDeviceSubmitBadCode covers the wrong-code path: the form re-renders
// with an error and confirms nothing.
func TestDeviceSubmitBadCode(t *testing.T) {
	srv, _ := deviceFlowServer(t)

	session := signIn(t, srv)

	form := url.Values{"user_code": {"WDJB-MJHT"}, "action": {"approve"}}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/login/device", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	bad, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(bad.Body)
	_ = bad.Body.Close()
	if !strings.Contains(string(body), "invalid or has expired") {
		t.Fatalf("bad code did not error:\n%s", body)
	}
}

func noRedirect() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// signIn posts the login form and returns the session cookie from the 303.
// The redirect must not be followed: the cookie rides on the redirect response
// itself, and the target is not mounted in this test server anyway.
func signIn(t *testing.T, srv *httptest.Server) *http.Cookie {
	t.Helper()
	resp, err := noRedirect().PostForm(srv.URL+"/login/session", url.Values{
		"login":    {"octocat"},
		"password": {"correct horse"},
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	for _, ck := range resp.Cookies() {
		if ck.Name == webmw.DefaultSessionCookie {
			return ck
		}
	}
	t.Fatalf("login issued no session cookie (status %d) body:\n%s", resp.StatusCode, body)
	return nil
}

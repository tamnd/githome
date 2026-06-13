package rest

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-mizu/mizu"
	"github.com/golang-jwt/jwt/v5"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// appFixture is a REST server seeded with one GitHub App installed on the
// octocat account, which owns a public and a private repository. It can sign an
// app JWT so the app-auth endpoints can be exercised end to end.
type appFixture struct {
	srv     *httptest.Server
	st      *store.Store
	priv    *rsa.PrivateKey
	app     *store.GitHubAppRow
	inst    *store.InstallationRow
	repoPK  int64
	privPK  int64
	account *store.UserRow
}

// appServerSelected builds the fixture with the given installation repository
// selection ("all" or "selected"); a "selected" install is granted the public
// repo only.
func appServerSelected(t *testing.T, selection string) appFixture {
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

	account := &store.UserRow{Login: "octocat", Type: "Organization"}
	if err := st.InsertUser(ctx, account); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	pub := &store.RepoRow{OwnerPK: account.PK, Name: "hello", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, pub); err != nil {
		t.Fatalf("insert public repo: %v", err)
	}
	priv := &store.RepoRow{OwnerPK: account.PK, Name: "secret", Private: true, DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, priv); err != nil {
		t.Fatalf("insert private repo: %v", err)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	app := &store.GitHubAppRow{
		OwnerPK:       account.PK,
		Slug:          "test-app",
		Name:          "Test App",
		ClientID:      "Iv1.testclientid",
		PrivateKeyPEM: pemBytes,
		Permissions:   `{"contents":"read","issues":"write"}`,
		Events:        `["push","pull_request"]`,
	}
	if err := st.InsertGitHubApp(ctx, app); err != nil {
		t.Fatalf("insert app: %v", err)
	}
	inst := &store.InstallationRow{
		AppPK:               app.PK,
		AccountPK:           account.PK,
		RepositorySelection: selection,
		Permissions:         `{"contents":"read"}`,
		Events:              `["push"]`,
	}
	if err := st.InsertInstallation(ctx, inst); err != nil {
		t.Fatalf("insert installation: %v", err)
	}
	if selection == "selected" {
		if err := st.InsertInstallationRepo(ctx, inst.PK, pub.PK); err != nil {
			t.Fatalf("grant repo: %v", err)
		}
	}

	authSvc := auth.NewService(st, "https://git.test.internal")
	t.Cleanup(authSvc.Close)
	cfg := authConfig(t)
	gitStore := git.NewStore(t.TempDir())
	repoSvc := domain.NewRepoService(st, gitStore)
	root := mizu.NewRouter()
	Mount(root, Deps{
		Config:     cfg,
		Ready:      st,
		Auth:       authSvc,
		Users:      domain.NewUserService(st),
		Repos:      repoSvc,
		URLs:       presenter.NewURLBuilder(cfg.URLs),
		NodeFormat: nodeid.FormatNew,
	})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	return appFixture{
		srv: srv, st: st, priv: key, app: app, inst: inst,
		repoPK: pub.PK, privPK: priv.PK, account: account,
	}
}

func appServer(t *testing.T) appFixture { return appServerSelected(t, "all") }

// appSend issues a POST carrying a Bearer app JWT (not the "token " scheme
// authedSend uses), the credential the app-auth endpoints require.
func appSend(t *testing.T, srv *httptest.Server, path, jwt, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

// appJWT signs a short-lived RS256 app JWT whose issuer is the app's internal
// pk, the credential GET /app and the installation endpoints authenticate.
func (fx appFixture) appJWT(t *testing.T) string {
	t.Helper()
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Issuer:    strconv.FormatInt(fx.app.PK, 10),
		IssuedAt:  jwt.NewNumericDate(now.Add(-30 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
	})
	signed, err := tok.SignedString(fx.priv)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return signed
}

// TestAppObjectShape checks GET /app carries the node_id, owner, permissions,
// and events the auth-app ecosystem reads.
func TestAppObjectShape(t *testing.T) {
	fx := appServer(t)

	resp, body := authedGet(t, fx.srv, "/app", "Bearer "+fx.appJWT(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	var app struct {
		NodeID      string            `json:"node_id"`
		Slug        string            `json:"slug"`
		Owner       *struct{ Login string } `json:"owner"`
		Permissions map[string]string `json:"permissions"`
		Events      []string          `json:"events"`
	}
	decodeBody(t, body, &app)
	if !strings.HasPrefix(app.NodeID, "A_") {
		t.Errorf("node_id = %q, want A_ prefix", app.NodeID)
	}
	if app.Owner == nil || app.Owner.Login != "octocat" {
		t.Errorf("owner = %+v, want octocat", app.Owner)
	}
	if app.Permissions["contents"] != "read" || app.Permissions["issues"] != "write" {
		t.Errorf("permissions = %v", app.Permissions)
	}
	if len(app.Events) != 2 || app.Events[0] != "push" {
		t.Errorf("events = %v, want [push pull_request]", app.Events)
	}
}

// TestInstallationObjectShape checks GET /app/installations carries the account
// object and a null suspended_at the auth-app flows read.
func TestInstallationObjectShape(t *testing.T) {
	fx := appServer(t)

	resp, body := authedGet(t, fx.srv, "/app/installations", "Bearer "+fx.appJWT(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	var insts []struct {
		Account     *struct{ Login string } `json:"account"`
		TargetType  string                  `json:"target_type"`
		SuspendedAt *string                 `json:"suspended_at"`
		RepoSel     string                  `json:"repository_selection"`
	}
	decodeBody(t, body, &insts)
	if len(insts) != 1 {
		t.Fatalf("installations = %d, want 1: %s", len(insts), body)
	}
	in := insts[0]
	if in.Account == nil || in.Account.Login != "octocat" {
		t.Errorf("account = %+v, want octocat", in.Account)
	}
	if in.TargetType != "Organization" {
		t.Errorf("target_type = %q, want Organization", in.TargetType)
	}
	if in.SuspendedAt != nil {
		t.Errorf("suspended_at = %v, want null", *in.SuspendedAt)
	}
}

// TestRepoInstallationLookup checks GET /repos/{o}/{r}/installation resolves the
// app's installation on the repository's account.
func TestRepoInstallationLookup(t *testing.T) {
	fx := appServer(t)

	resp, body := authedGet(t, fx.srv, "/repos/octocat/hello/installation", "Bearer "+fx.appJWT(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	var inst struct {
		ID      int64 `json:"id"`
		Account *struct{ Login string } `json:"account"`
	}
	decodeBody(t, body, &inst)
	if inst.ID != fx.inst.DBID {
		t.Errorf("id = %d, want %d", inst.ID, fx.inst.DBID)
	}
	if inst.Account == nil || inst.Account.Login != "octocat" {
		t.Errorf("account = %+v", inst.Account)
	}

	// An account with no installation of this app is 404.
	if resp, _ := authedGet(t, fx.srv, "/repos/ghost/none/installation", "Bearer "+fx.appJWT(t)); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown account status %d, want 404", resp.StatusCode)
	}
}

// TestInstallationTokenScoping checks the access-token mint reflects the
// repository scope: an unscoped request reports "all", and a request naming
// repositories reports "selected" and echoes the resolved repositories.
func TestInstallationTokenScoping(t *testing.T) {
	fx := appServer(t)
	path := "/app/installations/" + strconv.FormatInt(fx.inst.DBID, 10) + "/access_tokens"

	// Unscoped mint: repository_selection "all", no repositories array.
	tokResp, tokBody := appSend(t, fx.srv, path, fx.appJWT(t), `{}`)
	if tokResp.StatusCode != http.StatusCreated {
		t.Fatalf("unscoped mint status %d, body %s", tokResp.StatusCode, tokBody)
	}
	var unscoped struct {
		Token       string `json:"token"`
		RepoSel     string `json:"repository_selection"`
		Repos       []any  `json:"repositories"`
	}
	decodeBody(t, tokBody, &unscoped)
	if unscoped.RepoSel != "all" {
		t.Errorf("unscoped selection = %q, want all", unscoped.RepoSel)
	}
	if !strings.HasPrefix(unscoped.Token, "ghs_") {
		t.Errorf("token = %q, want ghs_ prefix", unscoped.Token)
	}
	if len(unscoped.Repos) != 0 {
		t.Errorf("unscoped repositories = %v, want absent", unscoped.Repos)
	}

	// Scoped mint: repository_selection "selected", repositories echoed.
	scResp, scBody := appSend(t, fx.srv, path, fx.appJWT(t), `{"repositories":["hello"]}`)
	if scResp.StatusCode != http.StatusCreated {
		t.Fatalf("scoped mint status %d, body %s", scResp.StatusCode, scBody)
	}
	var scoped struct {
		RepoSel string `json:"repository_selection"`
		Repos   []struct{ Name string } `json:"repositories"`
	}
	decodeBody(t, scBody, &scoped)
	if scoped.RepoSel != "selected" {
		t.Errorf("scoped selection = %q, want selected", scoped.RepoSel)
	}
	if len(scoped.Repos) != 1 || scoped.Repos[0].Name != "hello" {
		t.Errorf("scoped repositories = %+v, want [hello]", scoped.Repos)
	}
}

// TestInstallationReposAutodiscovery checks GET /installation/repositories,
// authenticated with a minted installation token, lists the installation's
// repositories — every account repo under "all" selection.
func TestInstallationReposAutodiscovery(t *testing.T) {
	fx := appServer(t)
	path := "/app/installations/" + strconv.FormatInt(fx.inst.DBID, 10) + "/access_tokens"
	_, tokBody := appSend(t, fx.srv, path, fx.appJWT(t), `{}`)
	var minted struct {
		Token string `json:"token"`
	}
	decodeBody(t, tokBody, &minted)
	if minted.Token == "" {
		t.Fatalf("no token minted: %s", tokBody)
	}

	resp, body := authedGet(t, fx.srv, "/installation/repositories", "token "+minted.Token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	var env struct {
		TotalCount int    `json:"total_count"`
		RepoSel    string `json:"repository_selection"`
		Repos      []struct {
			Name    string `json:"name"`
			Private bool   `json:"private"`
		} `json:"repositories"`
	}
	decodeBody(t, body, &env)
	if env.RepoSel != "all" {
		t.Errorf("repository_selection = %q, want all", env.RepoSel)
	}
	// "all" selection lists every repo the account owns, public and private.
	if env.TotalCount != 2 || len(env.Repos) != 2 {
		t.Fatalf("total_count %d / %d repos, want 2: %s", env.TotalCount, len(env.Repos), body)
	}
}

// TestInstallationReposSelected checks a "selected"-scope installation lists
// only the granted repository.
func TestInstallationReposSelected(t *testing.T) {
	fx := appServerSelected(t, "selected")
	path := "/app/installations/" + strconv.FormatInt(fx.inst.DBID, 10) + "/access_tokens"
	_, tokBody := appSend(t, fx.srv, path, fx.appJWT(t), `{}`)
	var minted struct {
		Token string `json:"token"`
	}
	decodeBody(t, tokBody, &minted)

	resp, body := authedGet(t, fx.srv, "/installation/repositories", "token "+minted.Token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	var env struct {
		TotalCount int `json:"total_count"`
		Repos      []struct{ Name string } `json:"repositories"`
	}
	decodeBody(t, body, &env)
	if env.TotalCount != 1 || len(env.Repos) != 1 || env.Repos[0].Name != "hello" {
		t.Fatalf("selected listing = %s, want just hello", body)
	}
}

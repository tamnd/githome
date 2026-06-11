package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
	"github.com/tamnd/githome/worker"
)

// hookFixture is a REST server backed by a store seeded with owner octocat, repo
// hello, and a second user mallory who has no rights to the repo. It carries both
// users' tokens so a test can drive the admin path and the forbidden path. The
// org tokens hold admin:org_hook, which the /orgs hook surface requires and the
// plain repo scope does not imply.
type hookFixture struct {
	srv       *httptest.Server
	token     string // octocat, the repo owner
	intrud    string // mallory, an unrelated user
	orgToken  string // octocat with admin:org_hook
	orgIntrud string // mallory with admin:org_hook
}

func hookServer(t *testing.T) hookFixture {
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

	owner := &store.UserRow{Login: "octocat", Type: "User"}
	if err := st.InsertUser(ctx, owner); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	intruder := &store.UserRow{Login: "mallory", Type: "User"}
	if err := st.InsertUser(ctx, intruder); err != nil {
		t.Fatalf("insert intruder: %v", err)
	}
	repo := &store.RepoRow{OwnerPK: owner.PK, Name: "hello", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("insert repo: %v", err)
	}

	authSvc := auth.NewService(st, "https://git.test.internal")
	t.Cleanup(authSvc.Close)
	cfg := authConfig(t)
	gitStore := git.NewStore(t.TempDir())
	repoSvc := domain.NewRepoService(st, gitStore)
	enq := worker.NewStoreEnqueuer(st)
	root := mizu.NewRouter()
	Mount(root, Deps{
		Config:     cfg,
		Ready:      st,
		Auth:       authSvc,
		Users:      domain.NewUserService(st),
		Repos:      repoSvc,
		Hooks:      domain.NewHookService(st, repoSvc, enq),
		URLs:       presenter.NewURLBuilder(cfg.URLs),
		NodeFormat: nodeid.FormatNew,
	})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	return hookFixture{
		srv:       srv,
		token:     seedToken(t, st, owner.PK),
		intrud:    seedToken(t, st, intruder.PK),
		orgToken:  seedScopedToken(t, st, owner.PK, "admin:org_hook"),
		orgIntrud: seedScopedToken(t, st, intruder.PK, "admin:org_hook"),
	}
}

// seedHook creates a hook subscribed to push and returns its external id, which
// is the store's global db_id rather than a per-repo counter.
func (fx hookFixture) seedHook(t *testing.T) int64 {
	t.Helper()
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/hooks", fx.token,
		`{"events":["push"],"config":{"url":"https://example.test/hook"}}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed hook status %d, body %s", resp.StatusCode, body)
	}
	var got struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode seed hook: %v", err)
	}
	return got.ID
}

func TestCreateHookContract(t *testing.T) {
	fx := hookServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/hooks", fx.token,
		`{"name":"web","active":true,"events":["push","issues"],"config":{"url":"https://example.test/hook","content_type":"json","secret":"swordfish","insecure_ssl":"0"}}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "hook_create.golden.json", body)

	// The secret is never echoed back; the config carries the fixed mask instead.
	var got struct {
		Config struct {
			Secret string `json:"secret"`
		} `json:"config"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Config.Secret != "********" {
		t.Errorf("config.secret = %q, want the fixed mask", got.Config.Secret)
	}
}

func TestGetHookContract(t *testing.T) {
	fx := hookServer(t)
	id := fx.seedHook(t)
	resp, body := authedSend(t, fx.srv, http.MethodGet, fmt.Sprintf("/repos/octocat/hello/hooks/%d", id), fx.token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "hook_get.golden.json", body)
}

func TestListHooksContract(t *testing.T) {
	fx := hookServer(t)
	fx.seedHook(t)
	resp, body := authedSend(t, fx.srv, http.MethodGet, "/repos/octocat/hello/hooks", fx.token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "hooks_list.golden.json", body)
}

func TestUpdateHookContract(t *testing.T) {
	fx := hookServer(t)
	id := fx.seedHook(t)
	resp, body := authedSend(t, fx.srv, http.MethodPatch, fmt.Sprintf("/repos/octocat/hello/hooks/%d", id), fx.token,
		`{"active":false,"add_events":["issues","pull_request"],"config":{"content_type":"form"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "hook_update.golden.json", body)
}

func TestDeleteHookContract(t *testing.T) {
	fx := hookServer(t)
	id := fx.seedHook(t)
	path := fmt.Sprintf("/repos/octocat/hello/hooks/%d", id)
	resp, _ := authedSend(t, fx.srv, http.MethodDelete, path, fx.token, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status %d, want 204", resp.StatusCode)
	}
	// The hook is gone: a follow-up GET is 404.
	if resp, _ := authedSend(t, fx.srv, http.MethodGet, path, fx.token, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("get after delete status %d, want 404", resp.StatusCode)
	}
}

func TestEmptyDeliveriesContract(t *testing.T) {
	fx := hookServer(t)
	id := fx.seedHook(t)
	resp, body := authedSend(t, fx.srv, http.MethodGet, fmt.Sprintf("/repos/octocat/hello/hooks/%d/deliveries", id), fx.token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	if string(body) != "[]" {
		t.Errorf("deliveries body = %s, want []", body)
	}
}

func TestHookWriteErrors(t *testing.T) {
	fx := hookServer(t)

	// A config without a url is rejected before any write.
	if resp, _ := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/hooks", fx.token,
		`{"events":["push"],"config":{}}`); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("missing-url status %d, want 422", resp.StatusCode)
	}

	// A user without admin rights cannot manage the repo's hooks: 403.
	if resp, _ := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/hooks", fx.intrud,
		`{"events":["push"],"config":{"url":"https://example.test/hook"}}`); resp.StatusCode != http.StatusForbidden {
		t.Errorf("intruder create status %d, want 403", resp.StatusCode)
	}

	// A hook under a repository that does not exist is 404, never 403.
	if resp, _ := authedSend(t, fx.srv, http.MethodGet, "/repos/octocat/nope/hooks/1", fx.token, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing repo status %d, want 404", resp.StatusCode)
	}

	// A hook id that does not exist is 404.
	if resp, _ := authedSend(t, fx.srv, http.MethodGet, "/repos/octocat/hello/hooks/999", fx.token, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing hook status %d, want 404", resp.StatusCode)
	}

	// A redelivery of a delivery that does not exist is 404.
	id := fx.seedHook(t)
	if resp, _ := authedSend(t, fx.srv, http.MethodPost, fmt.Sprintf("/repos/octocat/hello/hooks/%d/deliveries/999/attempts", id), fx.token, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing delivery redeliver status %d, want 404", resp.StatusCode)
	}
}

func TestOrgHooksContract(t *testing.T) {
	fx := hookServer(t)

	// Before the first hook the org has no anchor repository. That reads as an
	// empty list, not a 404, so a list-create-list flow works.
	resp, body := authedSend(t, fx.srv, http.MethodGet, "/orgs/octocat/hooks", fx.orgToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initial list status %d, want 200, body %s", resp.StatusCode, body)
	}
	if string(body) != "[]" {
		t.Errorf("initial list body = %s, want []", body)
	}

	// The plain repo scope does not reach the org surface.
	if resp, _ := authedSend(t, fx.srv, http.MethodGet, "/orgs/octocat/hooks", fx.token, ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("repo-scope list status %d, want 403", resp.StatusCode)
	}

	// A correctly scoped token of an unrelated user is still forbidden.
	if resp, _ := authedSend(t, fx.srv, http.MethodPost, "/orgs/octocat/hooks", fx.orgIntrud,
		`{"events":["issues"],"config":{"url":"https://example.test/orghook"}}`); resp.StatusCode != http.StatusForbidden {
		t.Errorf("intruder create status %d, want 403", resp.StatusCode)
	}

	// Create renders into the /orgs URL space, type Organization, no test_url.
	resp, body = authedSend(t, fx.srv, http.MethodPost, "/orgs/octocat/hooks", fx.orgToken,
		`{"name":"web","active":true,"events":["issues","push"],"config":{"url":"https://example.test/orghook","content_type":"json","secret":"swordfish"}}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d, want 201, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "org_hook_create.golden.json", body)
	var created struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Type != "Organization" {
		t.Errorf("type = %q, want Organization", created.Type)
	}
	if want := fmt.Sprintf("/orgs/octocat/hooks/%d", created.ID); !strings.HasSuffix(created.URL, want) {
		t.Errorf("url = %q, want suffix %q", created.URL, want)
	}
	if bytes.Contains(body, []byte("test_url")) {
		t.Errorf("org hook body carries test_url: %s", body)
	}

	// The hook reads back through the list and the single GET.
	resp, body = authedSend(t, fx.srv, http.MethodGet, "/orgs/octocat/hooks", fx.orgToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status %d, want 200, body %s", resp.StatusCode, body)
	}
	var listed []struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Errorf("list = %s, want the one created hook", body)
	}
	path := fmt.Sprintf("/orgs/octocat/hooks/%d", created.ID)
	if resp, body := authedSend(t, fx.srv, http.MethodGet, path, fx.orgToken, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("get status %d, want 200, body %s", resp.StatusCode, body)
	}

	// The org-level ping accepts and queues like the repo-level one.
	if resp, body := authedSend(t, fx.srv, http.MethodPost, path+"/pings", fx.orgToken, ""); resp.StatusCode != http.StatusNoContent {
		t.Errorf("ping status %d, want 204, body %s", resp.StatusCode, body)
	}

	// Delete tears it down and the org lists empty again.
	if resp, _ := authedSend(t, fx.srv, http.MethodDelete, path, fx.orgToken, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status %d, want 204", resp.StatusCode)
	}
	if resp, _ := authedSend(t, fx.srv, http.MethodGet, path, fx.orgToken, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("get after delete status %d, want 404", resp.StatusCode)
	}
	resp, body = authedSend(t, fx.srv, http.MethodGet, "/orgs/octocat/hooks", fx.orgToken, "")
	if resp.StatusCode != http.StatusOK || string(body) != "[]" {
		t.Errorf("final list status %d body %s, want 200 []", resp.StatusCode, body)
	}

	// An org that does not exist is a 404 on create.
	if resp, _ := authedSend(t, fx.srv, http.MethodPost, "/orgs/nope/hooks", fx.orgToken,
		`{"events":["issues"],"config":{"url":"https://example.test/orghook"}}`); resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing org create status %d, want 404", resp.StatusCode)
	}
}

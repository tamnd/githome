package reposettings

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/assets"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
)

// testSessionKey is a fixed 32-byte key for the test session signer.
var testSessionKey = []byte("githome-reposettings-test-key!!!")

// fakeEnqueuer records how many jobs the redeliver path enqueued, so a test
// asserts the replay reached the queue without standing up a worker.
type fakeEnqueuer struct{ calls int }

func (f *fakeEnqueuer) Enqueue(_ context.Context, _, _, _ string) (bool, error) {
	f.calls++
	return false, nil
}

// fixture is the repository settings web test harness: a live TLS httptest server
// mounting the webhooks handlers over a real sqlite store and the real domain hook
// service, with the real session middleware so the Resolve gate and the service
// see the actual administrator pk. It seeds an administrator (octocat) with a repo
// they own, and a second account (mallory) with a public repo octocat can see but
// must not administer, the case the 404-not-403 rule turns away.
type fixture struct {
	srv     *httptest.Server
	client  *http.Client
	store   *store.Store
	hooks   *domain.HookService
	enq     *fakeEnqueuer
	ownerPK int64
	repoPK  int64
}

func newFixture(t *testing.T) fixture {
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

	octocat := &store.UserRow{Login: "octocat", Type: "User"}
	if err := st.InsertUser(ctx, octocat); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	mallory := &store.UserRow{Login: "mallory", Type: "User"}
	if err := st.InsertUser(ctx, mallory); err != nil {
		t.Fatalf("insert other: %v", err)
	}
	hello := &store.RepoRow{OwnerPK: octocat.PK, Name: "hello", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, hello); err != nil {
		t.Fatalf("insert repo: %v", err)
	}
	// A public repo owned by someone else: octocat can see it but cannot administer
	// it, so its settings surface must 404 to octocat rather than confirm it.
	widgets := &store.RepoRow{OwnerPK: mallory.PK, Name: "widgets", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, widgets); err != nil {
		t.Fatalf("insert other repo: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	repoSvc := domain.NewRepoService(st, gitStore)
	enq := &fakeEnqueuer{}
	hookSvc := domain.NewHookService(st, repoSvc, enq)

	renderSet, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	h := New(Deps{
		Hooks:  hookSvc,
		Repos:  repoSvc,
		Render: renderSet,
		View:   view.NewBuilder("Githome"),
		Flash:  webmw.NewFlash(testSessionKey),
		Logger: discard,
	})

	sessions := webmw.NewSessions(testSessionKey, time.Hour, func(_ context.Context, pk int64) (*view.Viewer, error) {
		if pk == octocat.PK {
			return &view.Viewer{Login: "octocat", Name: "The Octocat"}, nil
		}
		return nil, nil
	})
	csrf := webmw.NewCSRF(renderSet)
	flash := webmw.NewFlash(testSessionKey)

	root := mizu.NewRouter()
	page := root.With(sessions.Middleware(), webmw.ColorMode(), csrf.Middleware(), flash.Middleware())

	page.Get("/_test/login", func(c *mizu.Ctx) error {
		sessions.Issue(c, octocat.PK, time.Now())
		return c.Text(http.StatusOK, "ok")
	})

	rg := page.With(h.Resolve)
	rg.Get("/{owner}/{repo}/settings", h.Root)
	rg.Post("/{owner}/{repo}/settings", h.UpdateGeneral)
	rg.Post("/{owner}/{repo}/settings/visibility", h.UpdateVisibility)
	rg.Post("/{owner}/{repo}/settings/delete", h.Delete)
	rg.Get("/{owner}/{repo}/settings/hooks", h.Hooks)
	rg.Get("/{owner}/{repo}/settings/hooks/new", h.NewHook)
	rg.Post("/{owner}/{repo}/settings/hooks", h.CreateHook)
	rg.Get("/{owner}/{repo}/settings/hooks/{hook}", h.EditHook)
	rg.Post("/{owner}/{repo}/settings/hooks/{hook}", h.UpdateHook)
	rg.Post("/{owner}/{repo}/settings/hooks/{hook}/delete", h.DeleteHook)
	rg.Get("/{owner}/{repo}/settings/hooks/{hook}/deliveries/{delivery}", h.Delivery)
	rg.Post("/{owner}/{repo}/settings/hooks/{hook}/deliveries/{delivery}/redeliver", h.Redeliver)

	srv := httptest.NewTLSServer(root)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := srv.Client()
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return fixture{srv: srv, client: client, store: st, hooks: hookSvc, enq: enq, ownerPK: octocat.PK, repoPK: hello.PK}
}

// login hits the test login route so the client jar carries octocat's session.
func (fx fixture) login(t *testing.T) {
	t.Helper()
	resp, err := fx.client.Get(fx.srv.URL + "/_test/login")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status %d", resp.StatusCode)
	}
}

// get issues a GET and returns the response and body.
func (fx fixture) get(t *testing.T, path string) (*http.Response, string) {
	t.Helper()
	resp, err := fx.client.Get(fx.srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp, string(b)
}

// csrfToken issues a GET so the CSRF cookie is set, then reads the token the form
// echoes into its hidden field, the half a no-JS post would carry.
func (fx fixture) csrfToken(t *testing.T, path string) string {
	t.Helper()
	_, body := fx.get(t, path)
	const marker = `name="_csrf" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no csrf token in %s", path)
	}
	rest := body[i+len(marker):]
	j := strings.IndexByte(rest, '"')
	return rest[:j]
}

// post submits a form with the CSRF field included and returns the response.
func (fx fixture) post(t *testing.T, path, csrf string, form url.Values) *http.Response {
	t.Helper()
	form.Set("_csrf", csrf)
	resp, err := fx.client.PostForm(fx.srv.URL+path, form)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// seedHook creates a webhook through the service as the owner and returns its
// public id, for the read and edit assertions.
func (fx fixture) seedHook(t *testing.T, payloadURL string, secret *string) int64 {
	t.Helper()
	active := true
	hook, err := fx.hooks.CreateHook(context.Background(), fx.ownerPK, "octocat", "hello", domain.HookInput{
		URL:    payloadURL,
		Secret: secret,
		Active: &active,
		Events: []string{domain.EventPush},
	})
	if err != nil {
		t.Fatalf("seed hook: %v", err)
	}
	return hook.ID
}

func TestEmptyHooksBlankslate(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	resp, body := fx.get(t, "/octocat/hello/settings/hooks")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "No webhooks yet") {
		t.Errorf("empty hooks list is missing the blankslate:\n%s", body)
	}
	if !strings.Contains(body, "Add webhook") {
		t.Errorf("empty hooks list is missing the add affordance:\n%s", body)
	}
}

func TestHooksListShowsCreatedHook(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	fx.seedHook(t, "https://hooks.example.com/a", nil)
	resp, body := fx.get(t, "/octocat/hello/settings/hooks")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "https://hooks.example.com/a") {
		t.Errorf("hooks list is missing the seeded hook target:\n%s", body)
	}
}

func TestCreateHookRedirectsToEdit(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	token := fx.csrfToken(t, "/octocat/hello/settings/hooks/new")
	resp := fx.post(t, "/octocat/hello/settings/hooks", token, url.Values{
		"payload_url":  {"https://hooks.example.com/new"},
		"content_type": {"json"},
		"subscribe":    {"choose"},
		"events":       {"push"},
		"active":       {"1"},
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create status %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/octocat/hello/settings/hooks/") {
		t.Errorf("create redirected to %q, want the hook edit page", loc)
	}
	// The hook really exists now.
	list, err := fx.hooks.ListHooks(context.Background(), fx.ownerPK, "octocat", "hello")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Config.URL != "https://hooks.example.com/new" {
		t.Fatalf("created hook not found in list: %+v", list)
	}
}

func TestCreateHookRejectsBadURL(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	token := fx.csrfToken(t, "/octocat/hello/settings/hooks/new")
	resp := fx.post(t, "/octocat/hello/settings/hooks", token, url.Values{
		"payload_url":  {"not-a-real-url"},
		"content_type": {"json"},
		"subscribe":    {"choose"},
		"events":       {"push"},
		"active":       {"1"},
	})
	defer func() { _ = resp.Body.Close() }()
	// A bad URL re-renders the filled form with the inline error, not an error page.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bad-url status %d, want 200 (re-rendered form)", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if !strings.Contains(body, "form-error") {
		t.Errorf("bad-url submit is missing the inline error:\n%s", body)
	}
	if !strings.Contains(body, `value="not-a-real-url"`) {
		t.Errorf("bad-url submit did not echo the typed value back:\n%s", body)
	}
	// No hook was created.
	list, _ := fx.hooks.ListHooks(context.Background(), fx.ownerPK, "octocat", "hello")
	if len(list) != 0 {
		t.Errorf("a hook was created from an invalid submit: %+v", list)
	}
}

func TestUpdateHookKeepsSecretWhenBlank(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	secret := "s3cr3t"
	id := fx.seedHook(t, "https://hooks.example.com/old", &secret)
	path := "/octocat/hello/settings/hooks/" + itoa(id)
	token := fx.csrfToken(t, path)
	resp := fx.post(t, path, token, url.Values{
		"payload_url":  {"https://hooks.example.com/changed"},
		"content_type": {"json"},
		"subscribe":    {"choose"},
		"events":       {"push"},
		"active":       {"1"},
		// secret left blank: the stored secret must survive.
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("update status %d, want 303", resp.StatusCode)
	}
	hook, err := fx.hooks.GetHook(context.Background(), fx.ownerPK, "octocat", "hello", id)
	if err != nil {
		t.Fatalf("get hook: %v", err)
	}
	if hook.Config.URL != "https://hooks.example.com/changed" {
		t.Errorf("update did not change the URL: %q", hook.Config.URL)
	}
	if !hook.Config.HasSecret {
		t.Errorf("a blank secret field cleared the stored secret")
	}
}

func TestDeleteHookRemovesIt(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	id := fx.seedHook(t, "https://hooks.example.com/gone", nil)
	path := "/octocat/hello/settings/hooks/" + itoa(id)
	token := fx.csrfToken(t, path)
	resp := fx.post(t, path+"/delete", token, url.Values{})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete status %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/octocat/hello/settings/hooks" {
		t.Errorf("delete redirected to %q, want the hooks list", loc)
	}
	list, _ := fx.hooks.ListHooks(context.Background(), fx.ownerPK, "octocat", "hello")
	if len(list) != 0 {
		t.Errorf("the hook survived the delete: %+v", list)
	}
}

func TestDeliveryDetailAndRedeliver(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	id := fx.seedHook(t, "https://hooks.example.com/d", nil)

	// Seed one recorded delivery against the hook so the detail page has a request
	// and a response to render and the redeliver has something to replay.
	row, err := fx.store.GetWebhookForRepo(context.Background(), fx.repoPK, id)
	if err != nil {
		t.Fatalf("load webhook row: %v", err)
	}
	code := int64(200)
	d := &store.WebhookDeliveryRow{
		WebhookPK:       row.PK,
		GUID:            "11111111-2222-3333-4444-555555555555",
		Event:           "push",
		StatusCode:      &code,
		RequestURL:      "https://hooks.example.com/d",
		RequestHeaders:  `{"Content-Type":"application/json"}`,
		RequestBody:     `{"ref":"refs/heads/main"}`,
		ResponseHeaders: `{"Server":"example"}`,
		ResponseBody:    "ok",
		DurationMS:      42,
		Success:         true,
		CreatedAt:       time.Unix(1700000000, 0).UTC(),
	}
	if err := fx.store.InsertDelivery(context.Background(), d); err != nil {
		t.Fatalf("seed delivery: %v", err)
	}

	detail := "/octocat/hello/settings/hooks/" + itoa(id) + "/deliveries/" + itoa(d.DBID)
	resp, body := fx.get(t, detail)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delivery detail status %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{d.GUID, "Content-Type", "refs/heads/main", "Server", "Redeliver"} {
		if !strings.Contains(body, want) {
			t.Errorf("delivery detail is missing %q:\n%s", want, body)
		}
	}

	// Redeliver enqueues a replay and returns to the hook. The count is a delta
	// because creating the hook already enqueued its ping.
	before := fx.enq.calls
	token := fx.csrfToken(t, detail)
	rr := fx.post(t, detail+"/redeliver", token, url.Values{})
	defer func() { _ = rr.Body.Close() }()
	if rr.StatusCode != http.StatusSeeOther {
		t.Fatalf("redeliver status %d, want 303", rr.StatusCode)
	}
	if got := fx.enq.calls - before; got != 1 {
		t.Errorf("redeliver enqueued %d jobs, want 1", got)
	}
}

func TestNonAdminGetsNotFound(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	// octocat can see the public repo mallory/widgets but does not administer it, so
	// its settings surface 404s rather than 403s: the surface never confirms its own
	// existence to someone who cannot use it.
	resp, _ := fx.get(t, "/mallory/widgets/settings/hooks")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("non-admin hooks status = %d, want 404", resp.StatusCode)
	}
}

func TestAnonymousGetsNotFound(t *testing.T) {
	fx := newFixture(t)
	// No login: an anonymous viewer administers nothing, so even the owner's own
	// repo settings 404 to them.
	resp, _ := fx.get(t, "/octocat/hello/settings/hooks")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("anonymous hooks status = %d, want 404", resp.StatusCode)
	}
}

func TestRootServesGeneral(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	resp, body := fx.get(t, "/octocat/hello/settings")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("settings root status %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, `<h1 class="settings-title">General</h1>`) {
		t.Errorf("settings root should serve the General page:\n%s", body)
	}
}

// itoa formats a hook or delivery id for a URL.
func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

package repo

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
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
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// newRepoFixture is the create-repository test harness: a live TLS httptest
// server mounting /new with the real session and CSRF middleware over a real
// sqlite store and domain repo service, plus the repo home so the post-create
// redirect can be followed onto the quick-setup page. One seeded user can sign
// in through a tiny test login route; the anonymous tests just skip it.
type newRepoFixture struct {
	srv     *httptest.Server
	client  *http.Client
	repos   *domain.RepoService
	ownerPK int64
}

func newNewRepoFixture(t *testing.T) newRepoFixture {
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

	u := &store.UserRow{Login: "octocat", Type: "User"}
	if err := st.InsertUser(ctx, u); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	repoSvc := domain.NewRepoService(st, gitStore)

	renderSet, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	h := New(Deps{
		Repos:  repoSvc,
		URLs:   presenter.NewURLBuilder(testURLs(t)),
		Render: renderSet,
		View:   view.NewBuilder("Githome"),
		Logger: discard,
	})

	sessions := webmw.NewSessions(testNewSessionKey, time.Hour, func(_ context.Context, pk int64) (*view.Viewer, error) {
		if pk == u.PK {
			return &view.Viewer{Login: "octocat", Name: "The Octocat"}, nil
		}
		return nil, nil
	})
	csrf := webmw.NewCSRF(renderSet)

	root := mizu.NewRouter()
	page := root.With(sessions.Middleware(), webmw.ColorMode(), csrf.Middleware())

	page.Get("/_test/login", func(c *mizu.Ctx) error {
		sessions.Issue(c, u.PK, time.Now())
		return c.Text(http.StatusOK, "ok")
	})

	page.Get("/new", h.NewForm)
	page.Post("/new", h.CreateRepo)
	rg := page.With(h.Resolve)
	rg.Get("/{owner}/{repo}", h.Home)

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

	return newRepoFixture{srv: srv, client: client, repos: repoSvc, ownerPK: u.PK}
}

var testNewSessionKey = []byte("githome-reponew-test-sessionkey!")

// login hits the test login route so the client jar carries the owner's session.
func (fx newRepoFixture) login(t *testing.T) {
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

// getBody issues a GET and returns the response and body.
func (fx newRepoFixture) getBody(t *testing.T, path string) (*http.Response, string) {
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

// csrfFromForm GETs the form and reads the token its hidden field echoes.
func (fx newRepoFixture) csrfFromForm(t *testing.T) string {
	t.Helper()
	_, body := fx.getBody(t, "/new")
	const marker = `name="_csrf" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no csrf token on /new:\n%s", body)
	}
	rest := body[i+len(marker):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		t.Fatalf("unterminated csrf token on /new")
	}
	return rest[:j]
}

// postForm submits the create form with the CSRF field included.
func (fx newRepoFixture) postForm(t *testing.T, csrf string, form url.Values) (*http.Response, string) {
	t.Helper()
	form.Set("_csrf", csrf)
	resp, err := fx.client.PostForm(fx.srv.URL+"/new", form)
	if err != nil {
		t.Fatalf("POST /new: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read POST /new: %v", err)
	}
	return resp, string(b)
}

func TestNewRepoAnonymousBouncesToLogin(t *testing.T) {
	fx := newNewRepoFixture(t)
	resp, _ := fx.getBody(t, "/new")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("anonymous /new status = %d, want 302", resp.StatusCode)
	}
	want := "/login?return_to=" + url.QueryEscape("/new")
	if got := resp.Header.Get("Location"); got != want {
		t.Errorf("anonymous /new location = %q, want %q", got, want)
	}
}

func TestNewRepoFormRenders(t *testing.T) {
	fx := newNewRepoFixture(t)
	fx.login(t)
	resp, body := fx.getBody(t, "/new")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/new status = %d, want 200", resp.StatusCode)
	}
	// The owner is fixed to the viewer, and the form carries the name input
	// and both visibility choices.
	if !strings.Contains(body, "octocat") {
		t.Errorf("/new is missing the owner login:\n%s", body)
	}
	if !strings.Contains(body, `name="name"`) || !strings.Contains(body, `name="visibility"`) {
		t.Errorf("/new is missing the form fields:\n%s", body)
	}
	if !strings.Contains(body, "Create a new repository") {
		t.Errorf("/new is missing the heading:\n%s", body)
	}
}

func TestNewRepoCreateRedirectsToRepoHome(t *testing.T) {
	fx := newNewRepoFixture(t)
	fx.login(t)
	csrf := fx.csrfFromForm(t)

	resp, _ := fx.postForm(t, csrf, url.Values{
		"name":        {"spoon-knife"},
		"description": {"a test repo"},
		"visibility":  {"private"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create status = %d, want 303", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/octocat/spoon-knife" {
		t.Errorf("create location = %q, want /octocat/spoon-knife", got)
	}

	// The repository exists through the same service the REST surface reads,
	// private as asked, and its empty home renders the quick-setup view.
	repo, err := fx.repos.GetRepo(context.Background(), fx.ownerPK, "octocat", "spoon-knife")
	if err != nil {
		t.Fatalf("created repo not readable: %v", err)
	}
	if !repo.Private {
		t.Errorf("created repo is public, want private")
	}
	if repo.Description == nil || *repo.Description != "a test repo" {
		t.Errorf("created repo description = %v, want %q", repo.Description, "a test repo")
	}
	_, home := fx.getBody(t, "/octocat/spoon-knife")
	if !strings.Contains(home, "Quick setup") {
		t.Errorf("new repo home is missing the quick-setup view:\n%s", home)
	}
}

func TestNewRepoRejectsBadAndTakenNames(t *testing.T) {
	fx := newNewRepoFixture(t)
	fx.login(t)
	csrf := fx.csrfFromForm(t)

	// A name that cannot live in a URL path re-renders the form with the
	// message inline and the rest of the input preserved.
	resp, body := fx.postForm(t, csrf, url.Values{
		"name":        {"not a name"},
		"description": {"kept"},
		"visibility":  {"public"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("invalid name status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "may only contain") {
		t.Errorf("invalid name is missing the inline error:\n%s", body)
	}
	if !strings.Contains(body, "kept") {
		t.Errorf("invalid name lost the typed description:\n%s", body)
	}

	// A name already taken under the viewer reports the conflict: the first
	// create succeeds, the repeat re-renders with the message.
	resp, _ = fx.postForm(t, csrf, url.Values{"name": {"hello"}, "visibility": {"public"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("first create status = %d, want 303", resp.StatusCode)
	}
	resp, body = fx.postForm(t, csrf, url.Values{"name": {"hello"}, "visibility": {"public"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("taken name status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "already exists") {
		t.Errorf("taken name is missing the conflict error:\n%s", body)
	}
}

func TestNewRepoAnonymousPostBounces(t *testing.T) {
	fx := newNewRepoFixture(t)
	resp, err := fx.client.PostForm(fx.srv.URL+"/new", url.Values{"name": {"sneaky"}})
	if err != nil {
		t.Fatalf("POST /new: %v", err)
	}
	_ = resp.Body.Close()
	// The CSRF guard rejects the tokenless post before the handler ever runs;
	// either way an anonymous post never creates anything.
	if resp.StatusCode == http.StatusSeeOther {
		t.Fatalf("anonymous create unexpectedly succeeded: %d", resp.StatusCode)
	}
	if _, err := fx.repos.GetRepo(context.Background(), fx.ownerPK, "octocat", "sneaky"); err == nil {
		t.Errorf("anonymous post created a repository")
	}
}

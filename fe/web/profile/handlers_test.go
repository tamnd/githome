package profile

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/config"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/assets"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// fixture is the profile web test harness: a live httptest server mounting the
// catch-all profile handler over a real sqlite store, a real user service, a real
// event service, and a real domain search (the same one the repositories tab and
// the overview grid read), plus the seeded names the assertions address. The
// viewer is anonymous, the visibility floor: a public account's identity, its
// public repositories, and its public activity all render, and a private repo the
// anonymous viewer cannot see never appears in either repository list.
type fixture struct {
	srv  *httptest.Server
	user string // the seeded user login
	org  string // the seeded organization login
	repo string // the seeded public repo name under the user
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

	// The user account carries every optional vcard field so the header and its
	// links all have real data to assert against.
	name := "The Octocat"
	bio := "building things at night"
	company := "Acme"
	location := "San Francisco"
	email := "octo@example.com"
	twitter := "@monatest"
	octocat := &store.UserRow{
		Login:           "octocat",
		Type:            "User",
		Name:            &name,
		Bio:             &bio,
		Company:         &company,
		Location:        &location,
		Email:           &email,
		Blog:            "octocat.example.com",
		TwitterUsername: &twitter,
		PublicRepos:     1,
		Followers:       42,
		Following:       7,
	}
	if err := st.InsertUser(ctx, octocat); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	// An organization account, so the profile's org badge and the org: search
	// qualifier both have a real account to resolve.
	acme := &store.UserRow{Login: "acme", Type: "Organization", PublicRepos: 1, Followers: 3}
	if err := st.InsertUser(ctx, acme); err != nil {
		t.Fatalf("insert org: %v", err)
	}

	desc := "the hello repo"
	hello := &store.RepoRow{OwnerPK: octocat.PK, Name: "hello", Description: &desc, DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, hello); err != nil {
		t.Fatalf("insert hello: %v", err)
	}
	widgets := &store.RepoRow{OwnerPK: acme.PK, Name: "widgets", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, widgets); err != nil {
		t.Fatalf("insert widgets: %v", err)
	}
	// A private repo under the user the anonymous viewer must never see in either
	// the overview grid or the repositories tab.
	secret := &store.RepoRow{OwnerPK: octocat.PK, Name: "secret", Private: true, DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, secret); err != nil {
		t.Fatalf("insert secret: %v", err)
	}

	// Two public events on the hello repo, seeded with the rendered Events-API
	// payloads the fan-out worker would store, so the activity timeline has a push
	// line (with a branch) and an opened-issue line (with a numbered subject).
	push := &store.EventRow{
		Event:   domain.EventPush,
		ActorPK: octocat.PK,
		RepoPK:  hello.PK,
		Payload: `{"ref":"refs/heads/main"}`,
		Public:  true,
	}
	if err := st.InsertEvent(ctx, push); err != nil {
		t.Fatalf("insert push event: %v", err)
	}
	issue := &store.EventRow{
		Event:   domain.EventIssues,
		Action:  "opened",
		ActorPK: octocat.PK,
		RepoPK:  hello.PK,
		Payload: `{"action":"opened","issue":{"number":1,"title":"first findme issue"}}`,
		Public:  true,
	}
	if err := st.InsertEvent(ctx, issue); err != nil {
		t.Fatalf("insert issues event: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	searchSvc := domain.NewSearchService(st, repoSvc, issueSvc, gitStore)
	userSvc := domain.NewUserService(st)
	eventSvc := domain.NewEventService(st, repoSvc)

	renderSet, err := render.New(assets.FS(), false)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	h := New(Deps{
		Users:  userSvc,
		Events: eventSvc,
		Search: searchSvc,
		URLs:   presenter.NewURLBuilder(testURLs(t)),
		Render: renderSet,
		View:   view.NewBuilder("Githome"),
		Logger: discard,
	})

	root := mizu.NewRouter()
	page := root.With(webmw.ColorMode())
	pg := page.With(h.Resolve)
	pg.Get("/{owner}", h.Show)

	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	return fixture{srv: srv, user: "octocat", org: "acme", repo: "hello"}
}

func testURLs(t *testing.T) config.URLs {
	t.Helper()
	must := func(s string) *url.URL {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return u
	}
	return config.URLs{
		API:     must("https://git.test.internal/api/v3"),
		HTML:    must("https://git.test.internal"),
		GraphQL: must("https://git.test.internal/api/graphql"),
		SSHHost: "git.test.internal",
		SSHPort: 22,
	}
}

// get issues a no-redirect GET and returns the response and body.
func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(srv.URL + path)
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

func TestOverviewRendersIdentityAndVcard(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// The identity card carries the display name and the login.
	for _, want := range []string{"The Octocat", "octocat", "building things at night"} {
		if !strings.Contains(body, want) {
			t.Errorf("profile is missing identity text %q:\n%s", want, body)
		}
	}
	// The vcard rows: company, location, the mailto link, the normalized blog link,
	// the twitter handle, and the join date.
	for _, want := range []string{
		"Acme",
		"San Francisco",
		`href="mailto:octo@example.com"`,
		`href="https://octocat.example.com"`,
		`href="https://twitter.com/monatest"`,
		"Joined",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("vcard is missing %q:\n%s", want, body)
		}
	}
	// The follower count reads from the account.
	if !strings.Contains(body, "42") {
		t.Errorf("profile is missing the follower count:\n%s", body)
	}
}

func TestOverviewRendersRepositoriesAndActivity(t *testing.T) {
	fx := newFixture(t)
	_, body := get(t, fx.srv, "/octocat")
	// The overview grid lists the public repo.
	if !strings.Contains(body, "octocat/hello") {
		t.Errorf("overview is missing the repository card:\n%s", body)
	}
	// The activity timeline carries both seeded events, phrased by type and action.
	for _, want := range []string{"pushed to", "opened an issue in", "#1 first findme issue"} {
		if !strings.Contains(body, want) {
			t.Errorf("activity is missing %q:\n%s", want, body)
		}
	}
	// The private repo never appears in the anonymous overview.
	if strings.Contains(body, "octocat/secret") {
		t.Errorf("private repo leaked into the anonymous overview:\n%s", body)
	}
}

func TestRepositoriesTab(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/octocat?tab=repositories")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// The repositories tab lists the visible repo with its sort menu.
	if !strings.Contains(body, "octocat/hello") {
		t.Errorf("repositories tab is missing the repo row:\n%s", body)
	}
	if !strings.Contains(body, "profile-sort") {
		t.Errorf("repositories tab is missing the sort menu:\n%s", body)
	}
	// The active tab is marked in the strip.
	if !strings.Contains(body, `aria-current="page"`) {
		t.Errorf("repositories tab is missing the active-tab marker:\n%s", body)
	}
	// The private repo never appears in the anonymous repositories tab.
	if strings.Contains(body, "octocat/secret") {
		t.Errorf("private repo leaked into the anonymous repositories tab:\n%s", body)
	}
}

func TestOrganizationProfile(t *testing.T) {
	fx := newFixture(t)
	resp, body := get(t, fx.srv, "/acme")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// An organization wears the Organization badge.
	if !strings.Contains(body, "Organization") {
		t.Errorf("organization profile is missing the org badge:\n%s", body)
	}
	// The org's repository resolves through the org: qualifier on its repositories
	// tab.
	_, repos := get(t, fx.srv, "/acme?tab=repositories")
	if !strings.Contains(repos, "acme/widgets") {
		t.Errorf("organization repositories tab is missing the org repo:\n%s", repos)
	}
}

func TestUnknownAccountNotFound(t *testing.T) {
	fx := newFixture(t)
	resp, _ := get(t, fx.srv, "/doesnotexist")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown account status = %d, want 404", resp.StatusCode)
	}
}

func TestReservedNameNotFound(t *testing.T) {
	fx := newFixture(t)
	// A reserved top-level name is never read as a login: the profile resolver 404s
	// it rather than resolving an account, so a future /settings surface is never
	// shadowed by a user who registered the login "settings".
	resp, _ := get(t, fx.srv, "/settings")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("reserved name status = %d, want 404", resp.StatusCode)
	}
}

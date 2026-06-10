package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/api/graphql"
	"github.com/tamnd/githome/api/rest"
	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/config"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe"
	"github.com/tamnd/githome/fe/assets"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/gittransport"
	"github.com/tamnd/githome/markup"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// TestServerMountsWithoutRouteConflict composes the whole server router the way
// run does, with the REST API, GraphQL, the git transport and the web front all
// on one mizu router, and asserts two things: mounting does not panic, and a
// request to each surface lands on the surface that owns it.
//
// It guards a class of bug the per-package tests cannot see. mizu wraps a single
// net/http mux, and the Go 1.22 mux rejects overlapping wildcard patterns at
// registration: the web front's /{owner}/{repo} routes overlap both the
// dotcom-style root REST mount and the /assets tree (an owner could be named
// "repos" or "assets"). Each package test mounts its own surface alone, so none
// of them ever registered the combination that panics. This test does, so the
// /api/v3 API separation and the assets-ahead-of-root dispatch stay in place.
func TestServerMountsWithoutRouteConflict(t *testing.T) {
	ctx := context.Background()

	st, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "githome.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	pullSvc := domain.NewPRService(st, repoSvc, issueSvc, gitStore)
	reviewSvc := domain.NewReviewService(st, repoSvc, pullSvc, issueSvc, gitStore)
	checksSvc := domain.NewChecksService(st, repoSvc, issueSvc, gitStore)
	userSvc := domain.NewUserService(st)
	hookSvc := domain.NewHookService(st, repoSvc, nil)
	eventSvc := domain.NewEventService(st, repoSvc)
	searchSvc := domain.NewSearchService(st, repoSvc, issueSvc, gitStore)

	authSvc := auth.NewService(st, "https://git.test.internal")
	t.Cleanup(authSvc.Close)

	cfg := testConfig(t)
	urls := presenter.NewURLBuilder(cfg.URLs)

	root := mizu.NewRouter()
	handler := mountAll(t, root, cfg, st, authSvc, repoSvc, userSvc, issueSvc, pullSvc, reviewSvc, checksSvc, hookSvc, eventSvc, searchSvc, urls)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	cases := []struct {
		name     string
		path     string
		wantCode int
		wantType string // a Content-Type prefix that proves which surface answered
	}{
		// The REST health probe stays at the bare root, outside the API version.
		{"rest health", "/healthz", http.StatusOK, ""},
		// A missing repo through the versioned API answers JSON: the REST surface
		// owns /api/v3, not the web front.
		{"rest api", "/api/v3/repos/ghost/ghost", http.StatusNotFound, "application/json"},
		// A missing repo at the bare root answers an HTML 404: the web front owns
		// /{owner}/{repo}, which is the route that used to collide with REST.
		{"web repo", "/ghost/ghost", http.StatusNotFound, "text/html"},
		// An unknown asset reaches the asset router (a 404 from it, not a mux
		// error), proving /assets is dispatched ahead of the owner-space routes.
		{"asset tree", "/assets/does-not-exist.css", http.StatusNotFound, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Get(srv.URL + tc.path)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}
			t.Cleanup(func() { _ = resp.Body.Close() })
			if resp.StatusCode != tc.wantCode {
				t.Errorf("GET %s: status = %d, want %d", tc.path, resp.StatusCode, tc.wantCode)
			}
			if tc.wantType != "" && !strings.HasPrefix(resp.Header.Get("Content-Type"), tc.wantType) {
				t.Errorf("GET %s: content-type = %q, want prefix %q", tc.path, resp.Header.Get("Content-Type"), tc.wantType)
			}
		})
	}
}

// mountAll composes every surface onto root exactly as run does and returns the
// servable handler (the web front wraps root to serve its assets ahead of it). A
// route-registration conflict surfaces here as a panic, which the recover turns
// into a clear test failure naming the conflicting patterns.
func mountAll(t *testing.T, root *mizu.Router, cfg config.Config, st *store.Store, authSvc *auth.Service,
	repoSvc *domain.RepoService, userSvc *domain.UserService, issueSvc *domain.IssueService,
	pullSvc *domain.PRService, reviewSvc *domain.ReviewService, checksSvc *domain.ChecksService,
	hookSvc *domain.HookService, eventSvc *domain.EventService, searchSvc *domain.SearchService,
	urls *presenter.URLBuilder) (handler http.Handler) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("mounting the combined router panicked (route conflict): %v", r)
		}
	}()

	rest.Mount(root, rest.Deps{
		Config: cfg, WebFront: cfg.Web.Enabled, Ready: st, Auth: authSvc,
		Users: userSvc, Repos: repoSvc, Issues: issueSvc, Pulls: pullSvc,
		Reviews: reviewSvc, Checks: checksSvc, Hooks: hookSvc, Events: eventSvc,
		Search: searchSvc, URLs: urls, NodeFormat: nodeid.FormatNew,
	})
	graphql.Mount(root, graphql.Deps{
		Auth: authSvc, Repos: repoSvc, Issues: issueSvc, Pulls: pullSvc,
		Reviews: reviewSvc, Checks: checksSvc, Users: userSvc,
		Batch: domain.NewBatcher(st), URLs: urls, NodeFormat: nodeid.FormatNew,
	})
	gittransport.Mount(root, &gittransport.Service{Repos: repoSvc, Git: git.NewStore(t.TempDir()), Pulls: pullSvc, Auth: authSvc})

	renderSet, err := render.New(assets.FS(), assets.Dev())
	if err != nil {
		t.Fatalf("render set: %v", err)
	}
	markupRenderer := markup.New(markup.Config{BaseURL: cfg.URLs.HTML.String()})
	lookup := func(context.Context, int64) (*view.Viewer, error) { return nil, nil }
	return fe.Mount(root, fe.Deps{
		Render: renderSet, View: view.NewBuilder(cfg.Web.SiteName),
		Repos: repoSvc, Hooks: hookSvc, Checks: checksSvc, Issues: issueSvc,
		Pulls: pullSvc, Reviews: reviewSvc, Search: searchSvc, Users: userSvc,
		Events: eventSvc, URLs: urls, Markup: markupRenderer,
		Sessions: webmw.NewSessions(make([]byte, 32), 0, lookup),
		CSRF:     webmw.NewCSRF(renderSet),
		Flash:    webmw.NewFlash(make([]byte, 32)),
	})
}

// testConfig builds the minimal configuration the surfaces read: the on-host
// base URLs and the web front turned on so the single-host layout (web at root,
// API under /api/v3) is the one under test.
func testConfig(t *testing.T) config.Config {
	t.Helper()
	mustURL := func(s string) *url.URL {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return u
	}
	var cfg config.Config
	cfg.Web.Enabled = true
	cfg.Web.SiteName = "Githome"
	cfg.Server.MaxBodyBytes = 25 << 20
	cfg.URLs = config.URLs{
		API:     mustURL("https://git.test.internal/api/v3"),
		HTML:    mustURL("https://git.test.internal"),
		GraphQL: mustURL("https://git.test.internal/api/graphql"),
		SSHHost: "git.test.internal",
		SSHPort: 22,
	}
	return cfg
}

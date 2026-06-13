package main

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/conform"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// TestConformanceGate boots the whole server in process — REST, GraphQL and the
// git transport on one router, exactly as run composes them — seeds a repository
// with content and a handful of issues, and runs the black-box conformance
// matrix (package conform, the same one the githome-conform binary runs against
// a live origin) against it. Asserting the matrix reports zero failures wires
// the compatibility suite into the normal `go test ./...` CI leg, so every
// review fix lands verified end to end rather than only against its own
// hand-written contract goldens. R01-70.
func TestConformanceGate(t *testing.T) {
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

	// Seed the target the matrix reads: the octocat user, a repo-scoped token,
	// the octocat/hello repository initialized with a README commit so it has
	// git content (and therefore an ETag), and a few issues so the issue list
	// pages and the Link header advertises next/last under per_page=1.
	owner := &store.UserRow{Login: "octocat", Type: "User"}
	if err := st.InsertUser(ctx, owner); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	token := seedConformToken(t, st, owner.PK)
	if _, err := repoSvc.CreateRepo(ctx, owner.PK, "octocat", domain.RepoInput{
		Name: "hello", AutoInit: true, DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	for _, title := range []string{"first issue", "second issue", "third issue"} {
		if _, err := issueSvc.CreateIssue(ctx, owner.PK, "octocat", "hello", domain.IssueInput{Title: title}); err != nil {
			t.Fatalf("create issue %q: %v", title, err)
		}
	}

	cfg := testConfig(t)
	urls := presenter.NewURLBuilder(cfg.URLs)
	root := mizu.NewRouter()
	handler := mountAll(t, root, cfg, st, authSvc, repoSvc, userSvc, issueSvc, pullSvc, reviewSvc, checksSvc, hookSvc, eventSvc, searchSvc, urls)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	rep := conform.Run(conform.Options{
		API:     srv.URL + "/api/v3",
		GraphQL: srv.URL + "/api/graphql",
		Token:   token,
		Owner:   "octocat",
		Repo:    "hello",
	})
	if rep.Failed() {
		var b strings.Builder
		_ = rep.Print(&b, "octocat/hello @ in-process")
		t.Fatalf("conformance gate failed (%d of %d checks):\n%s\n%s",
			rep.CountFailed(), rep.Total(), strings.Join(rep.Failures(), "\n"), b.String())
	}
}

// seedConformToken inserts a repo-scoped classic PAT for userPK and returns the
// plaintext token the matrix authenticates with.
func seedConformToken(t *testing.T, st *store.Store, userPK int64) string {
	t.Helper()
	g, err := auth.GenerateToken(auth.PrefixClassicPAT)
	if err != nil {
		t.Fatal(err)
	}
	hash := g.Hash
	if err := st.InsertToken(context.Background(), &store.TokenRow{
		UserPK: &userPK, TokenHash: hash[:], TokenPrefix: auth.PrefixClassicPAT,
		LastEight: g.Last8, Kind: "pat", Scopes: "repo",
	}); err != nil {
		t.Fatalf("insert token: %v", err)
	}
	return g.Plaintext
}

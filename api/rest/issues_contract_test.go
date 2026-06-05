package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// issueFixture is a REST server backed by a store seeded with owner octocat and
// repo hello, plus the owner's token. The issue subsystem needs no git objects,
// so unlike the ref-write fixture it does not build a bare repository.
type issueFixture struct {
	srv   *httptest.Server
	token string
	st    *store.Store
}

// issueServer mounts the full authenticated REST surface over a fresh store with
// the issue service wired, returning the server, the owner's plaintext token,
// and the store so a test can read back the durable job queue.
func issueServer(t *testing.T) issueFixture {
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
	g, err := auth.GenerateToken(auth.PrefixClassicPAT)
	if err != nil {
		t.Fatal(err)
	}
	hash := g.Hash
	if err := st.InsertToken(ctx, &store.TokenRow{
		UserPK: &u.PK, TokenHash: hash[:], TokenPrefix: auth.PrefixClassicPAT,
		LastEight: g.Last8, Kind: "pat", Scopes: "repo",
	}); err != nil {
		t.Fatalf("insert token: %v", err)
	}
	repo := &store.RepoRow{OwnerPK: u.PK, Name: "hello", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("insert repo: %v", err)
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
		Issues:     domain.NewIssueService(st, repoSvc),
		URLs:       presenter.NewURLBuilder(cfg.URLs),
		NodeFormat: nodeid.FormatNew,
	})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	return issueFixture{srv: srv, token: g.Plaintext, st: st}
}

func TestCreateIssueContract(t *testing.T) {
	fx := issueServer(t)
	// Seed a label so the created issue carries the embedded label shape.
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/labels", fx.token,
		`{"name":"bug","color":"#d73a4a","description":"Something is broken"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed label status %d, body %s", resp.StatusCode, body)
	}
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"Found a bug","body":"It crashes on start.","labels":["bug"]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "issue_create.golden.json", body)
}

func TestGetIssueContract(t *testing.T) {
	fx := issueServer(t)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"Found a bug","body":"It crashes on start."}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issue status %d, body %s", resp.StatusCode, body)
	}
	resp, body := get(t, fx.srv, "/repos/octocat/hello/issues/1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "issue_get.golden.json", body)
}

func TestCloseIssueContract(t *testing.T) {
	fx := issueServer(t)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"Close me"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issue status %d, body %s", resp.StatusCode, body)
	}
	resp, body := authedSend(t, fx.srv, http.MethodPatch, "/repos/octocat/hello/issues/1", fx.token,
		`{"state":"closed"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "issue_close.golden.json", body)
}

func TestIssueCommentContract(t *testing.T) {
	fx := issueServer(t)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"Discuss"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issue status %d, body %s", resp.StatusCode, body)
	}
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues/1/comments", fx.token,
		`{"body":"Thanks for the report."}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "issue_comment_create.golden.json", body)
}

func TestLabelContract(t *testing.T) {
	fx := issueServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/labels", fx.token,
		`{"name":"enhancement","color":"a2eeef","description":"New feature or request"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "label_create.golden.json", body)
}

func TestMilestoneContract(t *testing.T) {
	fx := issueServer(t)
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/milestones", fx.token,
		`{"title":"v1.0","description":"First stable release","due_on":"2026-12-31T08:00:00Z"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "milestone_create.golden.json", body)
}

func TestReactionContract(t *testing.T) {
	fx := issueServer(t)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"React to me"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issue status %d, body %s", resp.StatusCode, body)
	}
	resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues/1/reactions", fx.token,
		`{"content":"+1"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "reaction_create.golden.json", body)
}

// TestCreateIssueEnqueuesWebhookJob confirms the create records an event and
// enqueues the single deliver_event job that fans the activity out to the
// repository's hooks. The job carries the new event's pk in its payload.
func TestCreateIssueEnqueuesWebhookJob(t *testing.T) {
	fx := issueServer(t)
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"hook me"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d, body %s", resp.StatusCode, body)
	}
	jobs, err := fx.st.ListJobs(context.Background())
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	var found bool
	for _, j := range jobs {
		if j.Kind == "deliver_event" {
			found = true
		}
	}
	if !found {
		t.Fatalf("create did not enqueue a deliver_event job: %+v", jobs)
	}
}

func TestIssueWriteErrors(t *testing.T) {
	fx := issueServer(t)

	// Anonymous (no token) cannot open an issue on a visible repo: 403.
	if resp, _ := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", "",
		`{"title":"x"}`); resp.StatusCode != http.StatusForbidden {
		t.Errorf("anon create status %d, want 403", resp.StatusCode)
	}

	// A missing title is 422 before any write.
	if resp, _ := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"body":"no title"}`); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("missing-title status %d, want 422", resp.StatusCode)
	}

	// A fetch of an issue that does not exist is 404.
	if resp, _ := get(t, fx.srv, "/repos/octocat/hello/issues/999"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing issue status %d, want 404", resp.StatusCode)
	}

	// An issue under an invisible repository is 404, never 403.
	if resp, _ := get(t, fx.srv, "/repos/octocat/nope/issues/1"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing repo status %d, want 404", resp.StatusCode)
	}

	// Seed an issue, then react with an unknown content: 422.
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"react"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed status %d, body %s", resp.StatusCode, body)
	}
	if resp, _ := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues/1/reactions", fx.token,
		`{"content":"thumbsup"}`); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("bad-reaction status %d, want 422", resp.StatusCode)
	}

	// A duplicate label name is 422.
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/labels", fx.token,
		`{"name":"dup"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed label status %d, body %s", resp.StatusCode, body)
	}
	if resp, _ := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/labels", fx.token,
		`{"name":"DUP"}`); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("duplicate-label status %d, want 422", resp.StatusCode)
	}
}

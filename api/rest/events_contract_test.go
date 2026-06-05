package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
	"github.com/tamnd/githome/webhook"
	"github.com/tamnd/githome/worker"
)

// eventFixture is a REST server whose store has recorded one issue event and run
// the deliver_event job, so the activity feed serves a fully rendered payload
// the same way it would in the running server.
type eventFixture struct {
	srv     *httptest.Server
	token   string
	runtime *worker.Runtime
	ctx     context.Context
}

func eventServer(t *testing.T) eventFixture {
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
		t.Fatalf("insert user: %v", err)
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
	issueSvc := domain.NewIssueService(st, repoSvc)
	pullSvc := domain.NewPRService(st, repoSvc, issueSvc, gitStore)
	userSvc := domain.NewUserService(st)
	enq := worker.NewStoreEnqueuer(st)
	urls := presenter.NewURLBuilder(cfg.URLs)

	renderer := webhook.NewRenderer(repoSvc, issueSvc, pullSvc, userSvc, urls, nodeid.FormatNew)
	deliverer := webhook.NewDeliverer(st, renderer, nil, enq, "test")
	rt := worker.NewRuntime(st, nil, time.Millisecond)
	rt.Register(domain.JobDeliverEvent, deliverer.DeliverEventHandler())
	rt.Register(domain.JobDeliverWebhook, deliverer.DeliverWebhookHandler())

	root := mizu.NewRouter()
	Mount(root, Deps{
		Config:     cfg,
		Ready:      st,
		Auth:       authSvc,
		Users:      userSvc,
		Repos:      repoSvc,
		Issues:     issueSvc,
		Pulls:      pullSvc,
		Events:     domain.NewEventService(st, repoSvc),
		URLs:       urls,
		NodeFormat: nodeid.FormatNew,
	})
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)

	return eventFixture{srv: srv, token: seedToken(t, st, owner.PK), runtime: rt, ctx: ctx}
}

// drain runs the queue until it is empty so a recorded event has its rendered
// payload stored, the state the feed reads from.
func (fx eventFixture) drain(t *testing.T) {
	t.Helper()
	for i := 0; i < 50; i++ {
		worked, err := fx.runtime.RunOnce(fx.ctx)
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if !worked {
			return
		}
	}
	t.Fatal("queue did not drain")
}

// seedIssueEvent opens an issue, which records an `issues` event, then drains the
// queue so the event carries its rendered payload.
func (fx eventFixture) seedIssueEvent(t *testing.T) {
	t.Helper()
	if resp, body := authedSend(t, fx.srv, http.MethodPost, "/repos/octocat/hello/issues", fx.token,
		`{"title":"Found a bug","body":"It crashes on start."}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed issue status %d, body %s", resp.StatusCode, body)
	}
	fx.drain(t)
}

func TestPublicEventsContract(t *testing.T) {
	fx := eventServer(t)
	fx.seedIssueEvent(t)
	// The global timeline is readable without authentication.
	resp, body := get(t, fx.srv, "/events")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "events_public.golden.json", body)
}

func TestRepoEventsContract(t *testing.T) {
	fx := eventServer(t)
	fx.seedIssueEvent(t)
	resp, body := get(t, fx.srv, "/repos/octocat/hello/events")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "events_repo.golden.json", body)
}

func TestUserEventsContract(t *testing.T) {
	fx := eventServer(t)
	fx.seedIssueEvent(t)
	resp, body := get(t, fx.srv, "/users/octocat/events")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200, body %s", resp.StatusCode, body)
	}
	assertWriteGolden(t, "events_user.golden.json", body)
}

func TestEventErrors(t *testing.T) {
	fx := eventServer(t)

	// A feed for a repository that does not exist is 404.
	if resp, _ := get(t, fx.srv, "/repos/octocat/nope/events"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing repo status %d, want 404", resp.StatusCode)
	}

	// A feed for a user that does not exist is 404.
	if resp, _ := get(t, fx.srv, "/users/ghost/events"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing user status %d, want 404", resp.StatusCode)
	}

	// Before any activity the public timeline is an empty array, not null.
	if resp, body := get(t, fx.srv, "/events"); resp.StatusCode != http.StatusOK || string(body) != "[]" {
		t.Errorf("empty timeline status %d body %s", resp.StatusCode, body)
	}
}

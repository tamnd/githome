package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tamnd/githome/config"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
	"github.com/tamnd/githome/worker"
)

// capturedDelivery is one request the test receiver recorded.
type capturedDelivery struct {
	headers http.Header
	body    []byte
}

// receiver is an httptest endpoint standing in for a hook's URL. It records each
// delivery so the test can assert on the signature, headers, and body.
type receiver struct {
	mu         sync.Mutex
	deliveries []capturedDelivery
	status     int
}

func (r *receiver) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.deliveries = append(r.deliveries, capturedDelivery{headers: req.Header.Clone(), body: body})
		status := r.status
		r.mu.Unlock()
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
	}
}

func (r *receiver) last() (capturedDelivery, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.deliveries) == 0 {
		return capturedDelivery{}, false
	}
	return r.deliveries[len(r.deliveries)-1], true
}

// deliverFixture wires a real sqlite store, the domain services, and a deliverer
// whose client is allowed to reach the loopback test receiver. It is the harness
// the acceptance gate runs through: record an event, drain the queue, inspect
// what the receiver got.
type deliverFixture struct {
	ctx      context.Context
	st       *store.Store
	issues   *domain.IssueService
	pulls    *domain.PRService
	hooks    *domain.HookService
	runtime  *worker.Runtime
	rcv      *receiver
	srv      *httptest.Server
	ownerPK  int64
	repoName string
}

func newDeliverFixture(t *testing.T) *deliverFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "githome.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	owner := &store.UserRow{Login: "octocat", Type: "User"}
	if err := st.InsertUser(ctx, owner); err != nil {
		t.Fatalf("InsertUser: %v", err)
	}
	repo := &store.RepoRow{OwnerPK: owner.PK, Name: "hello", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("InsertRepo: %v", err)
	}

	gitStore := git.NewStore(t.TempDir())
	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	pullSvc := domain.NewPRService(st, repoSvc, issueSvc, gitStore)
	userSvc := domain.NewUserService(st)
	enq := worker.NewStoreEnqueuer(st)
	hookSvc := domain.NewHookService(st, repoSvc, enq)

	rcv := &receiver{}
	srv := httptest.NewServer(rcv.handler())
	t.Cleanup(srv.Close)

	urls := presenter.NewURLBuilder(testURLs(t))
	renderer := NewRenderer(repoSvc, issueSvc, pullSvc, userSvc, urls, nodeid.FormatNew)
	// The receiver runs on loopback, which the guard blocks by default; the test
	// client opts loopback in the way an operator would for an internal endpoint.
	client := NewClient(ClientOptions{Allow: []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("::1/128"),
	}})
	deliverer := NewDeliverer(st, renderer, client, enq, "test")

	rt := worker.NewRuntime(st, nil, time.Millisecond)
	rt.Register(domain.JobDeliverEvent, deliverer.DeliverEventHandler())
	rt.Register(domain.JobDeliverWebhook, deliverer.DeliverWebhookHandler())

	return &deliverFixture{
		ctx: ctx, st: st, issues: issueSvc, pulls: pullSvc, hooks: hookSvc,
		runtime: rt, rcv: rcv, srv: srv, ownerPK: owner.PK, repoName: "hello",
	}
}

// drain runs the queue until it is empty, the in-process equivalent of the
// runtime loop catching up on the fan-out a write enqueued.
func (f *deliverFixture) drain(t *testing.T) {
	t.Helper()
	for i := 0; i < 50; i++ {
		worked, err := f.runtime.RunOnce(f.ctx)
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if !worked {
			return
		}
	}
	t.Fatal("queue did not drain")
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

func TestIssueEventSignedDelivery(t *testing.T) {
	f := newDeliverFixture(t)
	secret := "swordfish"
	if _, err := f.hooks.CreateHook(f.ctx, f.ownerPK, "octocat", f.repoName, domain.HookInput{
		URL:    f.srv.URL,
		Secret: &secret,
		Events: []string{"issues"},
	}); err != nil {
		t.Fatalf("CreateHook: %v", err)
	}

	body := "the body"
	if _, err := f.issues.CreateIssue(f.ctx, f.ownerPK, "octocat", f.repoName, domain.IssueInput{Title: "first", Body: &body}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	f.drain(t)

	got, ok := f.rcv.last()
	if !ok {
		t.Fatal("receiver got no delivery")
	}
	if ev := got.headers.Get("X-GitHub-Event"); ev != "issues" {
		t.Errorf("X-GitHub-Event = %q, want issues", ev)
	}
	if got.headers.Get("X-GitHub-Delivery") == "" {
		t.Error("missing X-GitHub-Delivery header")
	}
	sig := got.headers.Get("X-Hub-Signature-256")
	if !Verify(secret, sig, got.body) {
		t.Errorf("signature %q did not verify over the received body", sig)
	}

	var payload struct {
		Action string `json:"action"`
		Issue  struct {
			Title string `json:"title"`
		} `json:"issue"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Sender struct {
			Login string `json:"login"`
		} `json:"sender"`
	}
	if err := json.Unmarshal(got.body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload.Action != "opened" || payload.Issue.Title != "first" {
		t.Errorf("payload action/title = %q/%q", payload.Action, payload.Issue.Title)
	}
	if payload.Repository.FullName != "octocat/hello" || payload.Sender.Login != "octocat" {
		t.Errorf("payload repo/sender = %q/%q", payload.Repository.FullName, payload.Sender.Login)
	}
}

func TestInactiveHookIsNotDelivered(t *testing.T) {
	f := newDeliverFixture(t)
	inactive := false
	if _, err := f.hooks.CreateHook(f.ctx, f.ownerPK, "octocat", f.repoName, domain.HookInput{
		URL:    f.srv.URL,
		Active: &inactive,
		Events: []string{"issues"},
	}); err != nil {
		t.Fatalf("CreateHook: %v", err)
	}
	if _, err := f.issues.CreateIssue(f.ctx, f.ownerPK, "octocat", f.repoName, domain.IssueInput{Title: "quiet"}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	f.drain(t)
	if _, ok := f.rcv.last(); ok {
		t.Error("an inactive hook received a delivery")
	}
}

func TestUnsubscribedEventIsNotDelivered(t *testing.T) {
	f := newDeliverFixture(t)
	if _, err := f.hooks.CreateHook(f.ctx, f.ownerPK, "octocat", f.repoName, domain.HookInput{
		URL:    f.srv.URL,
		Events: []string{"push"}, // subscribed to push only, not issues
	}); err != nil {
		t.Fatalf("CreateHook: %v", err)
	}
	if _, err := f.issues.CreateIssue(f.ctx, f.ownerPK, "octocat", f.repoName, domain.IssueInput{Title: "ignored"}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	f.drain(t)
	if _, ok := f.rcv.last(); ok {
		t.Error("a hook subscribed to push received an issues delivery")
	}
}

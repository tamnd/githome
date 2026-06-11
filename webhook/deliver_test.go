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

// count returns how many deliveries arrived with the given X-GitHub-Event,
// letting tests that expect silence ignore the ping a hook gets on creation.
func (r *receiver) count(event string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for i := range r.deliveries {
		if r.deliveries[i].headers.Get("X-GitHub-Event") == event {
			n++
		}
	}
	return n
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
	enq      *worker.StoreEnqueuer
	runtime  *worker.Runtime
	rcv      *receiver
	srv      *httptest.Server
	renderer *Renderer
	gs       *git.Store
	ownerPK  int64
	repoPK   int64
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
	renderer.BindGit(gitStore)
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
		ctx: ctx, st: st, issues: issueSvc, pulls: pullSvc, hooks: hookSvc, enq: enq,
		runtime: rt, rcv: rcv, srv: srv, renderer: renderer, gs: gitStore,
		ownerPK: owner.PK, repoPK: repo.PK, repoName: "hello",
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

// TestPushEventSignedDelivery covers the renderer's push branch, which the issue
// path does not touch. A push has no row to load from: the moved refs ride on the
// deliver_event job. The test records a push event and enqueues that job directly,
// the same two steps the git transport performs after a receive-pack, then drains
// and asserts the receiver got a signed PushEvent whose ref and after-sha match.
func TestPushEventSignedDelivery(t *testing.T) {
	f := newDeliverFixture(t)
	secret := "hunter2"
	if _, err := f.hooks.CreateHook(f.ctx, f.ownerPK, "octocat", f.repoName, domain.HookInput{
		URL:    f.srv.URL,
		Secret: &secret,
		Events: []string{"push"},
	}); err != nil {
		t.Fatalf("CreateHook: %v", err)
	}

	const after = "3333333333333333333333333333333333333333"
	ev := &store.EventRow{
		Event:   domain.EventPush,
		ActorPK: f.ownerPK,
		RepoPK:  f.repoPK,
		Public:  true,
		Payload: "{}",
	}
	if err := f.st.InsertEvent(f.ctx, ev); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	payload, err := json.Marshal(domain.DeliverEventPayload{
		EventPK: ev.PK,
		Push: &domain.PushPayload{
			RepoPK:   f.repoPK,
			PusherPK: f.ownerPK,
			Protocol: "http",
			Updates: []domain.RefUpdate{{
				Ref:    "refs/heads/main",
				OldSHA: domain.ZeroSHA,
				NewSHA: after,
			}},
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if _, err := f.enq.Enqueue(f.ctx, domain.JobDeliverEvent, string(payload), ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	f.drain(t)

	got, ok := f.rcv.last()
	if !ok {
		t.Fatal("receiver got no delivery")
	}
	if got.headers.Get("X-GitHub-Event") != "push" {
		t.Errorf("X-GitHub-Event = %q, want push", got.headers.Get("X-GitHub-Event"))
	}
	if !Verify(secret, got.headers.Get("X-Hub-Signature-256"), got.body) {
		t.Error("push signature did not verify over the received body")
	}
	var body struct {
		Ref     string `json:"ref"`
		After   string `json:"after"`
		Created bool   `json:"created"`
	}
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Ref != "refs/heads/main" || body.After != after || !body.Created {
		t.Errorf("push body ref/after/created = %q/%q/%v", body.Ref, body.After, body.Created)
	}
}

// TestPingDelivery covers the ping path: creating a hook delivers a signed ping
// with the {zen, hook_id, hook} body, and PingHook sends another on demand.
func TestPingDelivery(t *testing.T) {
	f := newDeliverFixture(t)
	secret := "tell-no-one"
	hook, err := f.hooks.CreateHook(f.ctx, f.ownerPK, "octocat", f.repoName, domain.HookInput{
		URL:    f.srv.URL,
		Secret: &secret,
		Events: []string{"issues"},
	})
	if err != nil {
		t.Fatalf("CreateHook: %v", err)
	}
	f.drain(t)

	got, ok := f.rcv.last()
	if !ok {
		t.Fatal("creating a hook delivered no ping")
	}
	if ev := got.headers.Get("X-GitHub-Event"); ev != "ping" {
		t.Fatalf("X-GitHub-Event = %q, want ping", ev)
	}
	if !Verify(secret, got.headers.Get("X-Hub-Signature-256"), got.body) {
		t.Error("ping signature did not verify over the received body")
	}
	var body struct {
		Zen    string `json:"zen"`
		HookID int64  `json:"hook_id"`
		Hook   struct {
			Config struct {
				URL string `json:"url"`
			} `json:"config"`
			Events []string `json:"events"`
		} `json:"hook"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Sender struct {
			Login string `json:"login"`
		} `json:"sender"`
	}
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("decode ping body: %v", err)
	}
	if body.Zen == "" {
		t.Error("ping zen is empty")
	}
	if body.HookID != hook.ID {
		t.Errorf("ping hook_id = %d, want %d", body.HookID, hook.ID)
	}
	if body.Hook.Config.URL != f.srv.URL {
		t.Errorf("ping hook.config.url = %q, want %q", body.Hook.Config.URL, f.srv.URL)
	}
	if len(body.Hook.Events) != 1 || body.Hook.Events[0] != "issues" {
		t.Errorf("ping hook.events = %v, want [issues]", body.Hook.Events)
	}
	if body.Repository.FullName != "octocat/hello" || body.Sender.Login != "octocat" {
		t.Errorf("ping repo/sender = %q/%q", body.Repository.FullName, body.Sender.Login)
	}

	// The pings endpoint triggers another delivery on demand.
	if err := f.hooks.PingHook(f.ctx, f.ownerPK, "octocat", f.repoName, hook.ID); err != nil {
		t.Fatalf("PingHook: %v", err)
	}
	f.drain(t)
	if n := f.rcv.count("ping"); n != 2 {
		t.Errorf("ping deliveries = %d, want 2", n)
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
	if n := f.rcv.count("issues"); n != 0 {
		t.Errorf("an inactive hook received %d issues deliveries", n)
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
	if n := f.rcv.count("issues"); n != 0 {
		t.Errorf("a hook subscribed to push received %d issues deliveries", n)
	}
}

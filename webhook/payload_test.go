package webhook

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/store"
)

// pushBody is the slice of the push delivery body the walk tests assert on.
type pushBody struct {
	Ref     string  `json:"ref"`
	Before  string  `json:"before"`
	After   string  `json:"after"`
	Created bool    `json:"created"`
	Deleted bool    `json:"deleted"`
	Forced  bool    `json:"forced"`
	BaseRef *string `json:"base_ref"`
	Commits []struct {
		ID        string `json:"id"`
		TreeID    string `json:"tree_id"`
		Distinct  bool   `json:"distinct"`
		Message   string `json:"message"`
		Timestamp string `json:"timestamp"`
		URL       string `json:"url"`
		Author    struct {
			Name     string `json:"name"`
			Email    string `json:"email"`
			Username string `json:"username"`
		} `json:"author"`
		Committer struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"committer"`
		Added    []string `json:"added"`
		Removed  []string `json:"removed"`
		Modified []string `json:"modified"`
	} `json:"commits"`
	HeadCommit *struct {
		ID string `json:"id"`
	} `json:"head_commit"`
}

// pushRepo seeds the fixture's bare repository with a small history and returns
// the shas the tests address ranges by:
//
//	main:    a -- b -- c
//	feature: a -- d        (d adds d.txt, so feature is not an ancestor of c)
type pushRepo struct {
	a, b, c, d string
}

func seedPushRepo(t *testing.T, f *deliverFixture) pushRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	src := t.TempDir()
	whGitCmd(t, src, "init", "-q", "-b", "main")
	whWriteFile(t, filepath.Join(src, "a.txt"), "one\n")
	whGitCmd(t, src, "add", "-A")
	whGitCmd(t, src, "commit", "-q", "-m", "first")
	a := whGitCmd(t, src, "rev-parse", "HEAD")
	whWriteFile(t, filepath.Join(src, "b.txt"), "two\n")
	whWriteFile(t, filepath.Join(src, "a.txt"), "one more\n")
	whGitCmd(t, src, "add", "-A")
	whGitCmd(t, src, "commit", "-q", "-m", "add b, touch a")
	b := whGitCmd(t, src, "rev-parse", "HEAD")
	whGitCmd(t, src, "rm", "-q", "a.txt")
	whGitCmd(t, src, "commit", "-q", "-m", "drop a")
	c := whGitCmd(t, src, "rev-parse", "HEAD")
	whGitCmd(t, src, "checkout", "-q", "-b", "feature", a)
	whWriteFile(t, filepath.Join(src, "d.txt"), "four\n")
	whGitCmd(t, src, "add", "-A")
	whGitCmd(t, src, "commit", "-q", "-m", "add d")
	d := whGitCmd(t, src, "rev-parse", "HEAD")
	whGitCmd(t, src, "checkout", "-q", "main")

	bare := f.gs.Dir(f.repoPK)
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	whGitCmd(t, "", "clone", "-q", "--bare", src, bare)
	return pushRepo{a: a, b: b, c: c, d: d}
}

// renderPushBody renders one push update through the fixture's renderer and
// decodes the delivery body.
func renderPushBody(t *testing.T, f *deliverFixture, ref, before, after string) pushBody {
	t.Helper()
	ev := &store.EventRow{Event: domain.EventPush, ActorPK: f.ownerPK, RepoPK: f.repoPK, Public: true}
	push := &domain.PushPayload{
		RepoPK:   f.repoPK,
		PusherPK: f.ownerPK,
		Protocol: "http",
		Updates:  []domain.RefUpdate{{Ref: ref, OldSHA: before, NewSHA: after}},
	}
	rendered, err := f.renderer.Render(f.ctx, ev, push, nil, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var body pushBody
	if err := json.Unmarshal(rendered.Body, &body); err != nil {
		t.Fatalf("decode push body: %v", err)
	}
	return body
}

func TestPushPayloadWalksCommits(t *testing.T) {
	f := newDeliverFixture(t)
	r := seedPushRepo(t, f)

	body := renderPushBody(t, f, "refs/heads/main", r.a, r.c)
	if body.Forced || body.Created || body.Deleted {
		t.Errorf("flags forced/created/deleted = %v/%v/%v, want all false", body.Forced, body.Created, body.Deleted)
	}
	if len(body.Commits) != 2 {
		t.Fatalf("commits = %d, want 2", len(body.Commits))
	}
	// Oldest first, head_commit is the new tip.
	if body.Commits[0].ID != r.b || body.Commits[1].ID != r.c {
		t.Errorf("commit order = %s, %s; want %s, %s", body.Commits[0].ID, body.Commits[1].ID, r.b, r.c)
	}
	if body.HeadCommit == nil || body.HeadCommit.ID != r.c {
		t.Errorf("head_commit = %+v, want id %s", body.HeadCommit, r.c)
	}
	cb := body.Commits[0]
	if cb.TreeID == "" {
		t.Error("commit tree_id is empty")
	}
	if !cb.Distinct {
		t.Error("commit distinct = false, want true")
	}
	if cb.Message != "add b, touch a" {
		t.Errorf("commit message = %q", cb.Message)
	}
	if cb.Timestamp == "" {
		t.Error("commit timestamp is empty")
	}
	if !strings.Contains(cb.URL, "/octocat/hello/commit/"+r.b) {
		t.Errorf("commit url = %q", cb.URL)
	}
	if cb.Author.Name != "Octo Cat" || cb.Author.Email != "octo@example.com" {
		t.Errorf("commit author = %+v", cb.Author)
	}
	if cb.Committer.Name != "Octo Cat" {
		t.Errorf("commit committer = %+v", cb.Committer)
	}
	// b adds b.txt and modifies a.txt; c removes a.txt.
	if len(cb.Added) != 1 || cb.Added[0] != "b.txt" {
		t.Errorf("commit added = %v, want [b.txt]", cb.Added)
	}
	if len(cb.Modified) != 1 || cb.Modified[0] != "a.txt" {
		t.Errorf("commit modified = %v, want [a.txt]", cb.Modified)
	}
	if len(cb.Removed) != 0 {
		t.Errorf("commit removed = %v, want []", cb.Removed)
	}
	if rm := body.Commits[1].Removed; len(rm) != 1 || rm[0] != "a.txt" {
		t.Errorf("head commit removed = %v, want [a.txt]", rm)
	}
}

func TestPushPayloadFeedMirrorsCommits(t *testing.T) {
	f := newDeliverFixture(t)
	r := seedPushRepo(t, f)

	ev := &store.EventRow{Event: domain.EventPush, ActorPK: f.ownerPK, RepoPK: f.repoPK, Public: true}
	push := &domain.PushPayload{
		RepoPK: f.repoPK, PusherPK: f.ownerPK, Protocol: "http",
		Updates: []domain.RefUpdate{{Ref: "refs/heads/main", OldSHA: r.a, NewSHA: r.c}},
	}
	rendered, err := f.renderer.Render(f.ctx, ev, push, nil, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var feed struct {
		Size         int `json:"size"`
		DistinctSize int `json:"distinct_size"`
		Commits      []struct {
			SHA    string `json:"sha"`
			Author struct {
				Email string `json:"email"`
				Name  string `json:"name"`
			} `json:"author"`
			Message  string `json:"message"`
			Distinct bool   `json:"distinct"`
			URL      string `json:"url"`
		} `json:"commits"`
	}
	if err := json.Unmarshal(rendered.Payload, &feed); err != nil {
		t.Fatalf("decode feed payload: %v", err)
	}
	if feed.Size != 2 || feed.DistinctSize != 2 || len(feed.Commits) != 2 {
		t.Fatalf("feed size/distinct/commits = %d/%d/%d, want 2/2/2", feed.Size, feed.DistinctSize, len(feed.Commits))
	}
	if feed.Commits[1].SHA != r.c || feed.Commits[1].Message != "drop a" {
		t.Errorf("feed head commit = %+v", feed.Commits[1])
	}
	if feed.Commits[0].Author.Name != "Octo Cat" {
		t.Errorf("feed commit author = %+v", feed.Commits[0].Author)
	}
}

func TestPushPayloadForcedPush(t *testing.T) {
	f := newDeliverFixture(t)
	r := seedPushRepo(t, f)

	// Moving main from c to d is not a fast-forward: d does not contain c.
	body := renderPushBody(t, f, "refs/heads/main", r.c, r.d)
	if !body.Forced {
		t.Error("forced = false for a non-fast-forward move, want true")
	}
	if len(body.Commits) != 1 || body.Commits[0].ID != r.d {
		t.Errorf("commits = %+v, want just %s", body.Commits, r.d)
	}
}

func TestPushPayloadCreatedBranch(t *testing.T) {
	f := newDeliverFixture(t)
	r := seedPushRepo(t, f)

	// feature is new work cut from a: only its own commit is listed, and no
	// base_ref since its tip is not reachable from main.
	body := renderPushBody(t, f, "refs/heads/feature", domain.ZeroSHA, r.d)
	if !body.Created {
		t.Error("created = false for a new branch, want true")
	}
	if body.BaseRef != nil {
		t.Errorf("base_ref = %q, want null", *body.BaseRef)
	}
	if len(body.Commits) != 1 || body.Commits[0].ID != r.d {
		t.Errorf("commits = %+v, want just %s", body.Commits, r.d)
	}
	if body.HeadCommit == nil || body.HeadCommit.ID != r.d {
		t.Errorf("head_commit = %+v, want %s", body.HeadCommit, r.d)
	}

	// A branch cut at an existing tip carries no commits and names the branch
	// it was cut from as base_ref.
	body = renderPushBody(t, f, "refs/heads/topic", domain.ZeroSHA, r.c)
	if body.BaseRef == nil || *body.BaseRef != "refs/heads/main" {
		t.Errorf("base_ref = %v, want refs/heads/main", body.BaseRef)
	}
	if len(body.Commits) != 0 {
		t.Errorf("commits = %+v, want none", body.Commits)
	}
}

func TestPushPayloadDeletedBranch(t *testing.T) {
	f := newDeliverFixture(t)
	r := seedPushRepo(t, f)

	body := renderPushBody(t, f, "refs/heads/feature", r.d, domain.ZeroSHA)
	if !body.Deleted {
		t.Error("deleted = false for a removed branch, want true")
	}
	if len(body.Commits) != 0 || body.HeadCommit != nil {
		t.Errorf("deleted push carries commits %v head %v, want none", body.Commits, body.HeadCommit)
	}
}

// TestPullRequestSynchronizeDelivery covers the head-push path: a push to a
// branch an open pull request tracks as its head emits a pull_request delivery
// with action synchronize and the moved shas as top-level before/after.
func TestPullRequestSynchronizeDelivery(t *testing.T) {
	f := newDeliverFixture(t)
	r := seedPushRepo(t, f)

	if _, err := f.hooks.CreateHook(f.ctx, f.ownerPK, "octocat", f.repoName, domain.HookInput{
		URL:    f.srv.URL,
		Events: []string{"pull_request"},
	}); err != nil {
		t.Fatalf("CreateHook: %v", err)
	}
	pr, err := f.pulls.CreatePR(f.ctx, f.ownerPK, "octocat", f.repoName, domain.PRInput{
		Title: "feature work", Base: "main", Head: "feature",
	})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	f.drain(t)

	// The push moves feature from d to b, both objects already in the bare repo.
	if err := f.pulls.OnHeadPush(f.ctx, f.ownerPK, f.repoPK, "feature", r.b); err != nil {
		t.Fatalf("OnHeadPush: %v", err)
	}
	f.drain(t)

	got, ok := f.rcv.last()
	if !ok {
		t.Fatal("receiver got no delivery")
	}
	if ev := got.headers.Get("X-GitHub-Event"); ev != "pull_request" {
		t.Errorf("X-GitHub-Event = %q, want pull_request", ev)
	}
	var body struct {
		Action      string `json:"action"`
		Number      int64  `json:"number"`
		Before      string `json:"before"`
		After       string `json:"after"`
		PullRequest struct {
			Head struct {
				SHA string `json:"sha"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Action != "synchronize" {
		t.Errorf("action = %q, want synchronize", body.Action)
	}
	if body.Number != pr.Number {
		t.Errorf("number = %d, want %d", body.Number, pr.Number)
	}
	if body.Before != r.d || body.After != r.b {
		t.Errorf("before/after = %s/%s, want %s/%s", body.Before, body.After, r.d, r.b)
	}
	if body.PullRequest.Head.SHA != r.b {
		t.Errorf("pull_request.head.sha = %s, want %s", body.PullRequest.Head.SHA, r.b)
	}

	// A push that leaves the head where it is emits nothing.
	count := len(f.rcv.deliveries)
	if err := f.pulls.OnHeadPush(f.ctx, f.ownerPK, f.repoPK, "feature", r.b); err != nil {
		t.Fatalf("OnHeadPush again: %v", err)
	}
	f.drain(t)
	if got := len(f.rcv.deliveries); got != count {
		t.Errorf("deliveries after no-op push = %d, want %d", got, count)
	}
}

func whWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func whGitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=Octo Cat", "GIT_AUTHOR_EMAIL=octo@example.com",
		"GIT_COMMITTER_NAME=Octo Cat", "GIT_COMMITTER_EMAIL=octo@example.com",
		"GIT_AUTHOR_DATE=2026-01-02T03:04:05Z", "GIT_COMMITTER_DATE=2026-01-02T03:04:05Z",
	)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

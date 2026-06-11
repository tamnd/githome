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

// pullActionBody is the slice of a pull_request delivery the action tests
// decode.
type pullActionBody struct {
	Action      string `json:"action"`
	Number      int64  `json:"number"`
	PullRequest struct {
		Title string `json:"title"`
		State string `json:"state"`
		Draft bool   `json:"draft"`
	} `json:"pull_request"`
	Label *struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	} `json:"label"`
}

// lastPullAction drains the queue and decodes the latest delivery, asserting it
// went out as a pull_request event.
func lastPullAction(t *testing.T, f *deliverFixture) pullActionBody {
	t.Helper()
	f.drain(t)
	got, ok := f.rcv.last()
	if !ok {
		t.Fatal("receiver got no delivery")
	}
	if ev := got.headers.Get("X-GitHub-Event"); ev != "pull_request" {
		t.Fatalf("X-GitHub-Event = %q, want pull_request", ev)
	}
	var body pullActionBody
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

// TestPullRequestActionDeliveries walks a pull request through the lifecycle
// edits that share the issues machinery and asserts each goes out as a
// pull_request delivery with the GitHub action name: ready_for_review,
// edited, closed, reopened, and labeled with the label object attached.
func TestPullRequestActionDeliveries(t *testing.T) {
	f := newDeliverFixture(t)
	seedPushRepo(t, f)

	if _, err := f.hooks.CreateHook(f.ctx, f.ownerPK, "octocat", f.repoName, domain.HookInput{
		URL:    f.srv.URL,
		Events: []string{"pull_request"},
	}); err != nil {
		t.Fatalf("CreateHook: %v", err)
	}
	pr, err := f.pulls.CreatePR(f.ctx, f.ownerPK, "octocat", f.repoName, domain.PRInput{
		Title: "draft work", Base: "main", Head: "feature", Draft: true,
	})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}

	if _, err := f.pulls.SetDraft(f.ctx, f.ownerPK, "octocat", f.repoName, pr.Number, false); err != nil {
		t.Fatalf("SetDraft: %v", err)
	}
	if body := lastPullAction(t, f); body.Action != "ready_for_review" || body.PullRequest.Draft {
		t.Errorf("ready_for_review body = %+v", body)
	}

	title := "renamed work"
	if _, err := f.issues.EditIssue(f.ctx, f.ownerPK, "octocat", f.repoName, pr.Number, domain.IssuePatch{Title: &title}); err != nil {
		t.Fatalf("EditIssue title: %v", err)
	}
	if body := lastPullAction(t, f); body.Action != "edited" || body.PullRequest.Title != title {
		t.Errorf("edited body = %+v", body)
	}

	closed := "closed"
	if _, err := f.issues.EditIssue(f.ctx, f.ownerPK, "octocat", f.repoName, pr.Number, domain.IssuePatch{State: &closed}); err != nil {
		t.Fatalf("EditIssue close: %v", err)
	}
	if body := lastPullAction(t, f); body.Action != "closed" || body.PullRequest.State != "closed" {
		t.Errorf("closed body = %+v", body)
	}

	open := "open"
	if _, err := f.issues.EditIssue(f.ctx, f.ownerPK, "octocat", f.repoName, pr.Number, domain.IssuePatch{State: &open}); err != nil {
		t.Fatalf("EditIssue reopen: %v", err)
	}
	if body := lastPullAction(t, f); body.Action != "reopened" || body.PullRequest.State != "open" {
		t.Errorf("reopened body = %+v", body)
	}

	if _, err := f.issues.CreateLabel(f.ctx, f.ownerPK, "octocat", f.repoName, domain.LabelInput{Name: "bug", Color: "d73a4a"}); err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	if _, err := f.issues.AddLabels(f.ctx, f.ownerPK, "octocat", f.repoName, pr.Number, []string{"bug"}); err != nil {
		t.Fatalf("AddLabels: %v", err)
	}
	body := lastPullAction(t, f)
	if body.Action != "labeled" {
		t.Errorf("labeled action = %q", body.Action)
	}
	if body.Label == nil || body.Label.Name != "bug" || body.Label.Color != "d73a4a" {
		t.Errorf("labeled label = %+v, want bug/d73a4a", body.Label)
	}
}

// TestIssueLabeledDelivery checks the plain-issue side of the labeled action:
// the delivery stays an issues event and embeds the label object.
func TestIssueLabeledDelivery(t *testing.T) {
	f := newDeliverFixture(t)
	if _, err := f.hooks.CreateHook(f.ctx, f.ownerPK, "octocat", f.repoName, domain.HookInput{
		URL:    f.srv.URL,
		Events: []string{"issues"},
	}); err != nil {
		t.Fatalf("CreateHook: %v", err)
	}
	iss, err := f.issues.CreateIssue(f.ctx, f.ownerPK, "octocat", f.repoName, domain.IssueInput{Title: "tag me"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := f.issues.CreateLabel(f.ctx, f.ownerPK, "octocat", f.repoName, domain.LabelInput{Name: "question", Color: "cc317c"}); err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	if _, err := f.issues.AddLabels(f.ctx, f.ownerPK, "octocat", f.repoName, iss.Number, []string{"question"}); err != nil {
		t.Fatalf("AddLabels: %v", err)
	}
	f.drain(t)

	got, ok := f.rcv.last()
	if !ok {
		t.Fatal("receiver got no delivery")
	}
	if ev := got.headers.Get("X-GitHub-Event"); ev != "issues" {
		t.Fatalf("X-GitHub-Event = %q, want issues", ev)
	}
	var body struct {
		Action string `json:"action"`
		Label  *struct {
			Name string `json:"name"`
		} `json:"label"`
	}
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Action != "labeled" || body.Label == nil || body.Label.Name != "question" {
		t.Errorf("labeled issue body = %+v", body)
	}
}

// TestIssueCommentDelivery checks the issue_comment body carries the comment
// object alongside the issue it landed on, not just the issue.
func TestIssueCommentDelivery(t *testing.T) {
	f := newDeliverFixture(t)
	if _, err := f.hooks.CreateHook(f.ctx, f.ownerPK, "octocat", f.repoName, domain.HookInput{
		URL:    f.srv.URL,
		Events: []string{"issue_comment"},
	}); err != nil {
		t.Fatalf("CreateHook: %v", err)
	}
	iss, err := f.issues.CreateIssue(f.ctx, f.ownerPK, "octocat", f.repoName, domain.IssueInput{Title: "talk here"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	cm, err := f.issues.CreateComment(f.ctx, f.ownerPK, "octocat", f.repoName, iss.Number, "first comment")
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	f.drain(t)

	got, ok := f.rcv.lastByEvent("issue_comment")
	if !ok {
		t.Fatal("receiver got no issue_comment delivery")
	}
	var body struct {
		Action string `json:"action"`
		Issue  struct {
			Number int64 `json:"number"`
		} `json:"issue"`
		Comment struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
			User struct {
				Login string `json:"login"`
			} `json:"user"`
		} `json:"comment"`
	}
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Action != "created" {
		t.Errorf("action = %q, want created", body.Action)
	}
	if body.Issue.Number != iss.Number {
		t.Errorf("issue.number = %d, want %d", body.Issue.Number, iss.Number)
	}
	if body.Comment.ID != cm.ID || body.Comment.Body != "first comment" {
		t.Errorf("comment = %+v, want id %d body %q", body.Comment, cm.ID, "first comment")
	}
	if body.Comment.User.Login != "octocat" {
		t.Errorf("comment.user.login = %q, want octocat", body.Comment.User.Login)
	}
}

// TestPullRequestReviewDeliveries submits a comment review with one inline
// comment and checks both bodies: pull_request_review carries the review
// object, pull_request_review_comment carries the inline comment.
func TestPullRequestReviewDeliveries(t *testing.T) {
	f := newDeliverFixture(t)
	seedPushRepo(t, f)

	if _, err := f.hooks.CreateHook(f.ctx, f.ownerPK, "octocat", f.repoName, domain.HookInput{
		URL:    f.srv.URL,
		Events: []string{"pull_request_review", "pull_request_review_comment"},
	}); err != nil {
		t.Fatalf("CreateHook: %v", err)
	}
	pr, err := f.pulls.CreatePR(f.ctx, f.ownerPK, "octocat", f.repoName, domain.PRInput{
		Title: "feature work", Base: "main", Head: "feature",
	})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	line := int64(1)
	rev, err := f.reviews.CreateReview(f.ctx, f.ownerPK, "octocat", f.repoName, pr.Number, domain.ReviewInput{
		Event: "COMMENT", Body: "looks fine",
		Comments: []domain.ReviewCommentInput{{Path: "d.txt", Body: "inline note", Side: "RIGHT", Line: &line}},
	})
	if err != nil {
		t.Fatalf("CreateReview: %v", err)
	}
	f.drain(t)

	got, ok := f.rcv.lastByEvent("pull_request_review")
	if !ok {
		t.Fatal("receiver got no pull_request_review delivery")
	}
	var reviewBody struct {
		Action string `json:"action"`
		Review struct {
			ID    int64  `json:"id"`
			State string `json:"state"`
			Body  string `json:"body"`
			User  struct {
				Login string `json:"login"`
			} `json:"user"`
		} `json:"review"`
		PullRequest struct {
			Number int64 `json:"number"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(got.body, &reviewBody); err != nil {
		t.Fatalf("decode review body: %v", err)
	}
	if reviewBody.Action != "submitted" {
		t.Errorf("action = %q, want submitted", reviewBody.Action)
	}
	if reviewBody.Review.ID != rev.ID || reviewBody.Review.State != "COMMENTED" || reviewBody.Review.Body != "looks fine" {
		t.Errorf("review = %+v, want id %d state COMMENTED body %q", reviewBody.Review, rev.ID, "looks fine")
	}
	if reviewBody.Review.User.Login != "octocat" {
		t.Errorf("review.user.login = %q, want octocat", reviewBody.Review.User.Login)
	}
	if reviewBody.PullRequest.Number != pr.Number {
		t.Errorf("pull_request.number = %d, want %d", reviewBody.PullRequest.Number, pr.Number)
	}

	got, ok = f.rcv.lastByEvent("pull_request_review_comment")
	if !ok {
		t.Fatal("receiver got no pull_request_review_comment delivery")
	}
	var commentBody struct {
		Action  string `json:"action"`
		Comment struct {
			Body                string `json:"body"`
			Path                string `json:"path"`
			Side                string `json:"side"`
			Line                *int64 `json:"line"`
			PullRequestReviewID int64  `json:"pull_request_review_id"`
		} `json:"comment"`
		PullRequest struct {
			Number int64 `json:"number"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(got.body, &commentBody); err != nil {
		t.Fatalf("decode comment body: %v", err)
	}
	if commentBody.Action != "created" {
		t.Errorf("action = %q, want created", commentBody.Action)
	}
	c := commentBody.Comment
	if c.Body != "inline note" || c.Path != "d.txt" || c.Side != "RIGHT" || c.Line == nil || *c.Line != 1 {
		t.Errorf("comment = %+v, want inline note on d.txt RIGHT line 1", c)
	}
	if c.PullRequestReviewID != rev.ID {
		t.Errorf("comment.pull_request_review_id = %d, want %d", c.PullRequestReviewID, rev.ID)
	}
	if commentBody.PullRequest.Number != pr.Number {
		t.Errorf("pull_request.number = %d, want %d", commentBody.PullRequest.Number, pr.Number)
	}
}

// TestReleasePublishedDelivery covers the two ways a release goes live:
// created live and a draft flipped live both deliver action published with the
// release object, and the draft itself stays silent.
func TestReleasePublishedDelivery(t *testing.T) {
	f := newDeliverFixture(t)
	if _, err := f.hooks.CreateHook(f.ctx, f.ownerPK, "octocat", f.repoName, domain.HookInput{
		URL:    f.srv.URL,
		Events: []string{"release"},
	}); err != nil {
		t.Fatalf("CreateHook: %v", err)
	}

	draft, err := f.releases.CreateRelease(f.ctx, f.ownerPK, "octocat", f.repoName, domain.ReleaseInput{
		TagName: "v0.1.0", Draft: true,
	})
	if err != nil {
		t.Fatalf("CreateRelease draft: %v", err)
	}
	f.drain(t)
	if n := f.rcv.count("release"); n != 0 {
		t.Fatalf("draft release produced %d deliveries, want 0", n)
	}

	if _, err := f.releases.UpdateRelease(f.ctx, f.ownerPK, "octocat", f.repoName, draft.ID, domain.ReleaseInput{
		Draft: false,
	}); err != nil {
		t.Fatalf("UpdateRelease publish: %v", err)
	}
	f.drain(t)

	got, ok := f.rcv.lastByEvent("release")
	if !ok {
		t.Fatal("receiver got no release delivery")
	}
	var body struct {
		Action  string `json:"action"`
		Release struct {
			ID          int64  `json:"id"`
			TagName     string `json:"tag_name"`
			Draft       bool   `json:"draft"`
			PublishedAt string `json:"published_at"`
			Author      struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"release"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Action != "published" {
		t.Errorf("action = %q, want published", body.Action)
	}
	rel := body.Release
	if rel.ID != draft.ID || rel.TagName != "v0.1.0" || rel.Draft {
		t.Errorf("release = %+v, want id %d tag v0.1.0 draft false", rel, draft.ID)
	}
	if rel.PublishedAt == "" {
		t.Error("release.published_at is empty")
	}
	if rel.Author.Login != "octocat" {
		t.Errorf("release.author.login = %q, want octocat", rel.Author.Login)
	}
	if body.Repository.FullName != "octocat/hello" {
		t.Errorf("repository.full_name = %q", body.Repository.FullName)
	}

	// A release created live delivers immediately.
	if _, err := f.releases.CreateRelease(f.ctx, f.ownerPK, "octocat", f.repoName, domain.ReleaseInput{
		TagName: "v0.2.0",
	}); err != nil {
		t.Fatalf("CreateRelease live: %v", err)
	}
	f.drain(t)
	if n := f.rcv.count("release"); n != 2 {
		t.Errorf("release deliveries = %d, want 2", n)
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

package domain

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
)

// issueFixture spins up a real sqlite store, migrates it, seeds an owner and a
// repository, and returns the wired issue service. Driving the service over the
// real store exercises the SQL and the transaction paths the way production
// does, and lets the worker-enqueue assertion read the jobs table back.
type issueFixture struct {
	svc     *IssueService
	st      *store.Store
	repo    *store.RepoRow
	ownerPK int64
	ctx     context.Context
}

func newIssueFixture(t *testing.T) *issueFixture {
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
	repos := NewRepoService(st, git.NewStore(t.TempDir()))
	return &issueFixture{svc: NewIssueService(st, repos), st: st, repo: repo, ownerPK: owner.PK, ctx: ctx}
}

func TestCreateIssueEnqueuesEvent(t *testing.T) {
	f := newIssueFixture(t)
	body := "the body"
	iss, err := f.svc.CreateIssue(f.ctx, f.ownerPK, "octocat", "hello", IssueInput{Title: "first", Body: &body})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if iss.Number != 1 || iss.State != "open" || iss.User.Login != "octocat" {
		t.Fatalf("issue = %+v", iss)
	}

	// The create records an `issues` webhook job in the durable queue, delivered
	// when the webhook milestone lands.
	jobs, err := f.st.ListJobs(f.ctx)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	var found bool
	for _, j := range jobs {
		if j.Kind == "issues" {
			found = true
			if !contains(j.Payload, `"action":"opened"`) {
				t.Errorf("issues payload missing opened action: %s", j.Payload)
			}
		}
	}
	if !found {
		t.Fatalf("create did not enqueue an issues job: %+v", jobs)
	}
}

func TestCreateIssueRequiresWrite(t *testing.T) {
	f := newIssueFixture(t)
	// A non-owner who can see the public repo is forbidden from opening an issue.
	if _, err := f.svc.CreateIssue(f.ctx, 999, "octocat", "hello", IssueInput{Title: "x"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-owner create err = %v, want ErrForbidden", err)
	}
	// An empty title is a validation error.
	if _, err := f.svc.CreateIssue(f.ctx, f.ownerPK, "octocat", "hello", IssueInput{Title: "  "}); !errors.Is(err, ErrValidation) {
		t.Errorf("empty title err = %v, want ErrValidation", err)
	}
}

func TestGetAndCloseIssue(t *testing.T) {
	f := newIssueFixture(t)
	created, err := f.svc.CreateIssue(f.ctx, f.ownerPK, "octocat", "hello", IssueInput{Title: "closeme"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	got, err := f.svc.GetIssue(f.ctx, f.ownerPK, "octocat", "hello", created.Number)
	if err != nil || got.Title != "closeme" {
		t.Fatalf("GetIssue = %+v (%v)", got, err)
	}

	closedState := "closed"
	closed, err := f.svc.EditIssue(f.ctx, f.ownerPK, "octocat", "hello", created.Number, IssuePatch{State: &closedState})
	if err != nil {
		t.Fatalf("EditIssue close: %v", err)
	}
	if closed.State != "closed" || closed.ClosedAt == nil || closed.ClosedBy == nil {
		t.Fatalf("closed issue = %+v", closed)
	}
	if closed.StateReason == nil || *closed.StateReason != "completed" {
		t.Errorf("state_reason = %v, want completed", closed.StateReason)
	}

	// Missing issue is ErrIssueNotFound, not a repo-level error.
	if _, err := f.svc.GetIssue(f.ctx, f.ownerPK, "octocat", "hello", 404); !errors.Is(err, ErrIssueNotFound) {
		t.Errorf("missing issue err = %v, want ErrIssueNotFound", err)
	}
}

func TestListIssuesStateFilter(t *testing.T) {
	f := newIssueFixture(t)
	a, _ := f.svc.CreateIssue(f.ctx, f.ownerPK, "octocat", "hello", IssueInput{Title: "open a"})
	_, _ = f.svc.CreateIssue(f.ctx, f.ownerPK, "octocat", "hello", IssueInput{Title: "open b"})
	closedState := "closed"
	if _, err := f.svc.EditIssue(f.ctx, f.ownerPK, "octocat", "hello", a.Number, IssuePatch{State: &closedState}); err != nil {
		t.Fatalf("close: %v", err)
	}

	open, total, err := f.svc.ListIssues(f.ctx, f.ownerPK, "octocat", "hello", IssueQuery{State: "open"})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if total != 1 || len(open) != 1 || open[0].Title != "open b" {
		t.Fatalf("open issues = %+v total=%d", open, total)
	}
	all, total, _ := f.svc.ListIssues(f.ctx, f.ownerPK, "octocat", "hello", IssueQuery{State: "all"})
	if total != 2 || len(all) != 2 {
		t.Fatalf("all issues = %d total=%d, want 2", len(all), total)
	}
}

func TestCommentOnIssue(t *testing.T) {
	f := newIssueFixture(t)
	iss, _ := f.svc.CreateIssue(f.ctx, f.ownerPK, "octocat", "hello", IssueInput{Title: "discuss"})

	c, err := f.svc.CreateComment(f.ctx, f.ownerPK, "octocat", "hello", iss.Number, "first reply")
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if c.Body != "first reply" || c.User.Login != "octocat" {
		t.Fatalf("comment = %+v", c)
	}
	list, err := f.svc.ListComments(f.ctx, f.ownerPK, "octocat", "hello", iss.Number, 1, 30)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListComments = %+v (%v)", list, err)
	}
	// The issue's cached comment count advanced.
	reread, _ := f.svc.GetIssue(f.ctx, f.ownerPK, "octocat", "hello", iss.Number)
	if reread.CommentsCount != 1 {
		t.Errorf("comments_count = %d, want 1", reread.CommentsCount)
	}
	// An anonymous actor cannot comment.
	if _, err := f.svc.CreateComment(f.ctx, 0, "octocat", "hello", iss.Number, "hi"); !errors.Is(err, ErrForbidden) {
		t.Errorf("anon comment err = %v, want ErrForbidden", err)
	}
}

func TestLabelsAndReactions(t *testing.T) {
	f := newIssueFixture(t)
	// Create a label, then open an issue carrying it.
	if _, err := f.svc.CreateLabel(f.ctx, f.ownerPK, "octocat", "hello", LabelInput{Name: "bug", Color: "#D73A4A"}); err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	iss, err := f.svc.CreateIssue(f.ctx, f.ownerPK, "octocat", "hello", IssueInput{Title: "labeled", Labels: []string{"bug"}})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if len(iss.Labels) != 1 || iss.Labels[0].Name != "bug" || iss.Labels[0].Color != "d73a4a" {
		t.Fatalf("issue labels = %+v", iss.Labels)
	}
	// A duplicate label name is a conflict.
	if _, err := f.svc.CreateLabel(f.ctx, f.ownerPK, "octocat", "hello", LabelInput{Name: "BUG"}); !errors.Is(err, ErrLabelExists) {
		t.Errorf("dup label err = %v, want ErrLabelExists", err)
	}

	// React to the issue, idempotently.
	r, err := f.svc.CreateIssueReaction(f.ctx, f.ownerPK, "octocat", "hello", iss.Number, "+1")
	if err != nil {
		t.Fatalf("CreateIssueReaction: %v", err)
	}
	if _, err := f.svc.CreateIssueReaction(f.ctx, f.ownerPK, "octocat", "hello", iss.Number, "+1"); err != nil {
		t.Fatalf("idempotent reaction: %v", err)
	}
	reread, _ := f.svc.GetIssue(f.ctx, f.ownerPK, "octocat", "hello", iss.Number)
	if reread.Reactions.TotalCount != 1 || reread.Reactions.Counts["+1"] != 1 {
		t.Fatalf("reaction rollup = %+v", reread.Reactions)
	}
	// Bad reaction content is rejected.
	if _, err := f.svc.CreateIssueReaction(f.ctx, f.ownerPK, "octocat", "hello", iss.Number, "thumbsup"); !errors.Is(err, ErrValidation) {
		t.Errorf("bad content err = %v, want ErrValidation", err)
	}
	if err := f.svc.DeleteIssueReaction(f.ctx, f.ownerPK, "octocat", "hello", iss.Number, r.ID); err != nil {
		t.Fatalf("DeleteIssueReaction: %v", err)
	}
}

func TestMilestoneFlow(t *testing.T) {
	f := newIssueFixture(t)
	title := "v1.0"
	m, err := f.svc.CreateMilestone(f.ctx, f.ownerPK, "octocat", "hello", MilestoneInput{Title: &title})
	if err != nil {
		t.Fatalf("CreateMilestone: %v", err)
	}
	if m.Number != 1 || m.State != "open" {
		t.Fatalf("milestone = %+v", m)
	}
	// Open an issue on the milestone and confirm the counts.
	if _, err := f.svc.CreateIssue(f.ctx, f.ownerPK, "octocat", "hello", IssueInput{Title: "scoped", MilestoneNumber: &m.Number}); err != nil {
		t.Fatalf("CreateIssue on milestone: %v", err)
	}
	got, err := f.svc.GetMilestone(f.ctx, f.ownerPK, "octocat", "hello", m.Number)
	if err != nil || got.OpenIssues != 1 {
		t.Fatalf("milestone counts = %+v (%v)", got, err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

package domain

import (
	"testing"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
)

func TestBatcherUsers(t *testing.T) {
	f := newIssueFixture(t)
	ctx := f.ctx

	// Create a second user.
	other := &store.UserRow{Login: "bob", Type: "User"}
	if err := f.st.InsertUser(ctx, other); err != nil {
		t.Fatal(err)
	}

	owner, err := f.st.UserByLogin(ctx, "octocat")
	if err != nil {
		t.Fatal(err)
	}

	b := NewBatcher(f.st)
	users, err := b.Users(ctx, []int64{owner.PK, other.PK, 999999})
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	if users[owner.PK].Login != "octocat" {
		t.Errorf("owner login = %q, want octocat", users[owner.PK].Login)
	}
	if users[other.PK].Login != "bob" {
		t.Errorf("other login = %q, want bob", users[other.PK].Login)
	}
	if _, ok := users[999999]; ok {
		t.Error("missing PK should not appear in result")
	}
}

func TestBatcherLabelsByIssues(t *testing.T) {
	f := newIssueFixture(t)
	ctx := f.ctx

	// Create a label on the repo.
	repos := NewRepoService(f.st, git.NewStore(t.TempDir()))
	issueSvc := NewIssueService(f.st, repos)

	_, err := issueSvc.CreateLabel(ctx, f.ownerPK, "octocat", "hello", LabelInput{Name: "bug", Color: "ff0000"})
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}

	// Create an issue with the label attached.
	iss, err := issueSvc.CreateIssue(ctx, f.ownerPK, "octocat", "hello", IssueInput{
		Title:  "test",
		Labels: []string{"bug"},
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	b := NewBatcher(f.st)
	lmap, err := b.LabelsByIssues(ctx, []int64{iss.PK})
	if err != nil {
		t.Fatal(err)
	}
	labels := lmap[iss.PK]
	if len(labels) != 1 {
		t.Fatalf("expected 1 label, got %d", len(labels))
	}
	if labels[0].Name != "bug" {
		t.Errorf("label name = %q, want bug", labels[0].Name)
	}
}

func TestBatcherAssigneesByIssues(t *testing.T) {
	f := newIssueFixture(t)
	ctx := f.ctx

	repos := NewRepoService(f.st, git.NewStore(t.TempDir()))
	issueSvc := NewIssueService(f.st, repos)

	// Create an issue with the owner as assignee.
	iss, err := issueSvc.CreateIssue(ctx, f.ownerPK, "octocat", "hello", IssueInput{
		Title:          "assigned",
		AssigneeLogins: []string{"octocat"},
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	b := NewBatcher(f.st)
	amap, err := b.AssigneesByIssues(ctx, []int64{iss.PK})
	if err != nil {
		t.Fatal(err)
	}
	assignees := amap[iss.PK]
	if len(assignees) != 1 {
		t.Fatalf("expected 1 assignee, got %d", len(assignees))
	}
	if assignees[0].Login != "octocat" {
		t.Errorf("assignee login = %q, want octocat", assignees[0].Login)
	}
}

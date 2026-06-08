package domain

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
)

// reviewFixture stands up a public repository with a main branch and a feature
// branch one commit ahead (b.txt added), an open pull request from feature into
// main, the review and checks services over them, and a second user who reviews.
// The author is the owner; the reviewer holds read access only, enough to review
// but not to dismiss or resolve.
type reviewFixture struct {
	reviews  *ReviewService
	checks   *ChecksService
	prs      *PRService
	st       *store.Store
	gs       *git.Store
	repo     *store.RepoRow
	pr       *PullRequest
	ownerPK  int64
	reviewPK int64 // the reviewer user, not a review row
	ctx      context.Context
}

func newReviewFixture(t *testing.T) *reviewFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
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
		t.Fatalf("InsertUser owner: %v", err)
	}
	reviewer := &store.UserRow{Login: "hubot", Type: "User"}
	if err := st.InsertUser(ctx, reviewer); err != nil {
		t.Fatalf("InsertUser reviewer: %v", err)
	}
	repo := &store.RepoRow{OwnerPK: owner.PK, Name: "hello", DefaultBranch: "main"}
	if err := st.InsertRepo(ctx, repo); err != nil {
		t.Fatalf("InsertRepo: %v", err)
	}
	gs := git.NewStore(t.TempDir())
	prBareRepo(t, gs, repo.PK)
	repos := NewRepoService(st, gs)
	issues := NewIssueService(st, repos)
	prs := NewPRService(st, repos, issues, gs)
	pr, err := prs.CreatePR(ctx, owner.PK, "octocat", "hello", PRInput{
		Title: "add b", Base: "main", Head: "feature",
	})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	return &reviewFixture{
		reviews:  NewReviewService(st, repos, prs, issues, gs),
		checks:   NewChecksService(st, repos, issues, gs),
		prs:      prs,
		st:       st,
		gs:       gs,
		repo:     repo,
		pr:       pr,
		ownerPK:  owner.PK,
		reviewPK: reviewer.PK,
		ctx:      ctx,
	}
}

// drainRecompute runs the pending recompute_review_decision jobs by hand, the
// work the worker does, so a test can assert the cached state the recompute
// writes.
func (f *reviewFixture) drainRecompute(t *testing.T) {
	t.Helper()
	jobs, err := f.st.ListJobs(f.ctx)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	for _, j := range jobs {
		if j.Kind != JobRecomputeReviewDecision {
			continue
		}
		iss, err := f.st.GetIssueByNumber(f.ctx, f.repo.PK, f.pr.Number)
		if err != nil {
			t.Fatalf("GetIssueByNumber: %v", err)
		}
		if err := f.reviews.RecomputeReviewDecision(f.ctx, iss.PK); err != nil {
			t.Fatalf("RecomputeReviewDecision: %v", err)
		}
	}
}

func TestReviewApprovalSetsDecision(t *testing.T) {
	f := newReviewFixture(t)
	// The reviewer, not the author, approves; one approval and no change request
	// makes the decision APPROVED.
	if _, err := f.reviews.CreateReview(f.ctx, f.reviewPK, "octocat", "hello", f.pr.Number, ReviewInput{
		Event: EventApprove, Body: "looks good",
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	dec, err := f.reviews.ReviewDecision(f.ctx, f.ownerPK, "octocat", "hello", f.pr.Number)
	if err != nil {
		t.Fatalf("ReviewDecision: %v", err)
	}
	if dec == nil || *dec != ReviewApproved {
		t.Fatalf("decision = %v, want APPROVED", dec)
	}

	// The recompute caches the same decision for the list and webhook paths.
	f.drainRecompute(t)
	state, err := f.st.GetPullCheckState(f.ctx, f.pr.PK)
	if err != nil {
		t.Fatalf("GetPullCheckState: %v", err)
	}
	if state.ReviewDecision == nil || *state.ReviewDecision != ReviewApproved {
		t.Fatalf("cached decision = %v, want APPROVED", state.ReviewDecision)
	}
}

func TestReviewAuthorCannotApproveOwnPull(t *testing.T) {
	f := newReviewFixture(t)
	if _, err := f.reviews.CreateReview(f.ctx, f.ownerPK, "octocat", "hello", f.pr.Number, ReviewInput{
		Event: EventApprove,
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("self-approve err = %v, want ErrValidation", err)
	}
}

func TestChangesRequestedBlocksAndNeedsBody(t *testing.T) {
	f := newReviewFixture(t)
	// A change request with no body is a validation error.
	if _, err := f.reviews.CreateReview(f.ctx, f.reviewPK, "octocat", "hello", f.pr.Number, ReviewInput{
		Event: EventRequestChanges,
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("bodyless change request err = %v, want ErrValidation", err)
	}
	if _, err := f.reviews.CreateReview(f.ctx, f.reviewPK, "octocat", "hello", f.pr.Number, ReviewInput{
		Event: EventRequestChanges, Body: "please fix",
	}); err != nil {
		t.Fatalf("change request: %v", err)
	}
	dec, err := f.reviews.ReviewDecision(f.ctx, f.ownerPK, "octocat", "hello", f.pr.Number)
	if err != nil {
		t.Fatalf("ReviewDecision: %v", err)
	}
	if dec == nil || *dec != ReviewChangesRequested {
		t.Fatalf("decision = %v, want CHANGES_REQUESTED", dec)
	}
}

func TestPendingReviewIsPrivateUntilSubmitted(t *testing.T) {
	f := newReviewFixture(t)
	draft, err := f.reviews.CreateReview(f.ctx, f.reviewPK, "octocat", "hello", f.pr.Number, ReviewInput{})
	if err != nil {
		t.Fatalf("open draft: %v", err)
	}
	if draft.State != ReviewPending {
		t.Fatalf("draft state = %q, want PENDING", draft.State)
	}
	// A pending draft is invisible to another viewer.
	if _, err := f.reviews.GetReview(f.ctx, f.ownerPK, "octocat", "hello", f.pr.Number, draft.ID); !errors.Is(err, ErrReviewNotFound) {
		t.Fatalf("other viewer sees draft, err = %v, want ErrReviewNotFound", err)
	}
	// A second draft by the same user is refused.
	if _, err := f.reviews.CreateReview(f.ctx, f.reviewPK, "octocat", "hello", f.pr.Number, ReviewInput{}); !errors.Is(err, ErrPendingReviewExists) {
		t.Fatalf("second draft err = %v, want ErrPendingReviewExists", err)
	}
	// Submitting it under an event publishes it and sets the decision.
	if _, err := f.reviews.SubmitReview(f.ctx, f.reviewPK, "octocat", "hello", f.pr.Number, draft.ID, EventApprove, ""); err != nil {
		t.Fatalf("submit draft: %v", err)
	}
	got, err := f.reviews.GetReview(f.ctx, f.ownerPK, "octocat", "hello", f.pr.Number, draft.ID)
	if err != nil {
		t.Fatalf("GetReview after submit: %v", err)
	}
	if got.State != ReviewApproved {
		t.Fatalf("submitted state = %q, want APPROVED", got.State)
	}
}

func TestCommentAnchorResolvesAndRejectsOffDiff(t *testing.T) {
	f := newReviewFixture(t)
	// b.txt is the added file; its first line is on the RIGHT side of the diff.
	one := int64(1)
	c, err := f.reviews.CreateComment(f.ctx, f.reviewPK, "octocat", "hello", f.pr.Number, ReviewCommentInput{
		Path: "b.txt", Body: "nit", Line: &one,
	})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if c.Position == nil || *c.Position != 1 {
		t.Fatalf("resolved position = %v, want 1", c.Position)
	}

	// A comment on a path outside the diff is refused.
	if _, err := f.reviews.CreateComment(f.ctx, f.reviewPK, "octocat", "hello", f.pr.Number, ReviewCommentInput{
		Path: "ghost.txt", Body: "no", Line: &one,
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("off-diff comment err = %v, want ErrValidation", err)
	}

	// A reply chains to the root comment and inherits its anchor.
	reply, err := f.reviews.ReplyComment(f.ctx, f.ownerPK, "octocat", "hello", f.pr.Number, c.ID, "agreed")
	if err != nil {
		t.Fatalf("ReplyComment: %v", err)
	}
	if reply.InReplyTo == nil || *reply.InReplyTo != c.PK {
		t.Fatalf("reply in_reply_to = %v, want root pk %d", reply.InReplyTo, c.PK)
	}
	if reply.Path != "b.txt" {
		t.Errorf("reply path = %q, want b.txt", reply.Path)
	}
}

func TestReviewThreadResolveByOwner(t *testing.T) {
	f := newReviewFixture(t)
	one := int64(1)
	c, err := f.reviews.CreateComment(f.ctx, f.reviewPK, "octocat", "hello", f.pr.Number, ReviewCommentInput{
		Path: "b.txt", Body: "nit", Line: &one,
	})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	// A read-only reviewer cannot resolve the thread.
	if _, err := f.reviews.ResolveThread(f.ctx, f.reviewPK, "octocat", "hello", f.pr.Number, c.ID, true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reviewer resolve err = %v, want ErrForbidden", err)
	}
	th, err := f.reviews.ResolveThread(f.ctx, f.ownerPK, "octocat", "hello", f.pr.Number, c.ID, true)
	if err != nil {
		t.Fatalf("ResolveThread: %v", err)
	}
	if !th.IsResolved {
		t.Fatalf("thread not resolved: %+v", th)
	}
}

func TestCommentLineOutOfRangeIsValidation(t *testing.T) {
	f := newReviewFixture(t)
	// A line past the anchor bound cannot land on any diff and must be rejected as a
	// validation miss, not narrowed to int (where on a 32-bit build it could wrap
	// onto a real position). The web inline composer feeds this straight from a form.
	huge := int64(1) << 40
	if _, err := f.reviews.CreateComment(f.ctx, f.reviewPK, "octocat", "hello", f.pr.Number, ReviewCommentInput{
		Path: "b.txt", Body: "out of range", Line: &huge,
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("out-of-range line err = %v, want ErrValidation", err)
	}
	// The same bound guards the legacy position anchor.
	if _, err := f.reviews.CreateComment(f.ctx, f.reviewPK, "octocat", "hello", f.pr.Number, ReviewCommentInput{
		Path: "b.txt", Body: "out of range", Position: &huge,
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("out-of-range position err = %v, want ErrValidation", err)
	}
}

func TestDismissClearsDecision(t *testing.T) {
	f := newReviewFixture(t)
	rev, err := f.reviews.CreateReview(f.ctx, f.reviewPK, "octocat", "hello", f.pr.Number, ReviewInput{
		Event: EventApprove,
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	// The owner, who holds write access, dismisses the approval.
	if _, err := f.reviews.DismissReview(f.ctx, f.ownerPK, "octocat", "hello", f.pr.Number, rev.ID, "stale"); err != nil {
		t.Fatalf("DismissReview: %v", err)
	}
	dec, err := f.reviews.ReviewDecision(f.ctx, f.ownerPK, "octocat", "hello", f.pr.Number)
	if err != nil {
		t.Fatalf("ReviewDecision: %v", err)
	}
	if dec != nil {
		t.Fatalf("decision = %v, want nil after dismiss", dec)
	}
}

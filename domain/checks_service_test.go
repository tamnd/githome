package domain

import (
	"testing"
)

func TestCommitStatusCombinedState(t *testing.T) {
	f := newReviewFixture(t)
	sha := f.pr.Head.SHA

	// A single pending status makes the combined state pending.
	if _, err := f.checks.CreateStatus(f.ctx, f.ownerPK, "octocat", "hello", sha, StatusInput{
		State: "pending", Context: "ci/build",
	}); err != nil {
		t.Fatalf("CreateStatus build pending: %v", err)
	}
	combined, err := f.checks.CombinedStatus(f.ctx, f.ownerPK, "octocat", "hello", sha)
	if err != nil {
		t.Fatalf("CombinedStatus: %v", err)
	}
	if combined.State != "pending" || combined.TotalCount != 1 {
		t.Fatalf("combined = %s / %d, want pending / 1", combined.State, combined.TotalCount)
	}

	// The latest status per context wins: build flips to success.
	if _, err := f.checks.CreateStatus(f.ctx, f.ownerPK, "octocat", "hello", sha, StatusInput{
		State: "success", Context: "ci/build",
	}); err != nil {
		t.Fatalf("CreateStatus build success: %v", err)
	}
	combined, err = f.checks.CombinedStatus(f.ctx, f.ownerPK, "octocat", "hello", sha)
	if err != nil {
		t.Fatalf("CombinedStatus: %v", err)
	}
	if combined.State != "success" || combined.TotalCount != 1 {
		t.Fatalf("combined after flip = %s / %d, want success / 1", combined.State, combined.TotalCount)
	}

	// A second context that fails drags the combined state to failure.
	if _, err := f.checks.CreateStatus(f.ctx, f.ownerPK, "octocat", "hello", sha, StatusInput{
		State: "failure", Context: "ci/lint",
	}); err != nil {
		t.Fatalf("CreateStatus lint failure: %v", err)
	}
	combined, err = f.checks.CombinedStatus(f.ctx, f.ownerPK, "octocat", "hello", sha)
	if err != nil {
		t.Fatalf("CombinedStatus: %v", err)
	}
	if combined.State != "failure" || combined.TotalCount != 2 {
		t.Fatalf("combined with lint = %s / %d, want failure / 2", combined.State, combined.TotalCount)
	}
}

func TestCheckRunRollupAndUpdate(t *testing.T) {
	f := newReviewFixture(t)
	sha := f.pr.Head.SHA

	run, err := f.checks.CreateCheckRun(f.ctx, f.ownerPK, "octocat", "hello", CheckRunInput{
		Name: "unit", HeadSHA: sha, Status: "in_progress",
	})
	if err != nil {
		t.Fatalf("CreateCheckRun: %v", err)
	}
	if run.StartedAt == nil {
		t.Errorf("in_progress run has no started_at")
	}

	// An in-progress run leaves the rollup pending.
	roll, err := f.checks.Rollup(f.ctx, f.ownerPK, "octocat", "hello", sha)
	if err != nil {
		t.Fatalf("Rollup: %v", err)
	}
	if roll.State != RollupPending {
		t.Fatalf("rollup = %s, want PENDING", roll.State)
	}

	// Completing it with a success conclusion makes the rollup success.
	if _, err := f.checks.UpdateCheckRun(f.ctx, f.ownerPK, "octocat", "hello", run.ID, CheckRunInput{
		Status: "completed", Conclusion: "success",
	}); err != nil {
		t.Fatalf("UpdateCheckRun: %v", err)
	}
	roll, err = f.checks.Rollup(f.ctx, f.ownerPK, "octocat", "hello", sha)
	if err != nil {
		t.Fatalf("Rollup: %v", err)
	}
	if roll.State != RollupSuccess {
		t.Fatalf("rollup after success = %s, want SUCCESS", roll.State)
	}

	// A failing status now drags the same rollup to failure: it folds both signals.
	if _, err := f.checks.CreateStatus(f.ctx, f.ownerPK, "octocat", "hello", sha, StatusInput{
		State: "failure", Context: "ci/lint",
	}); err != nil {
		t.Fatalf("CreateStatus: %v", err)
	}
	roll, err = f.checks.Rollup(f.ctx, f.ownerPK, "octocat", "hello", sha)
	if err != nil {
		t.Fatalf("Rollup: %v", err)
	}
	if roll.State != RollupFailure {
		t.Fatalf("rollup with failing status = %s, want FAILURE", roll.State)
	}
}

func TestStatusReportEnqueuesRecompute(t *testing.T) {
	f := newReviewFixture(t)
	sha := f.pr.Head.SHA
	if _, err := f.checks.CreateStatus(f.ctx, f.ownerPK, "octocat", "hello", sha, StatusInput{
		State: "success", Context: "ci/build",
	}); err != nil {
		t.Fatalf("CreateStatus: %v", err)
	}
	// The status anchors the pull request's head, so it enqueues a decision
	// recompute for the open pull request and the cached rollup refreshes.
	f.drainRecompute(t)
	state, err := f.st.GetPullCheckState(f.ctx, f.pr.PK)
	if err != nil {
		t.Fatalf("GetPullCheckState: %v", err)
	}
	if state.RollupState != RollupSuccess {
		t.Fatalf("cached rollup = %s, want SUCCESS", state.RollupState)
	}
}

func TestCheckRunWriteNeedsAccess(t *testing.T) {
	f := newReviewFixture(t)
	sha := f.pr.Head.SHA
	// The reviewer has read access only and cannot report a status or a check.
	if _, err := f.checks.CreateStatus(f.ctx, f.reviewPK, "octocat", "hello", sha, StatusInput{
		State: "success", Context: "ci/build",
	}); err == nil {
		t.Fatalf("reviewer CreateStatus succeeded, want forbidden")
	}
	if _, err := f.checks.CreateCheckRun(f.ctx, f.reviewPK, "octocat", "hello", CheckRunInput{
		Name: "unit", HeadSHA: sha,
	}); err == nil {
		t.Fatalf("reviewer CreateCheckRun succeeded, want forbidden")
	}
}

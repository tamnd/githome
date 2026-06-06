package store_test

import (
	"context"
	"testing"

	"github.com/tamnd/githome/store"
)

// TestUsersByPKs verifies that UsersByPKs returns the correct set of users
// and that missing PKs are silently absent.
func TestUsersByPKs(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		u1 := &store.UserRow{Login: "alice", Type: "User"}
		u2 := &store.UserRow{Login: "bob", Type: "User"}
		if err := st.InsertUser(ctx, u1); err != nil {
			t.Fatalf("InsertUser alice: %v", err)
		}
		if err := st.InsertUser(ctx, u2); err != nil {
			t.Fatalf("InsertUser bob: %v", err)
		}

		got, err := st.UsersByPKs(ctx, []int64{u1.PK, u2.PK, 999999})
		if err != nil {
			t.Fatalf("UsersByPKs: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("want 2 users, got %d", len(got))
		}
		if got[u1.PK].Login != "alice" {
			t.Errorf("alice login = %q", got[u1.PK].Login)
		}
		if got[u2.PK].Login != "bob" {
			t.Errorf("bob login = %q", got[u2.PK].Login)
		}
		if _, present := got[999999]; present {
			t.Error("missing PK should not appear in result")
		}

		// empty input returns empty map, no error
		empty, err := st.UsersByPKs(ctx, nil)
		if err != nil {
			t.Fatalf("UsersByPKs(nil): %v", err)
		}
		if len(empty) != 0 {
			t.Errorf("expected empty map, got %d entries", len(empty))
		}
	})
}

// TestLabelsByIssuePKs verifies that labels are returned grouped by issue PK.
func TestLabelsByIssuePKs(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "labeled"})
		iss1 := seedIssue(t, st, repo.PK, repo.OwnerPK, "issue one")
		iss2 := seedIssue(t, st, repo.PK, repo.OwnerPK, "issue two")

		// insert two labels and attach them
		lBug := &store.LabelRow{RepoPK: repo.PK, Name: "bug", Color: "fc2929"}
		lFeat := &store.LabelRow{RepoPK: repo.PK, Name: "feature", Color: "84b6eb"}
		if err := st.InsertLabel(ctx, lBug); err != nil {
			t.Fatalf("InsertLabel bug: %v", err)
		}
		if err := st.InsertLabel(ctx, lFeat); err != nil {
			t.Fatalf("InsertLabel feature: %v", err)
		}

		if err := st.WithTx(ctx, func(tx *store.Tx) error {
			if err := tx.AttachLabels(ctx, iss1.PK, []int64{lBug.PK, lFeat.PK}); err != nil {
				return err
			}
			return tx.AttachLabels(ctx, iss2.PK, []int64{lBug.PK})
		}); err != nil {
			t.Fatalf("AttachLabels: %v", err)
		}

		got, err := st.LabelsByIssuePKs(ctx, []int64{iss1.PK, iss2.PK})
		if err != nil {
			t.Fatalf("LabelsByIssuePKs: %v", err)
		}
		if len(got[iss1.PK]) != 2 {
			t.Errorf("issue1: want 2 labels, got %d", len(got[iss1.PK]))
		}
		if len(got[iss2.PK]) != 1 {
			t.Errorf("issue2: want 1 label, got %d", len(got[iss2.PK]))
		}
		if got[iss2.PK][0].Name != "bug" {
			t.Errorf("issue2 label = %q, want bug", got[iss2.PK][0].Name)
		}
	})
}

// TestAssigneesByIssuePKs verifies that assignees are returned grouped by
// issue PK.
func TestAssigneesByIssuePKs(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "assigned"})
		extra := &store.UserRow{Login: "collaborator", Type: "User"}
		if err := st.InsertUser(ctx, extra); err != nil {
			t.Fatalf("InsertUser collaborator: %v", err)
		}

		iss1 := seedIssue(t, st, repo.PK, repo.OwnerPK, "issue one")
		iss2 := seedIssue(t, st, repo.PK, repo.OwnerPK, "issue two")

		if err := st.WithTx(ctx, func(tx *store.Tx) error {
			if err := tx.AddAssignees(ctx, iss1.PK, []int64{repo.OwnerPK, extra.PK}); err != nil {
				return err
			}
			return tx.AddAssignees(ctx, iss2.PK, []int64{extra.PK})
		}); err != nil {
			t.Fatalf("AddAssignees: %v", err)
		}

		got, err := st.AssigneesByIssuePKs(ctx, []int64{iss1.PK, iss2.PK})
		if err != nil {
			t.Fatalf("AssigneesByIssuePKs: %v", err)
		}
		if len(got[iss1.PK]) != 2 {
			t.Errorf("issue1: want 2 assignees, got %d", len(got[iss1.PK]))
		}
		if len(got[iss2.PK]) != 1 {
			t.Errorf("issue2: want 1 assignee, got %d", len(got[iss2.PK]))
		}
	})
}

// TestReactionRollupsBySubjectPKs verifies that reaction counts are grouped
// correctly per subject PK and that an empty rollup is returned for PKs with
// no reactions.
func TestReactionRollupsBySubjectPKs(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "reacted"})
		iss1 := seedIssue(t, st, repo.PK, repo.OwnerPK, "issue one")
		iss2 := seedIssue(t, st, repo.PK, repo.OwnerPK, "issue two")

		// Add +1 and heart to iss1, nothing to iss2
		r1 := &store.ReactionRow{SubjectType: "issue", SubjectPK: iss1.PK, UserPK: repo.OwnerPK, Content: "+1"}
		r2 := &store.ReactionRow{SubjectType: "issue", SubjectPK: iss1.PK, UserPK: repo.OwnerPK, Content: "heart"}
		if _, err := st.InsertReaction(ctx, r1); err != nil {
			t.Fatalf("InsertReaction +1: %v", err)
		}
		if _, err := st.InsertReaction(ctx, r2); err != nil {
			t.Fatalf("InsertReaction heart: %v", err)
		}

		got, err := st.ReactionRollupsBySubjectPKs(ctx, "issue", []int64{iss1.PK, iss2.PK})
		if err != nil {
			t.Fatalf("ReactionRollupsBySubjectPKs: %v", err)
		}
		if got[iss1.PK].TotalCount != 2 {
			t.Errorf("iss1 total = %d, want 2", got[iss1.PK].TotalCount)
		}
		if got[iss1.PK].Counts["+1"] != 1 {
			t.Errorf("+1 count = %d, want 1", got[iss1.PK].Counts["+1"])
		}
		if got[iss2.PK].TotalCount != 0 {
			t.Errorf("iss2 total = %d, want 0", got[iss2.PK].TotalCount)
		}
	})
}

// TestIssuesByPKs verifies bulk issue lookup.
func TestIssuesByPKs(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "bulk"})
		iss1 := seedIssue(t, st, repo.PK, repo.OwnerPK, "alpha")
		iss2 := seedIssue(t, st, repo.PK, repo.OwnerPK, "beta")

		got, err := st.IssuesByPKs(ctx, []int64{iss1.PK, iss2.PK, 999999})
		if err != nil {
			t.Fatalf("IssuesByPKs: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("want 2, got %d", len(got))
		}
		if got[iss1.PK].Title != "alpha" {
			t.Errorf("iss1 title = %q", got[iss1.PK].Title)
		}
		if _, present := got[999999]; present {
			t.Error("missing PK should not appear")
		}
	})
}

// TestMilestonesByPKs verifies bulk milestone lookup.
func TestMilestonesByPKs(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "milestone-bulk"})
		m1 := &store.MilestoneRow{RepoPK: repo.PK, Title: "v1"}
		m2 := &store.MilestoneRow{RepoPK: repo.PK, Title: "v2"}
		if err := st.InsertMilestone(ctx, m1); err != nil {
			t.Fatalf("InsertMilestone v1: %v", err)
		}
		if err := st.InsertMilestone(ctx, m2); err != nil {
			t.Fatalf("InsertMilestone v2: %v", err)
		}

		got, err := st.MilestonesByPKs(ctx, []int64{m1.PK, m2.PK, 999999})
		if err != nil {
			t.Fatalf("MilestonesByPKs: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("want 2, got %d", len(got))
		}
		if got[m1.PK].Title != "v1" {
			t.Errorf("m1 title = %q", got[m1.PK].Title)
		}
	})
}

// TestBatchVsSingleConsistency verifies that batch loaders return the same data
// as the corresponding single-row loaders, so a list page is byte-identical to
// fetching each row individually.
func TestBatchVsSingleConsistency(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "consistency"})
		u2 := &store.UserRow{Login: "alice", Type: "User"}
		if err := st.InsertUser(ctx, u2); err != nil {
			t.Fatalf("InsertUser alice: %v", err)
		}

		// Batch user lookup matches single lookup.
		single, err := st.UserByPK(ctx, repo.OwnerPK)
		if err != nil {
			t.Fatalf("UserByPK: %v", err)
		}
		batch, err := st.UsersByPKs(ctx, []int64{repo.OwnerPK})
		if err != nil {
			t.Fatalf("UsersByPKs: %v", err)
		}
		if batch[repo.OwnerPK].Login != single.Login {
			t.Errorf("login mismatch: batch=%q single=%q", batch[repo.OwnerPK].Login, single.Login)
		}
		if batch[repo.OwnerPK].DBID != single.DBID {
			t.Errorf("dbid mismatch: batch=%d single=%d", batch[repo.OwnerPK].DBID, single.DBID)
		}

		// Labels: attach to issue, compare LabelsByIssue vs LabelsByIssuePKs.
		iss := seedIssue(t, st, repo.PK, repo.OwnerPK, "test issue")
		lBug := &store.LabelRow{RepoPK: repo.PK, Name: "bug", Color: "fc2929"}
		if err := st.InsertLabel(ctx, lBug); err != nil {
			t.Fatalf("InsertLabel: %v", err)
		}
		if err := st.WithTx(ctx, func(tx *store.Tx) error {
			return tx.AttachLabels(ctx, iss.PK, []int64{lBug.PK})
		}); err != nil {
			t.Fatalf("AttachLabels: %v", err)
		}
		singleLabels, err := st.LabelsByIssue(ctx, iss.PK)
		if err != nil {
			t.Fatalf("LabelsByIssue: %v", err)
		}
		batchLabels, err := st.LabelsByIssuePKs(ctx, []int64{iss.PK})
		if err != nil {
			t.Fatalf("LabelsByIssuePKs: %v", err)
		}
		if len(batchLabels[iss.PK]) != len(singleLabels) {
			t.Errorf("label count mismatch: batch=%d single=%d", len(batchLabels[iss.PK]), len(singleLabels))
		}
		if len(singleLabels) > 0 && batchLabels[iss.PK][0].Name != singleLabels[0].Name {
			t.Errorf("label name mismatch: batch=%q single=%q", batchLabels[iss.PK][0].Name, singleLabels[0].Name)
		}

		// Assignees: add one, compare ListAssigneePKs vs AssigneesByIssuePKs.
		if err := st.WithTx(ctx, func(tx *store.Tx) error {
			return tx.AddAssignees(ctx, iss.PK, []int64{repo.OwnerPK})
		}); err != nil {
			t.Fatalf("AddAssignees: %v", err)
		}
		singleAssignees, err := st.ListAssigneePKs(ctx, iss.PK)
		if err != nil {
			t.Fatalf("ListAssigneePKs: %v", err)
		}
		batchAssignees, err := st.AssigneesByIssuePKs(ctx, []int64{iss.PK})
		if err != nil {
			t.Fatalf("AssigneesByIssuePKs: %v", err)
		}
		if len(batchAssignees[iss.PK]) != len(singleAssignees) {
			t.Errorf("assignee count mismatch: batch=%d single=%d", len(batchAssignees[iss.PK]), len(singleAssignees))
		}
		if len(singleAssignees) > 0 && batchAssignees[iss.PK][0] != singleAssignees[0] {
			t.Errorf("assignee pk mismatch: batch=%d single=%d", batchAssignees[iss.PK][0], singleAssignees[0])
		}

		// Reactions: add one, compare single rollup vs batch rollup.
		r := &store.ReactionRow{SubjectType: "issue", SubjectPK: iss.PK, UserPK: repo.OwnerPK, Content: "+1"}
		if _, err := st.InsertReaction(ctx, r); err != nil {
			t.Fatalf("InsertReaction: %v", err)
		}
		singleRollup, err := st.ReactionRollupFor(ctx, "issue", iss.PK)
		if err != nil {
			t.Fatalf("ReactionRollupFor: %v", err)
		}
		batchRollup, err := st.ReactionRollupsBySubjectPKs(ctx, "issue", []int64{iss.PK})
		if err != nil {
			t.Fatalf("ReactionRollupsBySubjectPKs: %v", err)
		}
		if batchRollup[iss.PK].TotalCount != singleRollup.TotalCount {
			t.Errorf("reaction total mismatch: batch=%d single=%d", batchRollup[iss.PK].TotalCount, singleRollup.TotalCount)
		}
	})
}

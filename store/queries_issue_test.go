package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tamnd/githome/store"
)

// seedIssue creates one issue in repo via the transaction path the issue
// service uses: allocate a number, insert the row.
func seedIssue(t *testing.T, st *store.Store, repoPK, userPK int64, title string) *store.IssueRow {
	t.Helper()
	ctx := context.Background()
	iss := &store.IssueRow{RepoPK: repoPK, UserPK: userPK, Title: title, State: "open"}
	err := st.WithTx(ctx, func(tx *store.Tx) error {
		n, err := tx.AllocIssueNumber(ctx, repoPK)
		if err != nil {
			return err
		}
		iss.Number = n
		return tx.InsertIssue(ctx, iss)
	})
	if err != nil {
		t.Fatalf("seedIssue: %v", err)
	}
	return iss
}

func TestIssueCreateAndRead(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})

		first := seedIssue(t, st, repo.PK, repo.OwnerPK, "first issue")
		second := seedIssue(t, st, repo.PK, repo.OwnerPK, "second issue")
		if first.Number != 1 || second.Number != 2 {
			t.Fatalf("issue numbers should start at 1 and increment: got %d, %d", first.Number, second.Number)
		}
		if first.DBID == 0 || first.PK == 0 || first.CreatedAt.IsZero() {
			t.Fatalf("server fields not filled: %+v", first)
		}

		got, err := st.GetIssueByNumber(ctx, repo.PK, 1)
		if err != nil {
			t.Fatalf("GetIssueByNumber: %v", err)
		}
		if got.Title != "first issue" || got.State != "open" {
			t.Errorf("round-trip mismatch: %+v", got)
		}

		byDBID, err := st.GetIssueByDBID(ctx, first.DBID)
		if err != nil || byDBID.PK != first.PK {
			t.Fatalf("GetIssueByDBID: %v row=%+v", err, byDBID)
		}
		if _, err := st.GetIssueByNumber(ctx, repo.PK, 999); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("missing issue: err = %v, want ErrNotFound", err)
		}
	})
}

func TestIssueListFilters(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})
		open1 := seedIssue(t, st, repo.PK, repo.OwnerPK, "open one")
		_ = seedIssue(t, st, repo.PK, repo.OwnerPK, "open two")
		closed := seedIssue(t, st, repo.PK, repo.OwnerPK, "closed one")

		// Close the third issue through the optimistic-lock update path.
		if err := st.WithTx(ctx, func(tx *store.Tx) error {
			closed.State = "closed"
			return tx.UpdateIssue(ctx, closed)
		}); err != nil {
			t.Fatalf("close issue: %v", err)
		}

		openList, err := st.ListIssues(ctx, repo.PK, store.IssueFilter{State: "open"})
		if err != nil {
			t.Fatalf("ListIssues open: %v", err)
		}
		if len(openList) != 2 {
			t.Fatalf("open list = %d issues, want 2", len(openList))
		}
		closedList, _ := st.ListIssues(ctx, repo.PK, store.IssueFilter{State: "closed"})
		if len(closedList) != 1 || closedList[0].Number != closed.Number {
			t.Fatalf("closed list wrong: %+v", closedList)
		}
		all, _ := st.ListIssues(ctx, repo.PK, store.IssueFilter{State: "all"})
		if len(all) != 3 {
			t.Fatalf("all list = %d, want 3", len(all))
		}
		n, err := st.CountIssues(ctx, repo.PK, store.IssueFilter{State: "open"})
		if err != nil || n != 2 {
			t.Fatalf("CountIssues open = %d (%v), want 2", n, err)
		}
		// Creator filter narrows to the author.
		byCreator, _ := st.ListIssues(ctx, repo.PK, store.IssueFilter{State: "all", CreatorPK: &open1.UserPK})
		if len(byCreator) != 3 {
			t.Fatalf("creator filter = %d, want 3 (same author)", len(byCreator))
		}
	})
}

func TestOptimisticLockConflict(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})
		iss := seedIssue(t, st, repo.PK, repo.OwnerPK, "race me")

		// Two readers hold the same lock_version; the first write wins.
		stale := *iss
		if err := st.WithTx(ctx, func(tx *store.Tx) error {
			iss.Title = "winner"
			return tx.UpdateIssue(ctx, iss)
		}); err != nil {
			t.Fatalf("first update: %v", err)
		}
		err := st.WithTx(ctx, func(tx *store.Tx) error {
			stale.Title = "loser"
			return tx.UpdateIssue(ctx, &stale)
		})
		if !errors.Is(err, store.ErrOptimisticLock) {
			t.Fatalf("stale update: err = %v, want ErrOptimisticLock", err)
		}
	})
}

func TestLabelsAttachAndList(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})
		iss := seedIssue(t, st, repo.PK, repo.OwnerPK, "needs labels")

		bug := &store.LabelRow{RepoPK: repo.PK, Name: "bug", Color: "d73a4a"}
		enh := &store.LabelRow{RepoPK: repo.PK, Name: "enhancement", Color: "a2eeef"}
		for _, l := range []*store.LabelRow{bug, enh} {
			if err := st.InsertLabel(ctx, l); err != nil {
				t.Fatalf("InsertLabel: %v", err)
			}
		}
		// Case-insensitive resolve, order preserved, missing skipped.
		got, err := st.LabelsByNames(ctx, repo.PK, []string{"BUG", "ghost", "enhancement"})
		if err != nil {
			t.Fatalf("LabelsByNames: %v", err)
		}
		if len(got) != 2 || got[0].Name != "bug" || got[1].Name != "enhancement" {
			t.Fatalf("LabelsByNames = %+v", got)
		}

		if err := st.WithTx(ctx, func(tx *store.Tx) error {
			return tx.AttachLabels(ctx, iss.PK, []int64{bug.PK, enh.PK})
		}); err != nil {
			t.Fatalf("AttachLabels: %v", err)
		}
		attached, err := st.LabelsByIssue(ctx, iss.PK)
		if err != nil || len(attached) != 2 {
			t.Fatalf("LabelsByIssue = %+v (%v)", attached, err)
		}
		// Filtering by label finds the issue.
		hits, _ := st.ListIssues(ctx, repo.PK, store.IssueFilter{State: "all", Labels: []string{"bug"}})
		if len(hits) != 1 || hits[0].PK != iss.PK {
			t.Fatalf("label filter = %+v", hits)
		}
	})
}

func TestAssigneesRoundTrip(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})
		iss := seedIssue(t, st, repo.PK, repo.OwnerPK, "assign me")
		hubot := &store.UserRow{Login: "hubot", Type: "User"}
		if err := st.InsertUser(ctx, hubot); err != nil {
			t.Fatalf("InsertUser: %v", err)
		}

		if err := st.WithTx(ctx, func(tx *store.Tx) error {
			return tx.AddAssignees(ctx, iss.PK, []int64{repo.OwnerPK, hubot.PK})
		}); err != nil {
			t.Fatalf("AddAssignees: %v", err)
		}
		pks, err := st.ListAssigneePKs(ctx, iss.PK)
		if err != nil || len(pks) != 2 || pks[0] != repo.OwnerPK || pks[1] != hubot.PK {
			t.Fatalf("ListAssigneePKs = %v (%v), want order preserved", pks, err)
		}
		if ok, _ := st.IsAssigned(ctx, iss.PK, hubot.PK); !ok {
			t.Errorf("IsAssigned(hubot) = false, want true")
		}
		if err := st.WithTx(ctx, func(tx *store.Tx) error {
			return tx.RemoveAssignees(ctx, iss.PK, []int64{hubot.PK})
		}); err != nil {
			t.Fatalf("RemoveAssignees: %v", err)
		}
		if ok, _ := st.IsAssigned(ctx, iss.PK, hubot.PK); ok {
			t.Errorf("IsAssigned(hubot) after remove = true, want false")
		}
	})
}

func TestCommentsCountTracking(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})
		iss := seedIssue(t, st, repo.PK, repo.OwnerPK, "discuss")

		c := &store.CommentRow{IssuePK: iss.PK, UserPK: repo.OwnerPK, Body: "first comment"}
		if err := st.InsertComment(ctx, c); err != nil {
			t.Fatalf("InsertComment: %v", err)
		}
		got, _ := st.GetIssueByPK(ctx, iss.PK)
		if got.CommentsCount != 1 {
			t.Fatalf("comments_count = %d, want 1", got.CommentsCount)
		}
		list, _ := st.ListIssueComments(ctx, iss.PK, 0, 0)
		if len(list) != 1 || list[0].Body != "first comment" {
			t.Fatalf("ListIssueComments = %+v", list)
		}
		if err := st.DeleteComment(ctx, c.PK); err != nil {
			t.Fatalf("DeleteComment: %v", err)
		}
		got, _ = st.GetIssueByPK(ctx, iss.PK)
		if got.CommentsCount != 0 {
			t.Fatalf("comments_count after delete = %d, want 0", got.CommentsCount)
		}
	})
}

func TestReactionsIdempotentAndRollup(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})
		iss := seedIssue(t, st, repo.PK, repo.OwnerPK, "react to me")

		r := &store.ReactionRow{SubjectType: "issue", SubjectPK: iss.PK, UserPK: repo.OwnerPK, Content: "+1"}
		created, err := st.InsertReaction(ctx, r)
		if err != nil || !created {
			t.Fatalf("first InsertReaction: created=%v err=%v", created, err)
		}
		dup := &store.ReactionRow{SubjectType: "issue", SubjectPK: iss.PK, UserPK: repo.OwnerPK, Content: "+1"}
		created, err = st.InsertReaction(ctx, dup)
		if err != nil || created {
			t.Fatalf("duplicate InsertReaction: created=%v err=%v, want created=false", created, err)
		}
		if dup.DBID != r.DBID {
			t.Errorf("duplicate should return existing reaction: got db_id %d, want %d", dup.DBID, r.DBID)
		}
		roll, err := st.ReactionRollupFor(ctx, "issue", iss.PK)
		if err != nil || roll.TotalCount != 1 || roll.Counts["+1"] != 1 {
			t.Fatalf("rollup = %+v (%v), want total 1", roll, err)
		}
		if err := st.DeleteReaction(ctx, "issue", iss.PK, r.DBID); err != nil {
			t.Fatalf("DeleteReaction: %v", err)
		}
		roll, _ = st.ReactionRollupFor(ctx, "issue", iss.PK)
		if roll.TotalCount != 0 {
			t.Fatalf("rollup after delete = %+v, want 0", roll)
		}
	})
}

func TestMilestoneCounts(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})
		m := &store.MilestoneRow{RepoPK: repo.PK, Title: "v1.0", CreatorPK: &repo.OwnerPK}
		if err := st.InsertMilestone(ctx, m); err != nil {
			t.Fatalf("InsertMilestone: %v", err)
		}
		if m.Number != 1 {
			t.Fatalf("milestone number = %d, want 1", m.Number)
		}

		// Two issues on the milestone, one closed.
		for i, state := range []string{"open", "closed"} {
			iss := seedIssue(t, st, repo.PK, repo.OwnerPK, "m issue")
			_ = i
			if err := st.WithTx(ctx, func(tx *store.Tx) error {
				iss.MilestonePK = &m.PK
				iss.State = state
				return tx.UpdateIssue(ctx, iss)
			}); err != nil {
				t.Fatalf("attach to milestone: %v", err)
			}
		}
		open, closed, err := st.MilestoneIssueCounts(ctx, m.PK)
		if err != nil || open != 1 || closed != 1 {
			t.Fatalf("MilestoneIssueCounts = (%d, %d) %v, want (1, 1)", open, closed, err)
		}
	})
}

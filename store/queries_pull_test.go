package store_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/tamnd/githome/store"
)

// seedPull creates an issue and its pull request extension in repo, the two-row
// shape the pull request service writes in one transaction.
func seedPull(t *testing.T, st *store.Store, repo *store.RepoRow, title, base, head string) (*store.IssueRow, *store.PullRow) {
	t.Helper()
	ctx := context.Background()
	iss := seedIssue(t, st, repo.PK, repo.OwnerPK, title)
	pr := &store.PullRow{
		IssuePK: iss.PK,
		RepoPK:  repo.PK,
		BaseRef: base,
		BaseSHA: "1111111111111111111111111111111111111111",
		HeadRef: head,
		HeadSHA: "2222222222222222222222222222222222222222",
	}
	if err := st.WithTx(ctx, func(tx *store.Tx) error {
		return tx.InsertPull(ctx, pr)
	}); err != nil {
		t.Fatalf("seedPull: %v", err)
	}
	return iss, pr
}

func TestPullCreateAndRead(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})
		iss, pr := seedPull(t, st, repo, "add a feature", "main", "feature")

		if pr.PK == 0 || pr.DBID == 0 || pr.CreatedAt.IsZero() {
			t.Fatalf("server fields not filled: %+v", pr)
		}
		// A fresh pull request has no computed merge state; the read path surfaces
		// the NULL mergeable as UNKNOWN.
		if pr.MergeableState != "unknown" {
			t.Errorf("fresh mergeable_state = %q, want unknown", pr.MergeableState)
		}

		byIssue, err := st.GetPullByIssuePK(ctx, iss.PK)
		if err != nil {
			t.Fatalf("GetPullByIssuePK: %v", err)
		}
		if byIssue.PK != pr.PK || byIssue.BaseRef != "main" || byIssue.HeadRef != "feature" {
			t.Errorf("round-trip mismatch: %+v", byIssue)
		}
		if byIssue.Mergeable != nil {
			t.Errorf("fresh mergeable = %v, want nil (unknown)", *byIssue.Mergeable)
		}

		byDBID, err := st.GetPullByDBID(ctx, pr.DBID)
		if err != nil || byDBID.PK != pr.PK {
			t.Fatalf("GetPullByDBID: %v row=%+v", err, byDBID)
		}
		if _, err := st.GetPullByDBID(ctx, 999999); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("missing pull: err = %v, want ErrNotFound", err)
		}
	})
}

func TestPullListAndCountFilters(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})
		_, open1 := seedPull(t, st, repo, "open one", "main", "f1")
		_, _ = seedPull(t, st, repo, "open two", "main", "f2")
		closedIss, _ := seedPull(t, st, repo, "closed one", "main", "f3")

		// Close the third pull request through its issue row.
		if err := st.WithTx(ctx, func(tx *store.Tx) error {
			closedIss.State = "closed"
			return tx.UpdateIssue(ctx, closedIss)
		}); err != nil {
			t.Fatalf("close pull issue: %v", err)
		}

		// The empty state lists open ones, newest number first.
		openList, err := st.ListPulls(ctx, repo.PK, "", 30, 0)
		if err != nil {
			t.Fatalf("ListPulls open: %v", err)
		}
		if len(openList) != 2 {
			t.Fatalf("open list = %d pulls, want 2", len(openList))
		}
		if openList[0].PK == open1.PK {
			t.Errorf("open list not newest-first: %+v", openList)
		}
		closedList, _ := st.ListPulls(ctx, repo.PK, "closed", 30, 0)
		if len(closedList) != 1 {
			t.Fatalf("closed list = %d, want 1", len(closedList))
		}
		allList, _ := st.ListPulls(ctx, repo.PK, "all", 30, 0)
		if len(allList) != 3 {
			t.Fatalf("all list = %d, want 3", len(allList))
		}
		n, err := st.CountPulls(ctx, repo.PK, "open")
		if err != nil || n != 2 {
			t.Fatalf("CountPulls open = %d (%v), want 2", n, err)
		}
	})
}

func TestListPullsPageKeysetWalk(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})
		const total = 5
		for i := 0; i < total; i++ {
			seedPull(t, st, repo, "pull", "main", "f"+strconv.Itoa(i))
		}

		// Walk the open pulls two at a time following the number cursor, the way
		// the REST handler does. Every pull must appear once, newest number first.
		// PullRow carries the issue number on its backing issue, resolved here via
		// PullNumberByPK, which is also how the cursor advances.
		seen := map[int64]bool{}
		var cursor *store.PullCursor
		var prevNumber int64 = 1 << 62
		for pages := 0; pages < 20; pages++ {
			rows, hasMore, err := st.ListPullsPage(ctx, repo.PK, "", cursor, 2)
			if err != nil {
				t.Fatalf("ListPullsPage: %v", err)
			}
			var lastNumber int64
			for _, r := range rows {
				number, err := st.PullNumberByPK(ctx, r.PK)
				if err != nil {
					t.Fatalf("PullNumberByPK: %v", err)
				}
				if seen[number] {
					t.Fatalf("pull number %d returned twice", number)
				}
				if number >= prevNumber {
					t.Fatalf("out of order: number %d after %d", number, prevNumber)
				}
				seen[number] = true
				prevNumber = number
				lastNumber = number
			}
			if !hasMore {
				break
			}
			cursor = &store.PullCursor{Number: lastNumber}
		}
		if len(seen) != total {
			t.Fatalf("keyset walk covered %d pulls, want %d", len(seen), total)
		}
	})
}

func TestPullByHeadAndBaseRef(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})
		_, head := seedPull(t, st, repo, "head match", "main", "feature")
		seedPull(t, st, repo, "base match", "release", "other")

		byHead, err := st.OpenPullsByHeadRef(ctx, repo.PK, "feature")
		if err != nil || len(byHead) != 1 || byHead[0].PK != head.PK {
			t.Fatalf("OpenPullsByHeadRef = %+v (%v), want the feature pull", byHead, err)
		}
		byBase, err := st.OpenPullsByBaseRef(ctx, repo.PK, "main")
		if err != nil || len(byBase) != 1 || byBase[0].PK != head.PK {
			t.Fatalf("OpenPullsByBaseRef(main) = %+v (%v), want the feature pull", byBase, err)
		}
		// A branch no open pull request tracks matches nothing.
		none, _ := st.OpenPullsByHeadRef(ctx, repo.PK, "ghost")
		if len(none) != 0 {
			t.Errorf("OpenPullsByHeadRef(ghost) = %+v, want empty", none)
		}
	})
}

func TestSetMergeabilityRoundTrip(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})
		iss, _ := seedPull(t, st, repo, "compute me", "main", "feature")

		// The worker writes a resolved mergeable state over the NULL placeholder.
		yes := true
		no := false
		now := time.Now().UTC().Truncate(time.Second)
		if err := st.SetMergeability(ctx, iss.PK, &yes, "clean", &no, 12, 3, 2, 4, now); err != nil {
			t.Fatalf("SetMergeability: %v", err)
		}
		got, _ := st.GetPullByIssuePK(ctx, iss.PK)
		if got.Mergeable == nil || !*got.Mergeable {
			t.Fatalf("mergeable = %v, want true", got.Mergeable)
		}
		if got.MergeableState != "clean" || got.Additions != 12 || got.ChangedFiles != 2 || got.CommitsCount != 4 {
			t.Errorf("merge state not persisted: %+v", got)
		}
		if got.MergeabilityCheckedAt == nil {
			t.Errorf("checked_at not stamped")
		}

		// A push moves the head and resets the cached state back to unknown.
		if err := st.WithTx(ctx, func(tx *store.Tx) error {
			return tx.UpdatePullHead(ctx, got.PK, "3333333333333333333333333333333333333333")
		}); err != nil {
			t.Fatalf("UpdatePullHead: %v", err)
		}
		after, _ := st.GetPullByIssuePK(ctx, iss.PK)
		if after.Mergeable != nil || after.MergeableState != "unknown" || after.MergeabilityCheckedAt != nil {
			t.Errorf("head move did not reset merge state: %+v", after)
		}
		if after.HeadSHA != "3333333333333333333333333333333333333333" {
			t.Errorf("head sha not advanced: %s", after.HeadSHA)
		}
	})
}

func TestMarkMergedClosesPull(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "Hello-World"})
		iss, pr := seedPull(t, st, repo, "merge me", "main", "feature")

		when := time.Now().UTC().Truncate(time.Second)
		mergeSHA := "4444444444444444444444444444444444444444"
		if err := st.WithTx(ctx, func(tx *store.Tx) error {
			if err := tx.MarkMerged(ctx, pr.PK, repo.OwnerPK, mergeSHA, when); err != nil {
				return err
			}
			iss.State = "closed"
			return tx.UpdateIssue(ctx, iss)
		}); err != nil {
			t.Fatalf("MarkMerged: %v", err)
		}
		got, _ := st.GetPullByIssuePK(ctx, iss.PK)
		if !got.Merged || got.MergedAt == nil || got.MergedByPK == nil || *got.MergedByPK != repo.OwnerPK {
			t.Fatalf("merge not recorded: %+v", got)
		}
		if got.MergeCommitSHA == nil || *got.MergeCommitSHA != mergeSHA {
			t.Errorf("merge commit sha = %v, want %s", got.MergeCommitSHA, mergeSHA)
		}
		closed, _ := st.GetIssueByPK(ctx, iss.PK)
		if closed.State != "closed" {
			t.Errorf("merged pull issue state = %q, want closed", closed.State)
		}
	})
}

package store_test

import (
	"context"
	"testing"

	"github.com/tamnd/githome/store"
)

// seedOwner inserts one user and returns its primary key, for tests that place
// several repositories under a single owner (which seedRepo, inserting a fresh
// owner each call, cannot do).
func seedOwner(t *testing.T, st *store.Store, login string) int64 {
	t.Helper()
	u := &store.UserRow{Login: login, Type: "User"}
	if err := st.InsertUser(context.Background(), u); err != nil {
		t.Fatalf("InsertUser %s: %v", login, err)
	}
	return u.PK
}

// seedRepoFor inserts a repository under an existing owner.
func seedRepoFor(t *testing.T, st *store.Store, ownerPK int64, r *store.RepoRow) *store.RepoRow {
	t.Helper()
	r.OwnerPK = ownerPK
	if err := st.InsertRepo(context.Background(), r); err != nil {
		t.Fatalf("InsertRepo %s: %v", r.Name, err)
	}
	return r
}

// TestSearchIssuesVisibility confirms the cross-repository issue scan returns an
// issue from a public repository and from a private repository the viewer owns,
// but hides an issue in a private repository the viewer does not own. The same
// predicate runs on both dialects.
func TestSearchIssuesVisibility(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		pub := seedRepo(t, st, "octocat", &store.RepoRow{Name: "public"})
		priv := seedRepo(t, st, "hubot", &store.RepoRow{Name: "secret", Private: true})

		seedIssue(t, st, pub.PK, pub.OwnerPK, "shared topic")
		seedIssue(t, st, priv.PK, priv.OwnerPK, "shared topic")

		// Anonymous viewer (pk 0) sees only the public issue.
		anon, err := st.SearchIssues(ctx, store.IssueSearch{ViewerPK: 0, Terms: []string{"shared"}, MatchTitle: true, MatchBody: true})
		if err != nil {
			t.Fatalf("SearchIssues anon: %v", err)
		}
		if len(anon) != 1 {
			t.Fatalf("anonymous search = %d issues, want 1 (private hidden)", len(anon))
		}
		if n, _ := st.CountSearchIssues(ctx, store.IssueSearch{ViewerPK: 0, Terms: []string{"shared"}, MatchTitle: true, MatchBody: true}); n != 1 {
			t.Errorf("anonymous count = %d, want 1", n)
		}

		// The private repo's owner sees both issues.
		owner, err := st.SearchIssues(ctx, store.IssueSearch{ViewerPK: priv.OwnerPK, Terms: []string{"shared"}, MatchTitle: true, MatchBody: true})
		if err != nil {
			t.Fatalf("SearchIssues owner: %v", err)
		}
		if len(owner) != 2 {
			t.Fatalf("owner search = %d issues, want 2", len(owner))
		}
	})
}

// TestSearchIssuesRepoScope confirms an explicit repo pk scope narrows the scan
// to a single repository.
func TestSearchIssuesRepoScope(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		octocat := seedOwner(t, st, "octocat")
		a := seedRepoFor(t, st, octocat, &store.RepoRow{Name: "alpha"})
		b := seedRepoFor(t, st, octocat, &store.RepoRow{Name: "beta"})
		seedIssue(t, st, a.PK, a.OwnerPK, "common word")
		seedIssue(t, st, b.PK, b.OwnerPK, "common word")

		got, err := st.SearchIssues(ctx, store.IssueSearch{
			Terms: []string{"common"}, MatchTitle: true, MatchBody: true, RepoPKs: []int64{a.PK},
		})
		if err != nil {
			t.Fatalf("SearchIssues: %v", err)
		}
		if len(got) != 1 || got[0].RepoPK != a.PK {
			t.Fatalf("repo-scoped search = %+v, want one issue in repo %d", got, a.PK)
		}
	})
}

// TestSearchRepositoriesTermAndVisibility confirms the repository scan matches a
// term against name and description and hides private repositories the viewer
// cannot see.
func TestSearchRepositoriesTermAndVisibility(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		desc := "a searchable description"
		seedRepo(t, st, "octocat", &store.RepoRow{Name: "findme", Description: &desc})
		seedRepo(t, st, "hubot", &store.RepoRow{Name: "findme-private", Private: true})

		got, err := st.SearchRepositories(ctx, store.RepoSearch{ViewerPK: 0, Terms: []string{"findme"}})
		if err != nil {
			t.Fatalf("SearchRepositories: %v", err)
		}
		if len(got) != 1 || got[0].Name != "findme" {
			t.Fatalf("search = %+v, want only the public findme", got)
		}

		// Description matches too.
		byDesc, err := st.SearchRepositories(ctx, store.RepoSearch{ViewerPK: 0, Terms: []string{"searchable"}})
		if err != nil {
			t.Fatalf("SearchRepositories by desc: %v", err)
		}
		if len(byDesc) != 1 {
			t.Fatalf("description search = %d repos, want 1", len(byDesc))
		}
	})
}

// TestSearchIssuesFTSMultiTerm checks that FTS-based issue search requires all
// terms to appear (implicit AND), so a two-term query does not match a document
// that contains only one of the terms.
func TestSearchIssuesFTSMultiTerm(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "multi"})
		seedIssue(t, st, repo.PK, repo.OwnerPK, "hello world")
		seedIssue(t, st, repo.PK, repo.OwnerPK, "hello universe")

		// Both terms → only the first issue matches.
		got, err := st.SearchIssues(ctx, store.IssueSearch{
			Terms: []string{"hello", "world"}, MatchTitle: true, MatchBody: true,
		})
		if err != nil {
			t.Fatalf("SearchIssues: %v", err)
		}
		if len(got) != 1 || got[0].Title != "hello world" {
			t.Errorf("multi-term search = %+v, want one 'hello world' issue", got)
		}
	})
}

// TestSearchIssuesFTSTriggerUpdate verifies that the FTS5 sync trigger fires on
// title/body updates so the new text is searchable and the old text is not.
func TestSearchIssuesFTSTriggerUpdate(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		repo := seedRepo(t, st, "octocat", &store.RepoRow{Name: "trigup"})
		iss := seedIssue(t, st, repo.PK, repo.OwnerPK, "unique alpha")

		// Before update: "unique" is found.
		before, err := st.SearchIssues(ctx, store.IssueSearch{
			Terms: []string{"unique"}, MatchTitle: true, MatchBody: true,
		})
		if err != nil {
			t.Fatalf("SearchIssues before: %v", err)
		}
		if len(before) != 1 {
			t.Errorf("before update: want 1 result, got %d", len(before))
		}

		// Update title — FTS trigger must reindex.
		iss.Title = "different beta"
		if err := st.WithTx(ctx, func(tx *store.Tx) error {
			return tx.UpdateIssue(ctx, iss)
		}); err != nil {
			t.Fatalf("UpdateIssue: %v", err)
		}

		// "unique" must no longer match.
		after, err := st.SearchIssues(ctx, store.IssueSearch{
			Terms: []string{"unique"}, MatchTitle: true, MatchBody: true,
		})
		if err != nil {
			t.Fatalf("SearchIssues after: %v", err)
		}
		if len(after) != 0 {
			t.Errorf("after update: old term still matches %d issues, want 0", len(after))
		}

		// "different" must now match.
		updated, err := st.SearchIssues(ctx, store.IssueSearch{
			Terms: []string{"different"}, MatchTitle: true, MatchBody: true,
		})
		if err != nil {
			t.Fatalf("SearchIssues new term: %v", err)
		}
		if len(updated) != 1 {
			t.Errorf("after update: new term wants 1 result, got %d", len(updated))
		}
	})
}

// TestVisibleRepoPKs confirms the code-search scope helper lists only the
// repositories a viewer may see among the named owners.
func TestVisibleRepoPKs(t *testing.T) {
	eachDialect(t, func(t *testing.T, st *store.Store) {
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		octocat := seedOwner(t, st, "octocat")
		pub := seedRepoFor(t, st, octocat, &store.RepoRow{Name: "open"})
		priv := seedRepoFor(t, st, octocat, &store.RepoRow{Name: "closed", Private: true})

		anon, err := st.VisibleRepoPKs(ctx, 0, []int64{pub.OwnerPK})
		if err != nil {
			t.Fatalf("VisibleRepoPKs anon: %v", err)
		}
		if len(anon) != 1 || anon[0] != pub.PK {
			t.Fatalf("anon visible = %v, want only public pk %d", anon, pub.PK)
		}

		owner, err := st.VisibleRepoPKs(ctx, pub.OwnerPK, []int64{pub.OwnerPK})
		if err != nil {
			t.Fatalf("VisibleRepoPKs owner: %v", err)
		}
		if len(owner) != 2 {
			t.Fatalf("owner visible = %v, want both (incl private %d)", owner, priv.PK)
		}
	})
}

package domain

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tamnd/githome/store"
)

// socialFixture opens a migrated store and a SocialService over it, with two
// accounts: owner (octocat) and a viewer (hubber).
type socialFixture struct {
	svc      *SocialService
	st       *store.Store
	ownerPK  int64
	viewerPK int64
	ctx      context.Context
}

func newSocialFixture(t *testing.T) *socialFixture {
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
		t.Fatalf("InsertUser owner: %v", err)
	}
	viewer := &store.UserRow{Login: "hubber", Type: "User"}
	if err := st.InsertUser(ctx, viewer); err != nil {
		t.Fatalf("InsertUser viewer: %v", err)
	}
	return &socialFixture{
		svc:      NewSocialService(st),
		st:       st,
		ownerPK:  owner.PK,
		viewerPK: viewer.PK,
		ctx:      ctx,
	}
}

// TestStarredListFiltersPrivateRepos confirms a private repository the viewer
// cannot see is dropped from another user's starred list, while the owner still
// sees it in their own list.
func TestStarredListFiltersPrivateRepos(t *testing.T) {
	fx := newSocialFixture(t)

	pub := &store.RepoRow{OwnerPK: fx.ownerPK, Name: "public", DefaultBranch: "main"}
	if err := fx.st.InsertRepo(fx.ctx, pub); err != nil {
		t.Fatalf("InsertRepo public: %v", err)
	}
	priv := &store.RepoRow{OwnerPK: fx.ownerPK, Name: "secret", DefaultBranch: "main", Private: true}
	if err := fx.st.InsertRepo(fx.ctx, priv); err != nil {
		t.Fatalf("InsertRepo private: %v", err)
	}

	// octocat stars both of their own repositories.
	if err := fx.svc.StarRepo(fx.ctx, fx.ownerPK, "octocat", "public"); err != nil {
		t.Fatalf("star public: %v", err)
	}
	if err := fx.svc.StarRepo(fx.ctx, fx.ownerPK, "octocat", "secret"); err != nil {
		t.Fatalf("star secret: %v", err)
	}

	// hubber, viewing octocat's starred list, sees only the public repository.
	got, err := fx.svc.StarredByLogin(fx.ctx, fx.viewerPK, "octocat")
	if err != nil {
		t.Fatalf("StarredByLogin: %v", err)
	}
	if len(got) != 1 || got[0].Name != "public" {
		t.Fatalf("viewer starred = %v, want [public]", repoNames(got))
	}

	// octocat, viewing their own starred list, sees both.
	mine, err := fx.svc.StarredByActor(fx.ctx, fx.ownerPK)
	if err != nil {
		t.Fatalf("StarredByActor: %v", err)
	}
	if len(mine) != 2 {
		t.Fatalf("owner starred = %v, want both", repoNames(mine))
	}

	// An anonymous viewer (pk 0) sees only the public repository.
	anon, err := fx.svc.StarredByLogin(fx.ctx, 0, "octocat")
	if err != nil {
		t.Fatalf("StarredByLogin anon: %v", err)
	}
	if len(anon) != 1 || anon[0].Name != "public" {
		t.Fatalf("anon starred = %v, want [public]", repoNames(anon))
	}
}

// TestSubscriptionListFiltersPrivateRepos confirms the same visibility rule
// applies to the watched-repositories list.
func TestSubscriptionListFiltersPrivateRepos(t *testing.T) {
	fx := newSocialFixture(t)

	priv := &store.RepoRow{OwnerPK: fx.ownerPK, Name: "secret", DefaultBranch: "main", Private: true}
	if err := fx.st.InsertRepo(fx.ctx, priv); err != nil {
		t.Fatalf("InsertRepo private: %v", err)
	}
	if _, err := fx.svc.SetSubscription(fx.ctx, fx.ownerPK, "octocat", "secret", true, false); err != nil {
		t.Fatalf("SetSubscription: %v", err)
	}

	got, err := fx.svc.SubscriptionsByLogin(fx.ctx, fx.viewerPK, "octocat")
	if err != nil {
		t.Fatalf("SubscriptionsByLogin: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("viewer subscriptions = %v, want none", repoNames(got))
	}
}

// TestFollowSelfRejected confirms following oneself is forbidden, the 422 the
// REST layer renders.
func TestFollowSelfRejected(t *testing.T) {
	fx := newSocialFixture(t)
	if err := fx.svc.Follow(fx.ctx, fx.ownerPK, "octocat"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("self-follow err = %v, want ErrForbidden", err)
	}
}

// TestFollowUnknownTarget confirms following a nonexistent login maps to
// ErrUserNotFound.
func TestFollowUnknownTarget(t *testing.T) {
	fx := newSocialFixture(t)
	if err := fx.svc.Follow(fx.ctx, fx.ownerPK, "ghost"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("follow ghost err = %v, want ErrUserNotFound", err)
	}
}

// TestWatchersExcludeIgnorers confirms a user who set ignored=true is recorded
// as a subscription but does not appear in the watchers list.
func TestWatchersExcludeIgnorers(t *testing.T) {
	fx := newSocialFixture(t)
	repo := &store.RepoRow{OwnerPK: fx.ownerPK, Name: "hello", DefaultBranch: "main"}
	if err := fx.st.InsertRepo(fx.ctx, repo); err != nil {
		t.Fatalf("InsertRepo: %v", err)
	}

	// viewer subscribes, owner ignores.
	if _, err := fx.svc.SetSubscription(fx.ctx, fx.viewerPK, "octocat", "hello", true, false); err != nil {
		t.Fatalf("viewer subscribe: %v", err)
	}
	if _, err := fx.svc.SetSubscription(fx.ctx, fx.ownerPK, "octocat", "hello", false, true); err != nil {
		t.Fatalf("owner ignore: %v", err)
	}

	watchers, err := fx.svc.Watchers(fx.ctx, "octocat", "hello")
	if err != nil {
		t.Fatalf("Watchers: %v", err)
	}
	if len(watchers) != 1 || watchers[0].Login != "hubber" {
		t.Fatalf("watchers = %v, want [hubber]", userLogins(watchers))
	}

	// The ignorer still has a subscription row on record.
	sub, err := fx.svc.Subscription(fx.ctx, fx.ownerPK, "octocat", "hello")
	if err != nil {
		t.Fatalf("Subscription: %v", err)
	}
	if !sub.Ignored || sub.Subscribed {
		t.Fatalf("owner subscription = %+v, want ignored not subscribed", sub)
	}
}

func repoNames(rs []*Repo) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}

func userLogins(us []*User) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.Login
	}
	return out
}

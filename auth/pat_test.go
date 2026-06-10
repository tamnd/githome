package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tamnd/githome/store"
)

func TestCreatePATMintsUsableToken(t *testing.T) {
	fs := newFakeStore()
	userPK := fs.addUser(&store.UserRow{Login: "octocat"})
	svc := NewService(fs, "https://example.test")
	defer svc.Close()

	plain, err := svc.CreatePAT(context.Background(), userPK, "ci runner", []string{"repo", "gist", "bogus"})
	if err != nil {
		t.Fatalf("CreatePAT: %v", err)
	}
	if !strings.HasPrefix(plain, PrefixClassicPAT) {
		t.Fatalf("plaintext = %q, want %s prefix", plain, PrefixClassicPAT)
	}
	if !VerifyChecksum(plain) {
		t.Fatal("minted token fails its own checksum")
	}

	actor, err := svc.Authenticate(context.Background(), "token "+plain)
	if err != nil {
		t.Fatalf("authenticate minted PAT: %v", err)
	}
	if actor.UserLogin != "octocat" {
		t.Errorf("actor login = %q, want octocat", actor.UserLogin)
	}
	// The bogus scope is dropped at mint, repo and gist survive.
	if got := actor.Scopes.Header(); got != "gist, repo" {
		t.Errorf("scopes = %q, want \"gist, repo\"", got)
	}
}

func TestListAndDeletePATs(t *testing.T) {
	fs := newFakeStore()
	userPK := fs.addUser(&store.UserRow{Login: "octocat"})
	otherPK := fs.addUser(&store.UserRow{Login: "hubber"})
	svc := NewService(fs, "https://example.test")
	defer svc.Close()

	ctx := context.Background()
	if _, err := svc.CreatePAT(ctx, userPK, "first", []string{"repo"}); err != nil {
		t.Fatal(err)
	}
	plain, err := svc.CreatePAT(ctx, userPK, "second", []string{"gist"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreatePAT(ctx, otherPK, "not yours", []string{"repo"}); err != nil {
		t.Fatal(err)
	}

	pats, err := svc.ListPATs(ctx, userPK)
	if err != nil {
		t.Fatalf("ListPATs: %v", err)
	}
	if len(pats) != 2 {
		t.Fatalf("ListPATs returned %d tokens, want 2", len(pats))
	}
	// Newest first.
	if pats[0].Note != "second" || pats[1].Note != "first" {
		t.Errorf("order = %q, %q; want second, first", pats[0].Note, pats[1].Note)
	}
	if pats[0].LastEight != plain[len(plain)-8:] {
		t.Errorf("LastEight = %q, want %q", pats[0].LastEight, plain[len(plain)-8:])
	}

	// A user cannot delete someone else's token, and the answer does not say
	// whether the token exists.
	if err := svc.DeletePAT(ctx, otherPK, pats[0].ID); !errors.Is(err, ErrPATNotFound) {
		t.Fatalf("cross-user delete = %v, want ErrPATNotFound", err)
	}

	if err := svc.DeletePAT(ctx, userPK, pats[0].ID); err != nil {
		t.Fatalf("DeletePAT: %v", err)
	}
	left, err := svc.ListPATs(ctx, userPK)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].Note != "first" {
		t.Fatalf("after delete: %+v, want only first", left)
	}

	// The deleted token no longer authenticates.
	if _, err := svc.Authenticate(ctx, "token "+plain); err == nil {
		t.Fatal("deleted token still authenticates")
	}
}

package domain

import (
	"context"
	"errors"
	"testing"

	"github.com/tamnd/githome/store"
)

// TestAuthorizeWriteRoles covers R03-11: write authorization follows the actor's
// effective repository role rather than owner-only. The owner writes (admin), a
// push or admin collaborator writes, a read-only (pull) collaborator does not,
// and a stranger who can see the public repository is forbidden, not 404.
func TestAuthorizeWriteRoles(t *testing.T) {
	svc, st := newFixture(t)
	ctx := context.Background()

	const (
		ownerPK    = int64(10)
		pusherPK   = int64(20)
		adminPK    = int64(21)
		readerPK   = int64(22)
		strangerPK = int64(23)
	)
	// The collaborators must exist as users for RepoPermission's lookups; the
	// repo "hello" (pk 5) is public, so visibility never blocks the check.
	st.users[pusherPK] = &store.UserRow{PK: pusherPK, Login: "pusher", Type: "User"}
	st.users[adminPK] = &store.UserRow{PK: adminPK, Login: "adminer", Type: "User"}
	st.users[readerPK] = &store.UserRow{PK: readerPK, Login: "reader", Type: "User"}
	st.users[strangerPK] = &store.UserRow{PK: strangerPK, Login: "stranger", Type: "User"}
	st.collaborators = map[[2]int64]string{
		{5, pusherPK}: "push",
		{5, adminPK}:  "admin",
		{5, readerPK}: "pull",
	}

	cases := []struct {
		name    string
		actorPK int64
		wantErr error
	}{
		{"owner writes", ownerPK, nil},
		{"push collaborator writes", pusherPK, nil},
		{"admin collaborator writes", adminPK, nil},
		{"read-only collaborator forbidden", readerPK, ErrForbidden},
		{"stranger forbidden on public repo", strangerPK, ErrForbidden},
		{"anonymous forbidden", 0, ErrForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.AuthorizeWrite(ctx, tc.actorPK, "octocat", "hello")
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("AuthorizeWrite: unexpected error %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("AuthorizeWrite error = %v, want %v", err, tc.wantErr)
			}
		})
	}

	// A stranger on a private repository they cannot see stays 404, so the
	// repository's existence never leaks through the write gate.
	if _, err := svc.AuthorizeWrite(ctx, strangerPK, "octocat", "secret"); !errors.Is(err, ErrRepoNotFound) {
		t.Fatalf("private repo write by stranger: error = %v, want ErrRepoNotFound", err)
	}
}

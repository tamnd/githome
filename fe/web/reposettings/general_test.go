package reposettings

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/tamnd/githome/store"
)

// TestGeneralIsTheSettingsRoot shows the bare settings root now serves the
// General page (rename, description, default branch, danger zone) instead of
// bouncing straight to the webhooks, with the settings nav listing both backed
// sections.
func TestGeneralIsTheSettingsRoot(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	resp, body := fx.get(t, "/octocat/hello/settings")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{
		`<h1 class="settings-title">General</h1>`,
		`name="name"`,
		`value="hello"`,
		"Delete this repository",
		`href="/octocat/hello/settings"`,       // General nav link
		`href="/octocat/hello/settings/hooks"`, // Webhooks nav link
	} {
		if !strings.Contains(body, want) {
			t.Errorf("General page missing %q", want)
		}
	}
}

// TestGeneralRenamePersistsAndRedirects shows the main form renames the
// repository through the domain and redirects to the renamed repository's
// settings root, the post-redirect-get a no-JS save lands on.
func TestGeneralRenamePersistsAndRedirects(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	csrf := fx.csrfToken(t, "/octocat/hello/settings")
	resp := fx.post(t, "/octocat/hello/settings", csrf, url.Values{
		"name":        {"hello-world"},
		"description": {"now with a description"},
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/octocat/hello-world/settings" {
		t.Errorf("Location = %q, want /octocat/hello-world/settings", loc)
	}
	row, err := fx.store.RepoByOwnerName(context.Background(), "octocat", "hello-world")
	if err != nil {
		t.Fatalf("renamed repo not found: %v", err)
	}
	if row.Description == nil || *row.Description != "now with a description" {
		t.Errorf("description not saved: %+v", row.Description)
	}
}

// TestGeneralEmptyNameRejected shows a blank name re-renders the form with an
// inline error and changes nothing, never reaching the domain.
func TestGeneralEmptyNameRejected(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	csrf := fx.csrfToken(t, "/octocat/hello/settings")
	resp := fx.post(t, "/octocat/hello/settings", csrf, url.Values{"name": {""}})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200 (re-rendered form)", resp.StatusCode)
	}
	if _, err := fx.store.RepoByOwnerName(context.Background(), "octocat", "hello"); err != nil {
		t.Errorf("repo should be untouched: %v", err)
	}
}

// TestGeneralVisibilityTogglePersists shows the danger zone flips the
// repository to private through the domain and redirects back to the settings
// root.
func TestGeneralVisibilityTogglePersists(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	csrf := fx.csrfToken(t, "/octocat/hello/settings")
	resp := fx.post(t, "/octocat/hello/settings/visibility", csrf, url.Values{"visibility": {"private"}})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", resp.StatusCode)
	}
	row, err := fx.store.RepoByOwnerName(context.Background(), "octocat", "hello")
	if err != nil {
		t.Fatalf("repo not found: %v", err)
	}
	if !row.Private {
		t.Error("repository should be private after the toggle")
	}
}

// TestGeneralDeleteRequiresConfirmation shows a mismatched confirmation
// re-renders the page and leaves the repository intact, while the matching
// full name deletes it and lands on the owner's profile.
func TestGeneralDeleteRequiresConfirmation(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	csrf := fx.csrfToken(t, "/octocat/hello/settings")

	bad := fx.post(t, "/octocat/hello/settings/delete", csrf, url.Values{"confirm": {"not-the-name"}})
	defer func() { _ = bad.Body.Close() }()
	if bad.StatusCode != http.StatusOK {
		t.Fatalf("mismatch status %d, want 200 (re-rendered)", bad.StatusCode)
	}
	if _, err := fx.store.RepoByOwnerName(context.Background(), "octocat", "hello"); err != nil {
		t.Fatalf("repo should survive a bad confirmation: %v", err)
	}

	ok := fx.post(t, "/octocat/hello/settings/delete", csrf, url.Values{"confirm": {"octocat/hello"}})
	defer func() { _ = ok.Body.Close() }()
	if ok.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete status %d, want 303", ok.StatusCode)
	}
	if loc := ok.Header.Get("Location"); loc != "/octocat" {
		t.Errorf("Location = %q, want /octocat", loc)
	}
	if _, err := fx.store.RepoByOwnerName(context.Background(), "octocat", "hello"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("repo should be deleted, got err=%v", err)
	}
}

// TestGeneralNonAdminGets404 shows the General root 404s to a viewer who can
// see a repository but does not administer it, the same 404-not-403 rule the
// rest of the settings surface takes.
func TestGeneralNonAdminGets404(t *testing.T) {
	fx := newFixture(t)
	fx.login(t)
	resp, _ := fx.get(t, "/mallory/widgets/settings")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", resp.StatusCode)
	}
}

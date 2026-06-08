package route

import "testing"

func TestAccountSettingsURLs(t *testing.T) {
	if got := AccountSettings(); got != "/settings" {
		t.Errorf("AccountSettings() = %q, want /settings", got)
	}
	if got := Appearance(); got != "/settings/appearance" {
		t.Errorf("Appearance() = %q, want /settings/appearance", got)
	}
}

func TestRepoSettingsURLs(t *testing.T) {
	const owner, name = "octocat", "hello"
	cases := []struct {
		got  string
		want string
	}{
		{RepoSettings(owner, name), "/octocat/hello/settings"},
		{RepoHooks(owner, name), "/octocat/hello/settings/hooks"},
		{RepoHookNew(owner, name), "/octocat/hello/settings/hooks/new"},
		{RepoHook(owner, name, 7), "/octocat/hello/settings/hooks/7"},
		{RepoHookDelete(owner, name, 7), "/octocat/hello/settings/hooks/7/delete"},
		{RepoHookDelivery(owner, name, 7, 42), "/octocat/hello/settings/hooks/7/deliveries/42"},
		{RepoHookRedeliver(owner, name, 7, 42), "/octocat/hello/settings/hooks/7/deliveries/42/redeliver"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

// TestRepoSettingsEscapesOwnerAndName guards the owner and name being escaped
// the same way the rest of the repo URLs are, so a settings link never breaks
// out of its path segment.
func TestRepoSettingsEscapesOwnerAndName(t *testing.T) {
	if got := RepoHooks("a b", "c/d"); got != "/a%20b/c%2Fd/settings/hooks" {
		t.Errorf("RepoHooks(a b, c/d) = %q", got)
	}
}

// TestStafftoolsAndAdminReserved pins the two site-administration roots into the
// reserved set so a future admin panel can mount under them and no account can
// take the login. The check is case-insensitive like every other reserved name.
func TestStafftoolsAndAdminReserved(t *testing.T) {
	for _, name := range []string{"stafftools", "admin", "Stafftools", "ADMIN"} {
		if !IsReservedTop(name) {
			t.Errorf("IsReservedTop(%q) = false, want true", name)
		}
	}
}

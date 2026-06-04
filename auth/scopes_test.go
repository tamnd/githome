package auth

import (
	"reflect"
	"testing"
)

func TestParseScopeParam(t *testing.T) {
	cases := []struct {
		in   string
		want Scopes
	}{
		{"", Scopes{}},
		{"repo", Scopes{"repo"}},
		{"repo gist", Scopes{"repo", "gist"}},
		{"repo,gist", Scopes{"repo", "gist"}},
		{"repo+gist", Scopes{"repo", "gist"}},
		{"repo, gist , workflow", Scopes{"repo", "gist", "workflow"}},
		{"  repo   gist  ", Scopes{"repo", "gist"}},
	}
	for _, c := range cases {
		got := ParseScopeParam(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseScopeParam(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNormalizeScopes(t *testing.T) {
	cases := []struct {
		name string
		in   Scopes
		want Scopes
	}{
		{"sorts and dedupes", Scopes{"gist", "repo", "gist"}, Scopes{"gist", "repo"}},
		{"drops unknown", Scopes{"repo", "not_a_scope"}, Scopes{"repo"}},
		{"drops child implied by parent", Scopes{"repo", "public_repo"}, Scopes{"repo"}},
		{"drops read:org under admin:org", Scopes{"admin:org", "read:org", "write:org"}, Scopes{"admin:org"}},
		{"keeps unrelated", Scopes{"repo", "workflow"}, Scopes{"repo", "workflow"}},
		{"transitive: user implies user:email", Scopes{"user", "user:email"}, Scopes{"user"}},
		{"empty stays empty", Scopes{}, Scopes{}},
		{"all unknown collapses to empty", Scopes{"bogus", "nope"}, Scopes{}},
	}
	for _, c := range cases {
		got := NormalizeScopes(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: NormalizeScopes(%v) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

func TestScopesHas(t *testing.T) {
	ss := NormalizeScopes(Scopes{"repo", "admin:org"})
	yes := []Scope{"repo", "public_repo", "repo:status", "admin:org", "write:org", "read:org"}
	for _, sc := range yes {
		if !ss.Has(sc) {
			t.Errorf("Has(%q) = false, want true (parents should expand)", sc)
		}
	}
	no := []Scope{"gist", "workflow", "delete_repo"}
	for _, sc := range no {
		if ss.Has(sc) {
			t.Errorf("Has(%q) = true, want false", sc)
		}
	}
}

func TestScopesHeader(t *testing.T) {
	ss := NormalizeScopes(Scopes{"repo", "gist"})
	if got, want := ss.Header(), "gist, repo"; got != want {
		t.Errorf("Header() = %q, want %q (comma-space, sorted)", got, want)
	}
	if got := (Scopes{}).Header(); got != "" {
		t.Errorf("empty Header() = %q, want empty", got)
	}
}

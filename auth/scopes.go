package auth

import (
	"slices"
	"strings"
)

// Scope is a classic OAuth/PAT scope string, e.g. "repo" or "read:org".
type Scope string

// Scopes is a set of classic scopes, kept sorted and deduplicated after
// NormalizeScopes so the X-OAuth-Scopes header round-trips byte-for-byte with
// GitHub for the common cases.
type Scopes []Scope

// scopeImplies maps a parent scope to the children it grants. Normalization
// drops a child a held parent already implies, exactly like GitHub.
var scopeImplies = map[Scope][]Scope{
	"repo":             {"repo:status", "repo_deployment", "public_repo", "repo:invite", "security_events"},
	"admin:org":        {"write:org", "read:org"},
	"write:org":        {"read:org"},
	"user":             {"read:user", "user:email", "user:follow"},
	"admin:repo_hook":  {"write:repo_hook", "read:repo_hook"},
	"write:repo_hook":  {"read:repo_hook"},
	"admin:public_key": {"write:public_key", "read:public_key"},
	"write:public_key": {"read:public_key"},
	"admin:gpg_key":    {"write:gpg_key", "read:gpg_key"},
	"write:gpg_key":    {"read:gpg_key"},
	"write:discussion": {"read:discussion"},
	"write:packages":   {"read:packages"},
	"project":          {"read:project"},
}

// knownScopes is the validation set; unknown scopes are dropped at mint time.
var knownScopes = buildKnownScopeSet()

func buildKnownScopeSet() map[Scope]bool {
	set := map[Scope]bool{}
	add := func(s Scope) { set[s] = true }
	// Every parent and every child it implies is valid.
	for parent, children := range scopeImplies {
		add(parent)
		for _, c := range children {
			add(c)
		}
	}
	// Standalone scopes with no parent/child relationship.
	for _, s := range []Scope{
		"repo", "workflow", "write:packages", "read:packages", "delete:packages",
		"admin:org", "admin:public_key", "admin:repo_hook", "admin:org_hook",
		"gist", "notifications", "user", "delete_repo", "write:discussion",
		"admin:enterprise", "audit_log", "codespace", "copilot",
	} {
		add(s)
	}
	return set
}

// allImplied returns the transitive closure of the children parent grants.
func allImplied(parent Scope) []Scope {
	var out []Scope
	seen := map[Scope]bool{}
	var walk func(Scope)
	walk = func(s Scope) {
		for _, child := range scopeImplies[s] {
			if !seen[child] {
				seen[child] = true
				out = append(out, child)
				walk(child)
			}
		}
	}
	walk(parent)
	return out
}

// NormalizeScopes keeps only known scopes, drops any implied by a held parent,
// then dedupes and sorts.
func NormalizeScopes(in Scopes) Scopes {
	held := map[Scope]bool{}
	for _, sc := range in {
		if knownScopes[sc] {
			held[sc] = true
		}
	}
	for parent := range held {
		for _, child := range allImplied(parent) {
			if parent != child {
				delete(held, child)
			}
		}
	}
	out := make(Scopes, 0, len(held))
	for sc := range held {
		out = append(out, sc)
	}
	slices.Sort(out)
	return out
}

// Has reports whether the set effectively carries sc, expanding parents.
func (ss Scopes) Has(sc Scope) bool {
	for _, held := range ss {
		if held == sc {
			return true
		}
		if slices.Contains(allImplied(held), sc) {
			return true
		}
	}
	return false
}

// Header renders the set as GitHub's comma-space separated list, e.g.
// "gist, repo".
func (ss Scopes) Header() string {
	parts := make([]string, len(ss))
	for i, sc := range ss {
		parts[i] = string(sc)
	}
	return strings.Join(parts, ", ")
}

// Strings returns the scopes as a plain string slice, preserving order.
func (ss Scopes) Strings() []string {
	out := make([]string, len(ss))
	for i, sc := range ss {
		out[i] = string(sc)
	}
	return out
}

// ParseScopeParam splits a space- or comma-delimited scope parameter (the OAuth
// "scope" field accepts both) into a Scopes set.
func ParseScopeParam(s string) Scopes {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' || r == '+' })
	out := make(Scopes, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, Scope(f))
		}
	}
	return out
}

package etag

import (
	"strings"
	"testing"
)

func TestWeakIsStableAndWeak(t *testing.T) {
	a := Weak([]byte(`{"login":"octocat"}`))
	b := Weak([]byte(`{"login":"octocat"}`))
	if a != b {
		t.Fatalf("same body produced different tags: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, `W/"`) || !strings.HasSuffix(a, `"`) {
		t.Fatalf("tag is not a quoted weak validator: %q", a)
	}
	if Weak([]byte(`{"login":"hubot"}`)) == a {
		t.Fatalf("different bodies must produce different tags")
	}
}

func TestVersionVariesWithMarkers(t *testing.T) {
	base := Version("Repository", 1, 100, 1)
	if Version("Repository", 1, 100, 1) != base {
		t.Fatalf("same inputs must produce the same version tag")
	}
	if Version("Repository", 1, 101, 1) == base {
		t.Fatalf("a changed marker must change the tag")
	}
	if Version("Repository", 2, 100, 1) == base {
		t.Fatalf("a changed id must change the tag")
	}
	if Version("Issue", 1, 100, 1) == base {
		t.Fatalf("a changed seed must change the tag")
	}
}

func TestMatch(t *testing.T) {
	tag := Weak([]byte("body"))
	cases := []struct {
		ifNoneMatch string
		want        bool
	}{
		{"", false},
		{tag, true},
		{strings.TrimPrefix(tag, "W/"), true}, // strong form of the same opaque tag
		{"*", true},
		{`"other", ` + tag, true},
		{`"other"`, false},
	}
	for _, c := range cases {
		if got := Match(c.ifNoneMatch, tag); got != c.want {
			t.Errorf("Match(%q, tag)=%v want %v", c.ifNoneMatch, got, c.want)
		}
	}
}

package markup

import (
	"context"
	"html/template"
	"strings"
	"testing"
)

func testRenderer(t *testing.T) *Renderer {
	t.Helper()
	return New(Config{BaseURL: "http://localhost"})
}

func TestFragCacheEvictsByBytes(t *testing.T) {
	c := newFragCache(1 << 10)
	c.put(highlightKey([]byte("a"), ""), template.HTML("a"), 600)
	c.put(highlightKey([]byte("b"), ""), template.HTML("b"), 600)
	if _, ok := c.get(highlightKey([]byte("a"), "")); ok {
		t.Fatal("cold entry should have been evicted")
	}
	if _, ok := c.get(highlightKey([]byte("b"), "")); !ok {
		t.Fatal("warm entry should survive")
	}
	c.put(highlightKey([]byte("huge"), ""), template.HTML("x"), fragCacheMaxEntryBytes+1)
	if _, ok := c.get(highlightKey([]byte("huge"), "")); ok {
		t.Fatal("over-cap entry must not be cached")
	}
}

func TestRenderCommentServedFromCache(t *testing.T) {
	r := testRenderer(t)
	repo := &RepoRef{Owner: "octo", Name: "hello", ID: 7}
	src := "**bold** and a [link](https://example.com)"

	first := r.RenderComment(context.Background(), repo, src)
	if r.frags.len() != 1 {
		t.Fatalf("after first render: %d entries, want 1", r.frags.len())
	}
	hitsBefore := r.frags.hits
	second := r.RenderComment(context.Background(), repo, src)
	if second != first {
		t.Fatal("cached render must be byte-identical")
	}
	if r.frags.hits != hitsBefore+1 {
		t.Fatalf("second render should hit the cache: hits=%d want %d", r.frags.hits, hitsBefore+1)
	}

	// A different repo identity is a different key: relative links and refs
	// resolve against the repo, so its identity is an output input.
	other := &RepoRef{Owner: "octo", Name: "world", ID: 8}
	_ = r.RenderComment(context.Background(), other, src)
	if r.frags.len() != 2 {
		t.Fatalf("distinct repo should add an entry: %d entries, want 2", r.frags.len())
	}
}

func TestRenderWithResolveBypassesCache(t *testing.T) {
	r := testRenderer(t)
	rc := RenderContext{
		Mode: ModeComment,
		Repo: &RepoRef{Owner: "octo", Name: "hello", ID: 7},
		Resolve: func(_ context.Context, _ RefKind, _ string) (string, bool) {
			return "", false
		},
	}
	if _, err := r.Render(context.Background(), []byte("see #1"), rc); err != nil {
		t.Fatal(err)
	}
	if r.frags.len() != 0 {
		t.Fatalf("a render with a Resolve closure must not be cached: %d entries", r.frags.len())
	}
}

func TestHighlightLinesServedFromCache(t *testing.T) {
	r := testRenderer(t)
	code := []byte("package main\n\nfunc main() {}\n")

	first, err := r.HighlightLines(code, "go")
	if err != nil {
		t.Fatal(err)
	}
	hitsBefore := r.frags.hits
	second, err := r.HighlightLines(code, "go")
	if err != nil {
		t.Fatal(err)
	}
	if r.frags.hits != hitsBefore+1 {
		t.Fatalf("second highlight should hit the cache: hits=%d want %d", r.frags.hits, hitsBefore+1)
	}
	if len(first) != len(second) {
		t.Fatalf("line counts differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("line %d differs between cached and fresh render", i)
		}
	}

	// The grammar is a key input: the same bytes as plain text is a new entry.
	before := r.frags.len()
	if _, err := r.HighlightLines(code, ""); err != nil {
		t.Fatal(err)
	}
	if r.frags.len() != before+1 {
		t.Fatalf("distinct lang should add an entry: %d entries, want %d", r.frags.len(), before+1)
	}
	if strings.Join(htmlStrings(first), "\n") == "" {
		t.Fatal("highlighted output should not be empty")
	}
}

package domain

import (
	"strings"
	"testing"
)

func corpusOf(bytes int) *repoCorpus {
	return &repoCorpus{
		docs:  []corpusDoc{{path: "f.go", lowerText: strings.Repeat("x", bytes)}},
		bytes: bytes,
	}
}

func TestCorpusCachePutGetEvict(t *testing.T) {
	c := newCorpusCache(1 << 10)

	warm := corpusOf(300)
	c.put(corpusKey(1, "aaa"), warm)
	if got, ok := c.get(corpusKey(1, "aaa")); !ok || got != warm {
		t.Fatal("warm corpus not served back")
	}
	if _, ok := c.get(corpusKey(1, "bbb")); ok {
		t.Fatal("a different head sha must miss")
	}
	if _, ok := c.get(corpusKey(2, "aaa")); ok {
		t.Fatal("a different repo must miss")
	}

	// Filling past the byte budget evicts the least recently used entry. The
	// warm entry was touched by the get above, so the second insert goes first.
	c.put(corpusKey(3, "ccc"), corpusOf(300))
	if _, ok := c.get(corpusKey(1, "aaa")); !ok {
		t.Fatal("warm entry gone before the budget filled")
	}
	c.put(corpusKey(4, "ddd"), corpusOf(500))
	if _, ok := c.get(corpusKey(3, "ccc")); ok {
		t.Fatal("least recently used corpus should have been evicted")
	}
	if _, ok := c.get(corpusKey(1, "aaa")); !ok {
		t.Fatal("recently used corpus must survive the eviction")
	}
}

func TestCorpusCacheRejectsOversizedEntry(t *testing.T) {
	c := newCorpusCache(corpusCacheMaxBytes)
	c.put(corpusKey(1, "aaa"), corpusOf(corpusMaxEntryBytes+1))
	if _, ok := c.get(corpusKey(1, "aaa")); ok {
		t.Fatal("an over-cap corpus must not enter the cache")
	}
}

func TestMatchDoc(t *testing.T) {
	d := corpusDoc{
		lowerPath: "pkg/server/handler.go",
		lowerText: "package server\n\nfunc serve() {}\n",
	}
	cases := []struct {
		terms []string
		want  bool
	}{
		{nil, true},                          // no terms lists everything
		{[]string{"handler"}, true},          // path match
		{[]string{"serve()"}, true},          // content match
		{[]string{"handler", "serve"}, true}, // every term must match, mixed sources
		{[]string{"handler", "nothere"}, false},
		{[]string{"absent"}, false},
	}
	for _, tc := range cases {
		if got := matchDoc(d, tc.terms); got != tc.want {
			t.Errorf("matchDoc(%v) = %v, want %v", tc.terms, got, tc.want)
		}
	}

	// A binary doc has no text but still matches by path.
	bin := corpusDoc{lowerPath: "assets/logo.png"}
	if !matchDoc(bin, []string{"logo"}) {
		t.Error("binary doc must match by path")
	}
	if matchDoc(bin, []string{"png", "pixels"}) {
		t.Error("binary doc must not match content terms")
	}
}

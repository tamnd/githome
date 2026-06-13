package render

import (
	"html/template"
	"strings"
	"testing"
)

// TestOcticonDecorativeCaches checks the memoized decorative path: two calls for
// the same (name, size) return byte-identical markup, the cache holds an entry
// for the key, and a hand-seeded entry is served verbatim — proof the second
// call reads the cache rather than re-rendering.
func TestOcticonDecorativeCaches(t *testing.T) {
	octiconCache.Delete("repo\x0016")

	first, err := octicon("repo")
	if err != nil {
		t.Fatalf("octicon: %v", err)
	}
	if !strings.Contains(string(first), "octicon-repo") {
		t.Fatalf("octicon did not render the repo glyph: %q", first)
	}

	second, err := octicon("repo")
	if err != nil {
		t.Fatalf("octicon (cached): %v", err)
	}
	if second != first {
		t.Errorf("cached octicon differs from first render:\n%q\n%q", first, second)
	}

	if _, ok := octiconCache.Load("repo\x0016"); !ok {
		t.Errorf("decorative octicon was not cached under its (name, size) key")
	}

	// A poisoned cache entry is returned verbatim, confirming the lookup short
	// circuits the render for the unlabeled path.
	octiconCache.Store("repo\x0016", sentinelHTML)
	if got, _ := octicon("repo"); string(got) != string(sentinelHTML) {
		t.Errorf("octicon ignored the cache: got %q", got)
	}
	octiconCache.Delete("repo\x0016")
}

// TestOcticonLabeledSkipsCache checks that a labeled icon never reads or writes
// the cache: it carries the aria-label and title every time, and a poisoned key
// for its (name, size) is ignored because the labeled path does not consult it.
func TestOcticonLabeledSkipsCache(t *testing.T) {
	octiconCache.Store("repo\x0016", sentinelHTML)
	defer octiconCache.Delete("repo\x0016")

	got, err := octicon("repo", "Repository")
	if err != nil {
		t.Fatalf("octicon labeled: %v", err)
	}
	if string(got) == string(sentinelHTML) {
		t.Fatalf("labeled octicon wrongly read the decorative cache")
	}
	if !strings.Contains(string(got), `aria-label="Repository"`) {
		t.Errorf("labeled octicon missing aria-label: %q", got)
	}
	if !strings.Contains(string(got), "<title>Repository</title>") {
		t.Errorf("labeled octicon missing title: %q", got)
	}
}

var sentinelHTML = template.HTML("<!--cache-sentinel-->")

package markup

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// forbidden is the set of patterns that must never appear in rendered output, the
// sanitizer's job stated as assertions. Each is the residue of an injection the
// bluemonday allowlist (sanitize.go) is built to strip. The XSS corpus under
// testdata/xss feeds every file through ModeComment (the most permissive surface,
// since it runs the full extension set) and asserts none of these survive.
// The element patterns match a real tag opening; escaped text reads &lt;script and
// so cannot false-positive. The attribute and scheme patterns are anchored to the
// interior of a tag (<...attr), because the same bytes appearing in escaped text
// content (a code sample that mentions onerror= or javascript:) are inert and must
// not fail the test.
var forbidden = []*regexp.Regexp{
	regexp.MustCompile(`(?i)<script`),
	regexp.MustCompile(`(?i)<iframe`),
	regexp.MustCompile(`(?i)<object`),
	regexp.MustCompile(`(?i)<embed`),
	regexp.MustCompile(`(?i)<form`),
	regexp.MustCompile(`(?i)<style`),
	regexp.MustCompile(`(?i)<[^>]*\son\w+\s*=`),              // event handler attribute inside a tag
	regexp.MustCompile(`(?i)<[^>]*\sstyle\s*=`),              // inline style attribute inside a tag
	regexp.MustCompile(`(?i)<[^>]*(?:javascript|vbscript):`), // script URL scheme inside a tag
	regexp.MustCompile(`(?i)<[^>]*data:text/html`),           // html data URL inside a tag
}

func TestXSSCorpus(t *testing.T) {
	r := newTestRenderer(t)
	dir := filepath.Join("testdata", "xss")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read xss corpus: %v", err)
	}
	var ran int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		ran++
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			out, err := r.Render(context.Background(), src, RenderContext{Mode: ModeComment})
			if err != nil {
				t.Fatalf("render %s: %v", name, err)
			}
			for _, pat := range forbidden {
				if pat.MatchString(string(out)) {
					t.Errorf("%s: forbidden pattern %s survived sanitize:\n%s", name, pat, out)
				}
			}
		})
	}
	if ran == 0 {
		t.Fatal("xss corpus is empty; expected attack vectors under testdata/xss")
	}
}

// TestInlineXSSVectors keeps a handful of vectors inline as a fast smoke test that
// does not depend on the corpus files, so a broken sanitizer fails loudly even if
// the corpus directory is ever emptied.
func TestInlineXSSVectors(t *testing.T) {
	r := newTestRenderer(t)
	vectors := []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror="alert(1)">`,
		`[click](javascript:alert(1))`,
		`<a href="javascript:alert(1)">x</a>`,
		`<details open ontoggle="alert(1)">x</details>`,
		`<p style="background:url(javascript:alert(1))">x</p>`,
		`[x](vbscript:msgbox(1))`,
		`<svg/onload=alert(1)>`,
	}
	for _, v := range vectors {
		out, err := r.Render(context.Background(), []byte(v), RenderContext{Mode: ModeComment})
		if err != nil {
			t.Fatalf("render %q: %v", v, err)
		}
		for _, pat := range forbidden {
			if pat.MatchString(string(out)) {
				t.Errorf("vector %q left forbidden %s in:\n%s", v, pat, out)
			}
		}
	}
}

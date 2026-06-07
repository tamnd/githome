package render

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/tamnd/githome/fe/assets"
)

// This is the token-coverage check the icon registry comment promises: it walks
// the embedded template tree, pulls every {{octicon "name" ...}} reference out of
// the markup, and asserts each name is registered in assets.Icons. Without it a
// template can reference an unregistered icon and the page still renders, only
// with a dashed-box placeholder in place of the glyph, which is exactly how the
// F3 issues surface shipped a row of placeholders that nobody caught until F4.
// Wiring the check here, against the same embedded FS the engine parses, turns a
// missing icon into a failing build rather than a silent visual gap.

// octiconRef matches an octicon helper call and captures the icon name. The name
// is a single- or double-quoted token; both forms appear in the templates.
var octiconRef = regexp.MustCompile(`octicon\s+["']([a-z0-9-]+)["']`)

func TestEveryTemplateOcticonIsRegistered(t *testing.T) {
	used := map[string][]string{} // icon name -> template paths that reference it

	err := fs.WalkDir(templatesFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		body, err := fs.ReadFile(templatesFS, path)
		if err != nil {
			return err
		}
		for _, m := range octiconRef.FindAllStringSubmatch(string(body), -1) {
			name := m[1]
			used[name] = append(used[name], path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking templates: %v", err)
	}
	if len(used) == 0 {
		t.Fatal("found no octicon references in any template, the regexp or the embed is wrong")
	}

	for name, where := range used {
		if _, ok := assets.Icons[name]; !ok {
			t.Errorf("octicon %q is used in %s but is not registered in assets.Icons (it would render as a placeholder)",
				name, strings.Join(where, ", "))
		}
	}
}

package markup

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update regenerates the golden .html files from the .md inputs. Run
// `go test ./markup -run TestRenderingOracle -update` after a deliberate change to
// the renderer, then read the diff to confirm the change is what you intended.
var update = flag.Bool("update", false, "regenerate the golden corpus output")

// TestRenderingOracle is the byte-exact oracle: each testdata/corpus/NAME.md renders
// to NAME.html and must match. It locks the whole pipeline (parse, transform, render,
// sanitize, post-process) so an accidental change to any stage shows up as a diff in
// review rather than as a silent shift in what every page renders. The corpus is
// rendered in ModeComment with a resolver that links a fixed set of references, so the
// extension output is part of the locked bytes.
func TestRenderingOracle(t *testing.T) {
	r := newTestRenderer(t)
	rc := RenderContext{
		Mode: ModeComment,
		Repo: &RepoRef{Owner: "octo", Name: "demo"},
		Resolve: func(_ context.Context, kind RefKind, raw string) (string, bool) {
			switch {
			case kind == RefMention && raw == "octocat":
				return "/octocat", true
			case kind == RefIssue && raw == "1":
				return "/octo/demo/issues/1", true
			}
			return "", false
		},
	}
	dir := filepath.Join("testdata", "corpus")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var ran int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		ran++
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			srcPath := filepath.Join(dir, name)
			goldPath := strings.TrimSuffix(srcPath, ".md") + ".html"
			src, err := os.ReadFile(srcPath)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			out, err := r.Render(context.Background(), src, rc)
			if err != nil {
				t.Fatalf("render %s: %v", name, err)
			}
			got := string(out)
			if *update {
				if err := os.WriteFile(goldPath, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden %s: %v", goldPath, err)
				}
				return
			}
			wantBytes, err := os.ReadFile(goldPath)
			if err != nil {
				t.Fatalf("read golden %s (run with -update to create): %v", goldPath, err)
			}
			if got != string(wantBytes) {
				t.Errorf("%s output drifted from golden.\n--- got ---\n%s\n--- want ---\n%s", name, got, wantBytes)
			}
		})
	}
	if ran == 0 {
		t.Fatal("rendering corpus is empty; expected sample documents under testdata/corpus")
	}
}

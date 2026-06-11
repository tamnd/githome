package markup

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// newTestRenderer builds a Renderer with a fixed host and a camo proxy configured,
// so the host-relative and camo behaviors are exercised. The logger is discarded so
// the degraded-path Info/Warn lines do not clutter test output.
func newTestRenderer(t *testing.T) *Renderer {
	t.Helper()
	return New(Config{
		BaseURL:     "https://githome.test",
		CamoSecret:  []byte("test-camo-secret"),
		CamoBaseURL: "https://camo.githome.test",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// render is a tiny helper that renders src under rc and fails the test on the error
// the pipeline only returns for an internal fault.
func render(t *testing.T, r *Renderer, src string, rc RenderContext) string {
	t.Helper()
	out, err := r.Render(context.Background(), []byte(src), rc)
	if err != nil {
		t.Fatalf("Render(%q): %v", src, err)
	}
	return string(out)
}

func TestCommonMarkFloor(t *testing.T) {
	r := newTestRenderer(t)
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"emphasis", "*hi* and **bold**", "<em>hi</em> and <strong>bold</strong>"},
		{"inline code", "use `go test`", "<code>go test</code>"},
		{"unordered list", "- a\n- b", "<li>a</li>"},
		{"ordered list", "1. a\n2. b", "<ol>"},
		{"blockquote", "> quoted", "<blockquote>"},
		{"thematic break", "a\n\n---\n\nb", "<hr"},
		{"link", "[t](https://example.com)", `href="https://example.com"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := render(t, r, tc.src, RenderContext{Mode: ModeComment})
			if !strings.Contains(got, tc.want) {
				t.Errorf("rendered %q\n got: %s\nwant substring: %s", tc.src, got, tc.want)
			}
		})
	}
}

func TestGFMTables(t *testing.T) {
	r := newTestRenderer(t)
	src := "| a | b |\n|:--|--:|\n| 1 | 2 |\n"
	got := render(t, r, src, RenderContext{Mode: ModeComment})
	for _, want := range []string{"<table>", "<th", `align="left"`, `align="right"`, "<td"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q in:\n%s", want, got)
		}
	}
}

func TestHeadingAnchor(t *testing.T) {
	r := newTestRenderer(t)
	got := render(t, r, "# Hello World", RenderContext{Mode: ModeComment})
	if !strings.Contains(got, `id="user-content-hello-world"`) {
		t.Errorf("heading id missing user-content prefix:\n%s", got)
	}
	if !strings.Contains(got, `class="anchor"`) || !strings.Contains(got, `href="#user-content-hello-world"`) {
		t.Errorf("clickable anchor missing:\n%s", got)
	}
	// The anchor's visible body is the link octicon, not a CSS pseudo-element.
	if !strings.Contains(got, `<svg class="octicon octicon-link"`) || !strings.Contains(got, linkIconPath) {
		t.Errorf("anchor is missing the link octicon:\n%s", got)
	}
}

func TestDuplicateHeadingSlugs(t *testing.T) {
	r := newTestRenderer(t)
	got := render(t, r, "# Title\n\n## Title\n\n### Title", RenderContext{Mode: ModeComment})
	for _, want := range []string{`id="user-content-title"`, `id="user-content-title-1"`, `id="user-content-title-2"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing stable duplicate slug %q in:\n%s", want, got)
		}
	}
}

func TestAlertBlockquote(t *testing.T) {
	r := newTestRenderer(t)
	src := "> [!WARNING]\n> Be careful here.\n"
	got := render(t, r, src, RenderContext{Mode: ModeComment})
	for _, want := range []string{
		`class="markdown-alert markdown-alert-warning"`,
		`data-octicon="alert"`,
		"Be careful here.",
		// The post-process stage turns the data-octicon name into the inline
		// icon the title leads with.
		`<p class="markdown-alert-title"><svg class="octicon"`,
		alertIconPaths["alert"],
	} {
		if !strings.Contains(got, want) {
			t.Errorf("alert missing %q in:\n%s", want, got)
		}
	}
	// The marker line itself must not survive as literal text.
	if strings.Contains(got, "[!WARNING]") {
		t.Errorf("alert marker leaked into output:\n%s", got)
	}
}

func TestTaskList(t *testing.T) {
	r := newTestRenderer(t)
	src := "- [ ] todo\n- [x] done\n"
	got := render(t, r, src, RenderContext{Mode: ModeComment})
	for _, want := range []string{
		`class="contains-task-list"`,
		`task-list-item`,
		`type="checkbox"`,
		`disabled`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("task list missing %q in:\n%s", want, got)
		}
	}
}

func TestFencedCodeHighlight(t *testing.T) {
	r := newTestRenderer(t)
	src := "```go\nfunc main() {}\n```\n"
	got := render(t, r, src, RenderContext{Mode: ModeComment})
	if !strings.Contains(got, `class="highlight highlight-source-go"`) {
		t.Errorf("highlight wrapper missing:\n%s", got)
	}
	if !strings.Contains(got, `class="pl-k"`) {
		t.Errorf("expected a keyword span (pl-k) for func in:\n%s", got)
	}
}

func TestUnknownLanguageDegradesToPlain(t *testing.T) {
	r := newTestRenderer(t)
	src := "```nosuchlang\nplain text here\n```\n"
	got := render(t, r, src, RenderContext{Mode: ModeComment})
	if !strings.Contains(got, "plain text here") {
		t.Errorf("unknown language must still show escaped text:\n%s", got)
	}
	if strings.Contains(got, "pl-") {
		t.Errorf("unknown language must not produce token spans:\n%s", got)
	}
}

func TestMath(t *testing.T) {
	r := newTestRenderer(t)
	inline := render(t, r, "mass is $E = mc^2$ today", RenderContext{Mode: ModeComment})
	if !strings.Contains(inline, `class="math math-inline"`) {
		t.Errorf("inline math missing:\n%s", inline)
	}
	display := render(t, r, "$$\\int x$$", RenderContext{Mode: ModeComment})
	if !strings.Contains(display, `class="math math-display"`) {
		t.Errorf("display math missing:\n%s", display)
	}
	fenced := render(t, r, "```math\n\\frac{1}{2}\n```\n", RenderContext{Mode: ModeComment})
	if !strings.Contains(fenced, `class="math math-display"`) {
		t.Errorf("fenced math missing:\n%s", fenced)
	}
}

func TestMermaidDiagram(t *testing.T) {
	r := newTestRenderer(t)
	got := render(t, r, "```mermaid\ngraph TD;A-->B;\n```\n", RenderContext{Mode: ModeComment})
	if !strings.Contains(got, `class="render-diagram"`) || !strings.Contains(got, `data-diagram-type="mermaid"`) {
		t.Errorf("diagram island missing:\n%s", got)
	}
}

func TestMentionResolves(t *testing.T) {
	r := newTestRenderer(t)
	rc := RenderContext{
		Mode: ModeComment,
		Resolve: func(_ context.Context, kind RefKind, raw string) (string, bool) {
			if kind == RefMention && raw == "alice" {
				return "/alice", true
			}
			return "", false
		},
	}
	got := render(t, r, "hey @alice and @ghost", rc)
	if !strings.Contains(got, `<a href="/alice" class="user-mention">@alice</a>`) {
		t.Errorf("resolved mention missing:\n%s", got)
	}
	if strings.Contains(got, `>@ghost<`) && strings.Contains(got, "ghost</a>") {
		t.Errorf("unresolved mention should stay literal, not link:\n%s", got)
	}
	if !strings.Contains(got, "@ghost") {
		t.Errorf("unresolved mention text lost:\n%s", got)
	}
}

func TestMentionWithoutResolverStaysLiteral(t *testing.T) {
	r := newTestRenderer(t)
	got := render(t, r, "ping @alice", RenderContext{Mode: ModePlain})
	if strings.Contains(got, "user-mention") {
		t.Errorf("no resolver should mean no mention link:\n%s", got)
	}
	if !strings.Contains(got, "@alice") {
		t.Errorf("mention text lost:\n%s", got)
	}
}

func TestIssueRefResolves(t *testing.T) {
	r := newTestRenderer(t)
	rc := RenderContext{
		Mode: ModeComment,
		Resolve: func(_ context.Context, kind RefKind, raw string) (string, bool) {
			if kind == RefIssue && raw == "42" {
				return "/o/r/issues/42", true
			}
			return "", false
		},
	}
	got := render(t, r, "fixes #42", rc)
	if !strings.Contains(got, `<a href="/o/r/issues/42" class="issue-link">#42</a>`) {
		t.Errorf("resolved issue ref missing:\n%s", got)
	}
}

func TestRelativeLinkRewriteInFileMode(t *testing.T) {
	r := newTestRenderer(t)
	rc := RenderContext{
		Mode: ModeFile,
		Repo: &RepoRef{Owner: "octo", Name: "demo"},
		Ref:  "main",
		Path: "docs/guide.md",
	}
	got := render(t, r, "see [setup](./setup.md) and ![logo](../img/logo.png)", rc)
	if !strings.Contains(got, `href="/octo/demo/blob/main/docs/setup.md"`) {
		t.Errorf("relative link not rewritten to blob URL:\n%s", got)
	}
	if !strings.Contains(got, `src="/octo/demo/raw/main/img/logo.png"`) {
		t.Errorf("relative image not rewritten to raw URL:\n%s", got)
	}
}

func TestRelativeLinkNotRewrittenInCommentMode(t *testing.T) {
	r := newTestRenderer(t)
	got := render(t, r, "[x](./y.md)", RenderContext{Mode: ModeComment})
	if strings.Contains(got, "/blob/") {
		t.Errorf("comment mode must not rewrite relative links:\n%s", got)
	}
}

func TestExternalLinkNofollow(t *testing.T) {
	r := newTestRenderer(t)
	got := render(t, r, "[out](https://evil.example/x)", RenderContext{Mode: ModeComment})
	if !strings.Contains(got, `rel="nofollow"`) {
		t.Errorf("external link missing nofollow:\n%s", got)
	}
	in := render(t, r, "[in](https://githome.test/octo/demo)", RenderContext{Mode: ModeComment})
	if strings.Contains(in, "nofollow") {
		t.Errorf("on-host link should not get nofollow:\n%s", in)
	}
}

func TestCamoRewritesExternalImage(t *testing.T) {
	r := newTestRenderer(t)
	got := render(t, r, "![x](https://cdn.example/a.png)", RenderContext{Mode: ModeComment})
	if !strings.Contains(got, "https://camo.githome.test/") {
		t.Errorf("external image not proxied through camo:\n%s", got)
	}
	if strings.Contains(got, "cdn.example") {
		t.Errorf("original external image host leaked:\n%s", got)
	}
}

func TestCamoLeavesDataImage(t *testing.T) {
	r := newTestRenderer(t)
	// A data: image is allowed by the policy and must not be proxied.
	got := render(t, r, "![x](data:image/png;base64,iVBORw0KGgo=)", RenderContext{Mode: ModeComment})
	if !strings.Contains(got, "data:image/png") {
		t.Errorf("data image should pass through untouched:\n%s", got)
	}
	if strings.Contains(got, "camo.githome.test") {
		t.Errorf("data image should not be proxied:\n%s", got)
	}
}

// TestCrossSurfaceByteIdentity is the contract that the web UI and the REST
// text/html media type render the same bytes from the same source: RenderComment
// and a direct Render in ModeComment must be byte-identical, because both surfaces
// call the one Renderer.
func TestCrossSurfaceByteIdentity(t *testing.T) {
	r := newTestRenderer(t)
	src := "# Title\n\nSome **text** with `code` and a https://example.com link.\n"
	viaComment := string(r.RenderComment(context.Background(), nil, src))
	viaRender := render(t, r, src, RenderContext{Mode: ModeComment})
	if viaComment != viaRender {
		t.Errorf("surfaces diverged:\nRenderComment: %s\nRender:        %s", viaComment, viaRender)
	}
}

func TestVersionStable(t *testing.T) {
	r := newTestRenderer(t)
	v := r.Version()
	if v.Markup != markupVersion || v.Highlighter != highlighterVersion || v.Tags != tagsVersion {
		t.Errorf("Version() = %+v, want {%d %d %d}", v, markupVersion, highlighterVersion, tagsVersion)
	}
}

func TestClassify(t *testing.T) {
	r := newTestRenderer(t)
	cases := []struct {
		path string
		want string
	}{
		{"main.go", "Go"},
		{"app.py", "Python"},
		{"Makefile", "Makefile"},
		{"Dockerfile", "Dockerfile"},
		{"styles.scss", "SCSS"},
		{"weird.unknownext", ""},
	}
	for _, tc := range cases {
		if got := r.Classify(tc.path, nil, GitAttributes{}); got.Name != tc.want {
			t.Errorf("Classify(%q).Name = %q, want %q", tc.path, got.Name, tc.want)
		}
	}
	// A linguist-language override wins over the extension.
	if got := r.Classify("a.txt", nil, GitAttributes{Language: "Ruby"}); got.Name != "Ruby" {
		t.Errorf("override Classify = %q, want Ruby", got.Name)
	}
	// A shebang classifies an extensionless script.
	if got := r.Classify("run", []byte("#!/usr/bin/env python3\nprint(1)\n"), GitAttributes{}); got.Name != "Python" {
		t.Errorf("shebang Classify = %q, want Python", got.Name)
	}
}

func TestCodeNavUnavailableInPureGoBuild(t *testing.T) {
	r := newTestRenderer(t)
	idx, ok := r.ScanTags(context.Background(), []byte("func main() {}"), "go")
	if ok {
		t.Errorf("pure-Go build must report code navigation unavailable")
	}
	if idx.Symbols == nil {
		t.Errorf("ScanTags must return a usable empty index, got nil Symbols map")
	}
	if defs := r.Definitions(context.Background(), nil, "go", "main"); defs != nil {
		t.Errorf("Definitions must be empty in pure-Go build, got %v", defs)
	}
}

func TestHighlightCapSkipsBackendOverCutoff(t *testing.T) {
	r := newTestRenderer(t)
	if r.maxHL != 512<<10 {
		t.Fatalf("default highlight cap = %d, want %d (github's cutoff)", r.maxHL, 512<<10)
	}
	code := bytes.Repeat([]byte("func main() { return }\n"), (r.maxHL/23)+1)
	lines, err := r.HighlightLines(code, "go")
	if err != nil {
		t.Fatalf("HighlightLines: %v", err)
	}
	if len(lines) == 0 {
		t.Fatal("over-cap blob must still yield escaped lines")
	}
	for _, ln := range lines[:10] {
		if strings.Contains(string(ln), "pl-") {
			t.Fatalf("over-cap blob was highlighted: %q", ln)
		}
	}
}

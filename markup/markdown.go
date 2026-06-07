package markup

import (
	"bytes"
	"context"

	"github.com/yuin/goldmark"
	emoji "github.com/yuin/goldmark-emoji"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

// markdown.go is stages 1 through 3 of the pipeline: parse, transform, render. It
// builds the one goldmark instance (the GFM core plus footnotes, emoji, and the
// authored GitHub-beyond-spec extensions) and threads the per-call RenderContext
// through goldmark's parser context so the mention/ref transform can consult the
// caller's Resolve closure without markup ever importing the domain. The raw
// rendered HTML it returns is unsafe by design: html.WithUnsafe lets source
// <kbd>/<details>/<sub> survive to the sanitizer (stage 4), which is the single
// auditable wall. See implementation/10 section 3.

// renderCtxKey carries the per-call RenderContext on the parser context;
// goCtxKey carries the Go context, so the mention/ref transform can pass it to
// the caller's Resolve closure. goldmark requires context keys minted by
// NewContextKey, so they are package-level ints, not struct types.
var (
	renderCtxKey = parser.NewContextKey()
	goCtxKey     = parser.NewContextKey()
)

// buildGoldmark constructs the shared goldmark instance. The parser carries the
// authored AST transformers (alerts, mentions/refs); the renderer adds our node
// renderers on top of the GFM defaults so a fenced block routes to the
// highlighter, a math island, or a diagram island, and a resolved mention/ref
// node renders as a link. Registering a renderer for FencedCodeBlock at a higher
// priority overrides goldmark's default for that kind.
func (r *Renderer) buildGoldmark() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			// The GFM set, but with Table configured to emit the align ATTRIBUTE
			// rather than a style attribute: goldmark's default is text-align in an
			// inline style, which the sanitizer strips, so cells would lose their
			// alignment. The other three GFM pieces are added individually.
			extension.NewTable(extension.WithTableCellAlignMethod(extension.TableCellAlignAttribute)),
			extension.Strikethrough,
			extension.Linkify,
			extension.TaskList,
			extension.Footnote, // user-content-fn-{id} scheme
			emoji.Emoji,        // :shortcode: from the gemoji table
		),
		goldmark.WithParserOptions(
			parser.WithInlineParsers(
				// Math runs as an inline parser, not a post-parse text scan, so it
				// consumes a whole $...$ / $$...$$ span as one unit BEFORE the
				// backslash-escape parser can split LaTeX like \int into pieces.
				util.Prioritized(&mathParser{}, 150),
			),
			parser.WithASTTransformers(
				util.Prioritized(&alertTransformer{}, 100),
				util.Prioritized(&autolinkTransformer{}, 200),
			),
		),
		goldmark.WithRendererOptions(
			html.WithUnsafe(), // emit raw inline HTML; the SANITIZER (stage 4) is the wall
			renderer.WithNodeRenderers(
				util.Prioritized(&codeBlockRenderer{r: r}, 1),
				util.Prioritized(&alertRenderer{}, 1),
				util.Prioritized(&mathRenderer{}, 1),
				util.Prioritized(&refRenderer{}, 1),
			),
		),
	)
}

// renderMarkdown runs stages 1 through 3 and returns the raw (unsanitized) HTML.
// It stores the RenderContext and the Go context on a fresh parser context per
// call, so the instance stays shared and concurrency-safe while each render sees
// its own context.
func (r *Renderer) renderMarkdown(ctx context.Context, src []byte, rc RenderContext) ([]byte, error) {
	pc := parser.NewContext()
	pc.Set(renderCtxKey, rc)
	pc.Set(goCtxKey, ctx)
	var buf bytes.Buffer
	if err := r.md.Convert(src, &buf, parser.WithContext(pc)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// contextFrom reads the RenderContext a transformer stored on the parser context.
func contextFrom(pc parser.Context) (RenderContext, context.Context) {
	rc, _ := pc.Get(renderCtxKey).(RenderContext)
	ctx, _ := pc.Get(goCtxKey).(context.Context)
	if ctx == nil {
		ctx = context.Background()
	}
	return rc, ctx
}

// Package markup renders GitHub Flavored Markdown to sanitized, trusted HTML and
// highlights source code, for BOTH the web frontend and the REST text/html media
// type. It is a module-root package (a sibling of domain and presenter), never
// under fe/, because api/rest renders the same GFM through the same calls; it
// imports no fe/ package and no domain/store/git (the markup-boundary depguard
// rule, implementation/01 section 6).
//
// Its RenderComment, RenderFile, Render, and Highlight methods are the only
// sanctioned producers of template.HTML from user or source content in the whole
// codebase. Every render path runs the bluemonday allowlist (sanitize.go) before
// returning, so injecting the result as template.HTML is safe by construction.
//
// The package carries no clock, no RNG, and no map-iteration-order dependence in
// its output (anchor de-duplication uses a stable per-document counter), so its
// output is a pure function of (src, RenderContext, markupVersion) and the
// implementation/03 caches keyed on the version constants are sound. See
// implementation/10.
package markup

import (
	"context"
	"html/template"
	"log/slog"

	"github.com/yuin/goldmark"
)

// Renderer holds the constructed goldmark instance, the bluemonday policy, the
// highlighter, and the classifier. It is built once and is safe for concurrent
// use: goldmark parsing is goroutine-safe and the policy is read-only after New.
type Renderer struct {
	md      goldmark.Markdown
	policy  *Policy
	hl      highlighter
	camo    camoSigner
	class   *classifier
	baseURL string
	maxHL   int
	log     *slog.Logger
}

// Config is the markup section of the app config (cfg.Markup, implementation/01).
type Config struct {
	BaseURL           string       // on-host base, for link/ref/anchor emission
	CamoSecret        []byte       // HMAC key for the off-host image proxy; empty disables proxying
	CamoBaseURL       string       // where the proxy is mounted (default {BaseURL}/camo)
	MaxHighlightBytes int          // default 5<<20; a larger blob is shown unhighlighted (logged)
	EmojiAssetBase    string       // asset path for custom :octocat:-style image emoji (unused in v1)
	Logger            *slog.Logger // optional; falls back to slog.Default
}

const defaultMaxHighlightBytes = 5 << 20

// New constructs the shared Renderer. It wires the sanitizer in front of every
// render path (a package test asserts this, since goldmark runs WithUnsafe),
// builds the highlighter and the classifier, and logs the highlighter backend
// once at startup so a deployment running a degraded backend is observable. It
// never fails on config alone; a missing CamoSecret only disables proxying.
func New(cfg Config) *Renderer {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	maxHL := cfg.MaxHighlightBytes
	if maxHL <= 0 {
		maxHL = defaultMaxHighlightBytes
	}
	camo := newCamoSigner(cfg.CamoSecret, camoBase(cfg))
	if len(cfg.CamoSecret) == 0 {
		log.Info("markup camo disabled", "reason", "no camo secret configured")
	}
	hl := newHighlighter()
	log.Info("markup highlighter", "backend", hl.name())
	r := &Renderer{
		policy:  NewPolicy(),
		hl:      hl,
		camo:    camo,
		class:   newClassifier(),
		baseURL: cfg.BaseURL,
		maxHL:   maxHL,
		log:     log,
	}
	r.md = r.buildGoldmark()
	return r
}

func camoBase(cfg Config) string {
	if cfg.CamoBaseURL != "" {
		return cfg.CamoBaseURL
	}
	if cfg.BaseURL == "" {
		return ""
	}
	return cfg.BaseURL + "/camo"
}

// RenderMode selects reference resolution and link rewriting.
type RenderMode int

// RenderMode values: comment, file, gist, wiki, and plain, each selecting its own
// reference resolution and link rewriting.
const (
	ModeComment RenderMode = iota // issue/PR/commit comments, releases, descriptions
	ModeFile                      // a rendered README/Markdown file in the blob view
	ModeGist                      // a gist file (Spec 2004): refs off, gist-local links
	ModeWiki                      // a wiki page: non-notifying mentions
	ModePlain                     // /markdown/raw and any no-repo render: GFM + emoji only
)

// RefKind names the kind of reference the transform stage asks the caller to
// resolve.
type RefKind int

// RefKind values: a mention, an issue or PR reference, and a bare commit SHA.
const (
	RefMention RefKind = iota // @user or @org/team
	RefIssue                  // #123, GH-123, owner/repo#123
	RefCommit                 // a bare commit SHA
)

// RepoRef is the small repo identity the caller fills from its domain object. It
// keeps markup free of the domain package.
type RepoRef struct {
	Owner string
	Name  string
	ID    int64
}

// Viewer is the small viewer identity the caller fills from its session. It is
// used only to let the caller's Resolve closure visibility-gate a reference.
type Viewer struct {
	ID    int64
	Login string
}

// RenderContext controls reference resolution and link rewriting. It is a plain
// value the caller fills from its domain objects; markup never touches the
// domain. See implementation/10 section 2.
type RenderContext struct {
	Mode   RenderMode
	Repo   *RepoRef
	Ref    string // branch/tag/SHA, for File-mode relative-link rewriting
	Path   string // the file's in-repo path, for resolving relative links
	Viewer *Viewer

	// Resolve is the caller's hook the transform stage calls to decide whether a
	// @mention / #ref / SHA actually exists and is visible. It returns the link
	// target and whether to link at all. In ModePlain, or when Resolve is nil, no
	// mention/ref/SHA links, which is the safe no-repo default.
	Resolve func(ctx context.Context, kind RefKind, raw string) (target string, ok bool)
}

// RenderComment renders a comment body, release note, or repo description:
// ModeComment, full GitHub-extension processing, notifying mentions, relative
// links against the repo default branch. This is the method the view layer calls
// for BodyHTML and the REST text/html media type calls for a comment.
func (r *Renderer) RenderComment(ctx context.Context, repo *RepoRef, src string) template.HTML {
	out, err := r.Render(ctx, []byte(src), RenderContext{Mode: ModeComment, Repo: repo})
	if err != nil {
		r.log.Error("render comment failed, falling back to escaped text", "error", err)
		return escapeFallback(src)
	}
	return out
}

// RenderFile renders a README/Markdown file in the blob or repo-home view:
// ModeFile, non-notifying mentions, relative links/images rewritten against the
// file's ref and path. ref and path place the rewriting.
func (r *Renderer) RenderFile(ctx context.Context, repo *RepoRef, ref, path, src string) template.HTML {
	out, err := r.Render(ctx, []byte(src), RenderContext{Mode: ModeFile, Repo: repo, Ref: ref, Path: path})
	if err != nil {
		r.log.Error("render file failed, falling back to escaped text", "error", err, "path", path)
		return escapeFallback(src)
	}
	return out
}

// Render is the general entry the /markdown and /markdown/raw API call with an
// explicit context (Plain when no repo is given). RenderComment and RenderFile
// are presets over it. It returns an error only for an internal failure; a
// malformed document still renders, since CommonMark is total.
func (r *Renderer) Render(ctx context.Context, src []byte, rc RenderContext) (template.HTML, error) {
	rendered, err := r.renderMarkdown(ctx, src, rc)
	if err != nil {
		return "", err
	}
	// Stage 4: the sanitizer is THE trust boundary; it runs on every path.
	sanitized := r.policy.Sanitize(rendered)
	// Stage 5: post-process runs on already-sanitized HTML (anchors, rel/nofollow,
	// camo, task-list wiring), so it can add ids and rels without re-opening safety.
	out := r.postProcess(sanitized, rc)
	return template.HTML(out), nil // nolint:gosec // out has passed Policy.Sanitize, the only sanctioned producer
}

// escapeFallback renders source as escaped plain text wrapped in a paragraph, the
// always-safe fallback when the pipeline fails internally.
func escapeFallback(src string) template.HTML {
	return template.HTML("<p>" + template.HTMLEscapeString(src) + "</p>") // nolint:gosec // fully escaped
}

package markup

import (
	"html"
	"html/template"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// highlight_chroma.go is the pure-Go highlighter, the backend the default
// CGO_ENABLED=0 static binary uses. It lexes (no parse tree), so it covers
// highlighting but not code navigation; codenav.go returns empty in this build.
// It shares the pl-* token vocabulary with the future Tree-sitter backend so one
// highlight.css themes either, and the nine themes restyle it for free. See
// implementation/10 section 6.2.
//
// The Tree-sitter backend and the build-tag split it lives behind are deferred in
// F2 (the cgo grammars are a large vendoring step); this file carries no build tag
// so every build, cgo or not, highlights through chroma until that backend lands.

type chromaHL struct{}

func newHighlighter() highlighter { return &chromaHL{} }

func (h *chromaHL) name() string { return "chroma" }

// highlight lexes code in lang and returns per-line HTML, each line a sequence of
// escaped text and pl-* spans. It returns ok=false when chroma has no lexer for
// the language, so the caller falls back to plain escaped text.
func (h *chromaHL) highlight(code []byte, lang string) ([]template.HTML, bool) {
	lexer := lexerFor(lang)
	if lexer == nil {
		return nil, false
	}
	it, err := lexer.Tokenise(nil, string(code))
	if err != nil {
		return nil, false
	}
	var (
		lines []template.HTML
		cur   strings.Builder
	)
	flush := func() {
		lines = append(lines, template.HTML(cur.String())) // nolint:gosec // token text escaped below, only pl-* spans are raw
		cur.Reset()
	}
	for _, tok := range it.Tokens() {
		class := plClass(tok.Type)
		parts := strings.Split(tok.Value, "\n")
		for i, part := range parts {
			if i > 0 {
				flush()
			}
			if part == "" {
				continue
			}
			esc := html.EscapeString(part)
			if class == "" {
				cur.WriteString(esc)
			} else {
				cur.WriteString(`<span class="` + class + `">` + esc + `</span>`)
			}
		}
	}
	flush()
	// A trailing newline produces a final empty line; drop it so the count matches
	// the file's lines, the way the blob view counts.
	if n := len(lines); n > 1 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines, true
}

// lexerFor resolves a chroma lexer for the language name, returning nil when none
// matches so the caller degrades to plain text. The empty language (indented code
// blocks) has no lexer and degrades cleanly.
func lexerFor(lang string) chroma.Lexer {
	if lang == "" {
		return nil
	}
	if l := lexers.Get(lang); l != nil && l != lexers.Fallback {
		return l
	}
	return nil
}

// plClass maps a chroma token type to a GitHub pl-* class. The mapping is
// deliberately conservative: categories github.com leaves uncolored map to the
// empty class (plain escaped text). See implementation/10 section 6.2.
func plClass(t chroma.TokenType) string {
	switch {
	case t.InCategory(chroma.Comment):
		return "pl-c"
	case t.InCategory(chroma.Keyword):
		return "pl-k"
	case t.InSubCategory(chroma.LiteralString):
		return "pl-s"
	case t.InSubCategory(chroma.LiteralNumber):
		return "pl-c1"
	}
	switch t {
	case chroma.NameFunction, chroma.NameFunctionMagic:
		return "pl-en"
	case chroma.NameClass, chroma.NameNamespace, chroma.NameBuiltin, chroma.NameBuiltinPseudo, chroma.NameException:
		return "pl-e"
	case chroma.NameConstant:
		return "pl-c1"
	case chroma.NameTag:
		return "pl-ent"
	case chroma.NameAttribute, chroma.NameDecorator:
		return "pl-e"
	case chroma.NameVariable, chroma.NameVariableInstance, chroma.NameVariableGlobal:
		return "pl-smi"
	}
	return ""
}

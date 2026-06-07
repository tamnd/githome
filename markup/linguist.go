package markup

import (
	"path"
	"strings"
)

// linguist.go is the Linguist-equivalent classifier. It is pure Go and present in
// every build, so the language bar and the language-* class are correct regardless
// of the highlighter backend; only the spans inside differ in fidelity. Classify
// precedence is the Linguist order: a linguist-language override, then filename,
// extension, shebang, and a small content heuristic (implementation/10 section
// 6.3).
//
// The table here covers the common languages a self-hosted forge sees. It is not
// the full vendored languages.yml; an unlisted extension classifies as the empty
// Language (no color, no grammar), which the blob view renders as plain text, a
// clean honest degradation rather than a wrong guess.

// Language is the classifier result: the display name, the Linguist color for the
// language bar, and the grammar id the highlighter keys on (the chroma lexer name
// in the pure-Go build).
type Language struct {
	Name    string
	Color   string
	Grammar string
}

// GitAttributes carries the .gitattributes signals that override detection, filled
// by the caller from the repo's attributes for the path.
type GitAttributes struct {
	Language      string // linguist-language=<name> override
	Vendored      bool   // linguist-vendored
	Generated     bool   // linguist-generated
	Documentation bool   // linguist-documentation
}

// langEntry is one row of the classifier table.
type langEntry struct {
	name    string
	color   string
	grammar string
}

// classifier holds the lookup tables built once at startup.
type classifier struct {
	byExt      map[string]langEntry
	byFilename map[string]langEntry
	byShebang  map[string]langEntry
	byName     map[string]langEntry
}

// Classify runs the precedence chain and returns the language. An unmatched path
// returns the zero Language, which the blob view renders monochrome.
func (r *Renderer) Classify(p string, content []byte, attrs GitAttributes) Language {
	return r.class.classify(p, content, attrs)
}

func (c *classifier) classify(p string, content []byte, attrs GitAttributes) Language {
	if attrs.Language != "" {
		if e, ok := c.byName[strings.ToLower(attrs.Language)]; ok {
			return e.lang()
		}
	}
	base := path.Base(p)
	if e, ok := c.byFilename[strings.ToLower(base)]; ok {
		return e.lang()
	}
	ext := strings.ToLower(path.Ext(base))
	if e, ok := c.byExt[ext]; ok {
		return e.lang()
	}
	if lang, ok := c.shebang(content); ok {
		return lang
	}
	return Language{}
}

// shebang reads a leading #! line and matches the interpreter basename.
func (c *classifier) shebang(content []byte) (Language, bool) {
	if len(content) < 2 || content[0] != '#' || content[1] != '!' {
		return Language{}, false
	}
	line := string(content)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	fields := strings.Fields(line)
	for _, f := range fields {
		bn := path.Base(f)
		if bn == "env" {
			continue
		}
		// Strip a version suffix like python3 -> python.
		bn = strings.TrimRight(bn, "0123456789.")
		if e, ok := c.byShebang[bn]; ok {
			return e.lang(), true
		}
	}
	return Language{}, false
}

func (e langEntry) lang() Language {
	return Language{Name: e.name, Color: e.color, Grammar: e.grammar}
}

// newClassifier builds the tables. The colors are the Linguist colors; the grammar
// ids are the chroma lexer names the pure-Go highlighter resolves.
func newClassifier() *classifier {
	langs := []struct {
		name     string
		color    string
		grammar  string
		exts     []string
		files    []string
		shebangs []string
	}{
		{"Go", "#00ADD8", "go", []string{".go"}, nil, nil},
		{"Python", "#3572A5", "python", []string{".py", ".pyw", ".pyi"}, nil, []string{"python"}},
		{"JavaScript", "#f1e05a", "javascript", []string{".js", ".mjs", ".cjs", ".jsx"}, nil, []string{"node"}},
		{"TypeScript", "#3178c6", "typescript", []string{".ts", ".tsx"}, nil, nil},
		{"Ruby", "#701516", "ruby", []string{".rb", ".rake"}, []string{"rakefile", "gemfile"}, []string{"ruby"}},
		{"Rust", "#dea584", "rust", []string{".rs"}, nil, nil},
		{"C", "#555555", "c", []string{".c", ".h"}, nil, nil},
		{"C++", "#f34b7d", "cpp", []string{".cc", ".cpp", ".cxx", ".hpp", ".hh"}, nil, nil},
		{"C#", "#178600", "csharp", []string{".cs"}, nil, nil},
		{"Java", "#b07219", "java", []string{".java"}, nil, nil},
		{"Kotlin", "#A97BFF", "kotlin", []string{".kt", ".kts"}, nil, nil},
		{"Swift", "#F05138", "swift", []string{".swift"}, nil, nil},
		{"Shell", "#89e051", "bash", []string{".sh", ".bash", ".zsh"}, nil, []string{"sh", "bash", "zsh"}},
		{"HTML", "#e34c26", "html", []string{".html", ".htm"}, nil, nil},
		{"CSS", "#563d7c", "css", []string{".css"}, nil, nil},
		{"SCSS", "#c6538c", "scss", []string{".scss"}, nil, nil},
		{"JSON", "#292929", "json", []string{".json"}, nil, nil},
		{"YAML", "#cb171e", "yaml", []string{".yml", ".yaml"}, nil, nil},
		{"TOML", "#9c4221", "toml", []string{".toml"}, nil, nil},
		{"Markdown", "#083fa1", "markdown", []string{".md", ".markdown"}, nil, nil},
		{"SQL", "#e38c00", "sql", []string{".sql"}, nil, nil},
		{"PHP", "#4F5D95", "php", []string{".php"}, nil, []string{"php"}},
		{"Perl", "#0298c3", "perl", []string{".pl", ".pm"}, nil, []string{"perl"}},
		{"Lua", "#000080", "lua", []string{".lua"}, nil, []string{"lua"}},
		{"Haskell", "#5e5086", "haskell", []string{".hs"}, nil, nil},
		{"Elixir", "#6e4a7e", "elixir", []string{".ex", ".exs"}, nil, nil},
		{"Scala", "#c22d40", "scala", []string{".scala", ".sc"}, nil, nil},
		{"Dart", "#00B4AB", "dart", []string{".dart"}, nil, nil},
		{"Dockerfile", "#384d54", "docker", []string{".dockerfile"}, []string{"dockerfile"}, nil},
		{"Makefile", "#427819", "make", []string{".mk"}, []string{"makefile"}, nil},
		{"XML", "#0060ac", "xml", []string{".xml", ".xsd", ".svg"}, nil, nil},
		{"Protocol Buffer", "#e3d7ff", "protobuf", []string{".proto"}, nil, nil},
	}
	c := &classifier{
		byExt:      map[string]langEntry{},
		byFilename: map[string]langEntry{},
		byShebang:  map[string]langEntry{},
		byName:     map[string]langEntry{},
	}
	for _, l := range langs {
		e := langEntry{name: l.name, color: l.color, grammar: l.grammar}
		c.byName[strings.ToLower(l.name)] = e
		for _, ext := range l.exts {
			c.byExt[ext] = e
		}
		for _, f := range l.files {
			c.byFilename[f] = e
		}
		for _, s := range l.shebangs {
			c.byShebang[s] = e
		}
	}
	return c
}

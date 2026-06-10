package presenter

import (
	"mime"
	"path/filepath"
	"strings"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/restmodel"
	"github.com/tamnd/githome/store"
)

// Gist renders a store.GistRow (with its resolved owner) into the REST wire model.
// commentCount is the pre-fetched number of comments for this gist.
func (b *URLBuilder) Gist(g *store.GistRow, owner *domain.User, commentCount int, format nodeid.Format) restmodel.Gist {
	api := b.API("gists", g.GistID)
	gitURL := b.HTML("_gist", g.GistID+".git")

	files := make(map[string]restmodel.GistFile, len(g.Files))
	for _, f := range g.Files {
		ct := guessContentType(f.Filename)
		lang := guessLanguage(f.Filename)
		rawURL := b.HTML("_gist", g.GistID, "raw", f.Filename)
		content := f.Content
		files[f.Filename] = restmodel.GistFile{
			Filename:  f.Filename,
			Type:      ct,
			Language:  lang,
			RawURL:    rawURL,
			Size:      len(f.Content),
			Truncated: false,
			Content:   &content,
		}
	}

	return restmodel.Gist{
		ID:          g.GistID,
		NodeID:      nodeid.Encode(nodeid.KindGist, g.PK, format),
		Description: g.Description,
		Public:      g.Public,
		Owner:       b.SimpleUser(owner, format),
		User:        nil,
		Files:       files,
		Forks:       []any{},
		History:     []any{},
		Truncated:   false,
		Comments:    commentCount,
		GitPullURL:  gitURL,
		GitPushURL:  gitURL,
		HTMLURL:     b.HTML("gist", g.GistID),
		CommitsURL:  api + "/commits",
		ForksURL:    api + "/forks",
		CommentsURL: api + "/comments",
		CreatedAt:   restmodel.NewTime(g.CreatedAt),
		UpdatedAt:   restmodel.NewTime(g.UpdatedAt),
	}
}

// GistComment renders a store.GistCommentRow into the REST wire model.
func (b *URLBuilder) GistComment(c *store.GistCommentRow, user *domain.User, format nodeid.Format) restmodel.GistComment {
	return restmodel.GistComment{
		ID:        c.PK,
		NodeID:    nodeid.Encode(nodeid.KindGistComment, c.PK, format),
		Body:      c.Body,
		User:      b.SimpleUser(user, format),
		CreatedAt: restmodel.NewTime(c.CreatedAt),
		UpdatedAt: restmodel.NewTime(c.UpdatedAt),
	}
}

func guessContentType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	ct := mime.TypeByExtension(ext)
	if ct == "" {
		return "text/plain"
	}
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct
}

func guessLanguage(filename string) *string {
	ext := strings.ToLower(filepath.Ext(filename))
	langs := map[string]string{
		".go":   "Go",
		".py":   "Python",
		".js":   "JavaScript",
		".ts":   "TypeScript",
		".rb":   "Ruby",
		".java": "Java",
		".c":    "C",
		".cpp":  "C++",
		".rs":   "Rust",
		".sh":   "Shell",
		".md":   "Markdown",
		".json": "JSON",
		".yaml": "YAML",
		".yml":  "YAML",
		".toml": "TOML",
		".html": "HTML",
		".css":  "CSS",
		".sql":  "SQL",
	}
	if lang, ok := langs[ext]; ok {
		return &lang
	}
	return nil
}

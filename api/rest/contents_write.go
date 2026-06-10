package rest

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
)

// contentsWriteBody is the request body for PUT /repos/{owner}/{repo}/contents/{path}.
type contentsWriteBody struct {
	Message   string `json:"message"`
	Content   string `json:"content"` // base64-encoded
	Branch    string `json:"branch"`
	SHA       string `json:"sha"` // current file SHA, required for updates
	Committer *struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"committer"`
	Author *struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"author"`
}

// contentsDeleteBody is the request body for DELETE /repos/{owner}/{repo}/contents/{path}.
type contentsDeleteBody struct {
	Message   string `json:"message"`
	SHA       string `json:"sha"`
	Branch    string `json:"branch"`
	Committer *struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"committer"`
}

// handleContentsCreate serves PUT /repos/{owner}/{repo}/contents/{path}.
func handleContentsCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		path := c.Param("path")
		if path == "" {
			writeError(c.Writer(), errUnprocessable("path is required"))
			return nil
		}

		var body contentsWriteBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if body.Message == "" {
			writeError(c.Writer(), errUnprocessable("message is required"))
			return nil
		}
		if body.Content == "" {
			writeError(c.Writer(), errUnprocessable("content is required"))
			return nil
		}

		// GitHub strips newlines from base64-encoded content.
		b64 := strings.ReplaceAll(body.Content, "\n", "")
		decoded, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			writeError(c.Writer(), errUnprocessable("content must be base64-encoded"))
			return nil
		}

		authorName, authorEmail := "", ""
		if body.Author != nil {
			authorName = body.Author.Name
			authorEmail = body.Author.Email
		} else if body.Committer != nil {
			authorName = body.Committer.Name
			authorEmail = body.Committer.Email
		}

		res, err := d.Repos.WriteFile(repo, domain.WriteFileInput{
			Path:        path,
			Content:     decoded,
			Message:     body.Message,
			AuthorName:  authorName,
			AuthorEmail: authorEmail,
			Branch:      body.Branch,
		})
		if errors.Is(err, domain.ErrEmptyRepo) || errors.Is(err, domain.ErrGitNotFound) {
			// OK - new repo
			res, err = d.Repos.WriteFile(repo, domain.WriteFileInput{
				Path:        path,
				Content:     decoded,
				Message:     body.Message,
				AuthorName:  authorName,
				AuthorEmail: authorEmail,
				Branch:      body.Branch,
			})
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		}

		writeJSON(c.Writer(), http.StatusCreated, map[string]any{
			"content": map[string]any{
				"name": lastSegment(path),
				"path": path,
				"sha":  res.BlobSHA,
			},
			"commit": map[string]any{
				"sha":     res.CommitSHA,
				"message": body.Message,
			},
		})
		return nil
	}
}

// handleContentsDelete serves DELETE /repos/{owner}/{repo}/contents/{path}.
func handleContentsDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		ctx := c.Request().Context()
		actor := auth.ActorFrom(ctx)
		if !actor.IsUser() {
			writeError(c.Writer(), errRequiresAuth())
			return nil
		}
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		path := c.Param("path")

		var body contentsDeleteBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if body.Message == "" {
			writeError(c.Writer(), errUnprocessable("message is required"))
			return nil
		}

		authorName, authorEmail := "", ""
		if body.Committer != nil {
			authorName = body.Committer.Name
			authorEmail = body.Committer.Email
		}

		res, err := d.Repos.DeleteFile(repo, domain.WriteFileInput{
			Path:        path,
			Message:     body.Message,
			AuthorName:  authorName,
			AuthorEmail: authorEmail,
			Branch:      body.Branch,
		})
		if errors.Is(err, domain.ErrGitNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}

		writeJSON(c.Writer(), http.StatusOK, map[string]any{
			"commit": map[string]any{
				"sha":     res.CommitSHA,
				"message": body.Message,
			},
		})
		return nil
	}
}

func lastSegment(path string) string {
	parts := strings.Split(strings.TrimRight(path, "/"), "/")
	if len(parts) == 0 {
		return path
	}
	return parts[len(parts)-1]
}

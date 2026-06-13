package rest

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/nodeid"
)

// handleGitBlobCreate serves POST /repos/{owner}/{repo}/git/blobs.
func handleGitBlobCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		var body struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"` // "utf-8" or "base64"
		}
		if !decodeJSON(c, &body) {
			return nil
		}
		var raw []byte
		switch strings.ToLower(body.Encoding) {
		case "base64":
			raw, err = base64.StdEncoding.DecodeString(stripBase64Whitespace(body.Content))
			if err != nil {
				writeError(c.Writer(), errUnprocessable("content: invalid base64"))
				return nil
			}
		default:
			raw = []byte(body.Content)
		}
		res, err := d.Repos.CreateBlob(repo, raw)
		if errors.Is(err, domain.ErrEmptyRepo) || errors.Is(err, domain.ErrGitNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, map[string]any{
			"sha":      res.SHA,
			"url":      d.URLs.API("repos", repo.Owner.Login, repo.Name, "git", "blobs", res.SHA),
			"size":     res.Size,
			"encoding": "base64",
		})
		return nil
	}
}

// handleGitTreeCreate serves POST /repos/{owner}/{repo}/git/trees.
func handleGitTreeCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		var body struct {
			BaseTree string `json:"base_tree"`
			Tree     []struct {
				Path    string  `json:"path"`
				Mode    string  `json:"mode"`
				Type    string  `json:"type"`
				SHA     *string `json:"sha"`
				Content *string `json:"content"`
			} `json:"tree"`
		}
		if !decodeJSON(c, &body) {
			return nil
		}
		entries := make([]git.CreateTreeEntry, 0, len(body.Tree))
		for _, e := range body.Tree {
			entry := git.CreateTreeEntry{
				Path: e.Path,
				Mode: e.Mode,
				Type: git.ObjectType(e.Type),
			}
			if e.SHA != nil {
				entry.SHA = *e.SHA
			}
			if e.Content != nil {
				entry.Content = []byte(*e.Content)
			}
			entries = append(entries, entry)
		}
		res, err := d.Repos.CreateTree(repo, body.BaseTree, entries)
		if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		repoBase := d.URLs.API("repos", repo.Owner.Login, repo.Name)
		treeEntries := make([]any, 0, len(res.Entries))
		for _, e := range res.Entries {
			entry := map[string]any{
				"path": e.Path,
				"mode": e.Mode,
				"type": string(e.Type),
				"sha":  e.SHA,
				"url":  repoBase + "/git/blobs/" + e.SHA,
			}
			if e.Type == git.ObjectBlob {
				entry["size"] = e.Size
			} else {
				entry["url"] = repoBase + "/git/trees/" + e.SHA
			}
			treeEntries = append(treeEntries, entry)
		}
		writeJSON(c.Writer(), http.StatusCreated, map[string]any{
			"sha":       res.SHA,
			"url":       repoBase + "/git/trees/" + res.SHA,
			"tree":      treeEntries,
			"truncated": false,
		})
		return nil
	}
}

// handleGitCommitCreate serves POST /repos/{owner}/{repo}/git/commits.
func handleGitCommitCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		var body struct {
			Message   string   `json:"message"`
			Tree      string   `json:"tree"`
			Parents   []string `json:"parents"`
			Author    *struct {
				Name  string `json:"name"`
				Email string `json:"email"`
				Date  string `json:"date"`
			} `json:"author"`
			Committer *struct {
				Name  string `json:"name"`
				Email string `json:"email"`
				Date  string `json:"date"`
			} `json:"committer"`
		}
		if !decodeJSON(c, &body) {
			return nil
		}
		var missing []FieldError
		if body.Message == "" {
			missing = append(missing, FieldError{Resource: "Commit", Field: "message", Code: "missing_field"})
		}
		if body.Tree == "" {
			missing = append(missing, FieldError{Resource: "Commit", Field: "tree", Code: "missing_field"})
		}
		if len(missing) > 0 {
			writeError(c.Writer(), errValidation(missing...))
			return nil
		}
		in := git.CreateCommitInput{
			Message: body.Message,
			Tree:    body.Tree,
			Parents: body.Parents,
		}
		if body.Author != nil {
			in.Author = git.Signature{
				Name:  body.Author.Name,
				Email: body.Author.Email,
				When:  parseGitTime(body.Author.Date),
			}
		}
		if body.Committer != nil {
			in.Committer = git.Signature{
				Name:  body.Committer.Name,
				Email: body.Committer.Email,
				When:  parseGitTime(body.Committer.Date),
			}
		}
		res, err := d.Repos.CreateGitCommit(repo, in)
		if err != nil {
			return err
		}
		// Read back the commit so we can render the full shape.
		commit, commitErr := d.Repos.GetCommit(repo, res.SHA)
		if commitErr != nil {
			return commitErr
		}
		writeJSON(c.Writer(), http.StatusCreated,
			d.URLs.GitCommit(repo.Owner.Login, repo.Name, repo.ID, commit))
		return nil
	}
}

// handleGitTagCreate serves POST /repos/{owner}/{repo}/git/tags.
func handleGitTagCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		var body struct {
			Tag     string `json:"tag"`
			Message string `json:"message"`
			Object  string `json:"object"`
			Type    string `json:"type"`
			Tagger  *struct {
				Name  string `json:"name"`
				Email string `json:"email"`
				Date  string `json:"date"`
			} `json:"tagger"`
		}
		if !decodeJSON(c, &body) {
			return nil
		}
		in := git.CreateTagInput{
			Tag:        body.Tag,
			Message:    body.Message,
			ObjectSHA:  body.Object,
			ObjectType: body.Type,
		}
		if body.Tagger != nil {
			in.Tagger = git.Signature{
				Name:  body.Tagger.Name,
				Email: body.Tagger.Email,
				When:  parseGitTime(body.Tagger.Date),
			}
		}
		res, err := d.Repos.CreateGitTag(repo, in)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, gitTagJSON(res, d, repo))
		return nil
	}
}

// handleGitTagGet serves GET /repos/{owner}/{repo}/git/tags/{sha}.
func handleGitTagGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		sha := c.Param("sha")
		res, err := d.Repos.GetGitTag(repo, sha)
		if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		tag := &git.CreateTagResult{
			SHA:     res.SHA,
			Tag:     res.Tag,
			Message: res.Message,
			Object:  res.Object,
			Type:    res.Type,
			Tagger:  res.Tagger,
		}
		writeJSON(c.Writer(), http.StatusOK, gitTagJSON(tag, d, repo))
		return nil
	}
}

func gitTagJSON(res *git.CreateTagResult, d Deps, repo *domain.Repo) map[string]any {
	repoBase := d.URLs.API("repos", repo.Owner.Login, repo.Name)
	objURL := repoBase + "/git/commits/" + res.Object
	switch res.Type {
	case "tree":
		objURL = repoBase + "/git/trees/" + res.Object
	case "blob":
		objURL = repoBase + "/git/blobs/" + res.Object
	}
	return map[string]any{
		"sha":     res.SHA,
		"node_id": nodeid.EncodeGitObject("tag", repo.ID, res.SHA),
		"url":     repoBase + "/git/tags/" + res.SHA,
		"tag":     res.Tag,
		"message": res.Message,
		"tagger": map[string]any{
			"name":  res.Tagger.Name,
			"email": res.Tagger.Email,
			"date":  res.Tagger.When.UTC().Format(time.RFC3339),
		},
		"object": map[string]any{
			"sha":  res.Object,
			"type": res.Type,
			"url":  objURL,
		},
		"verification": map[string]any{
			"verified":  false,
			"reason":    "unsigned",
			"signature": nil,
			"payload":   nil,
		},
	}
}

// stripBase64Whitespace removes every whitespace character from a base64 body.
// GitHub tolerates the line wrapping that clients (and `base64` itself) insert,
// so newlines, carriage returns, tabs, and spaces all drop out before decoding.
func stripBase64Whitespace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// parseGitTime parses an ISO 8601 / RFC 3339 date string. Returns zero time on failure.
func parseGitTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

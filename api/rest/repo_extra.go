package rest

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
)

// handleREADME serves GET /repos/{owner}/{repo}/readme.
func handleREADME(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		ref := c.Request().URL.Query().Get("ref")
		if ref == "" {
			ref = repo.DefaultBranch
		}
		candidates := []string{
			"README.md", "readme.md", "Readme.md",
			"README", "readme",
			"README.txt", "readme.txt",
			"README.rst", "readme.rst",
		}
		for _, name := range candidates {
			res, err := d.Repos.Contents(repo, name, ref)
			if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
				continue
			}
			if err != nil {
				return err
			}
			if !res.IsDir && res.File != nil {
				out := d.URLs.ContentFile(repo.Owner.Login, repo.Name, ref, res.Entry, res.File.Content)
				writeJSON(c.Writer(), http.StatusOK, out)
				return nil
			}
		}
		writeError(c.Writer(), errNotFound())
		return nil
	}
}

// handleZipball serves GET /repos/{owner}/{repo}/zipball/{ref}.
func handleZipball(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		ref := c.Param("ref")
		if ref == "" {
			ref = repo.DefaultBranch
		}
		tree, err := d.Repos.GetTree(repo, ref, true)
		if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		prefix := repo.Owner.Login + "-" + repo.Name + "-" + archiveRef(ref)
		w := c.Writer()
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, prefix))
		zw := zip.NewWriter(w)
		defer zw.Close()
		for _, e := range tree.Entries {
			if e.Type != git.ObjectBlob || e.Size > 10<<20 {
				continue
			}
			blob, err := d.Repos.GetBlob(repo, e.SHA)
			if err != nil {
				continue
			}
			fh, err := zw.Create(path.Join(prefix, e.Path))
			if err != nil {
				return err
			}
			if _, err := fh.Write(blob.Content); err != nil {
				return err
			}
		}
		return nil
	}
}

// handleTarball serves GET /repos/{owner}/{repo}/tarball/{ref}.
func handleTarball(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		ref := c.Param("ref")
		if ref == "" {
			ref = repo.DefaultBranch
		}
		tree, err := d.Repos.GetTree(repo, ref, true)
		if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		prefix := repo.Owner.Login + "-" + repo.Name + "-" + archiveRef(ref)
		w := c.Writer()
		w.Header().Set("Content-Type", "application/x-gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.gz"`, prefix))
		gzw := gzip.NewWriter(w)
		defer gzw.Close()
		tw := tar.NewWriter(gzw)
		defer tw.Close()
		for _, e := range tree.Entries {
			if e.Type != git.ObjectBlob || e.Size > 10<<20 {
				continue
			}
			blob, err := d.Repos.GetBlob(repo, e.SHA)
			if err != nil {
				continue
			}
			hdr := &tar.Header{
				Name:    path.Join(prefix, e.Path),
				Mode:    0644,
				Size:    int64(len(blob.Content)),
				ModTime: repo.UpdatedAt,
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if _, err := tw.Write(blob.Content); err != nil {
				return err
			}
		}
		return nil
	}
}

// handleCompare serves GET /repos/{owner}/{repo}/compare/{basehead}.
func handleCompare(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		basehead := c.Param("basehead")
		var base, head string
		if b, h, ok := strings.Cut(basehead, "..."); ok {
			base, head = b, h
		} else if b, h, ok := strings.Cut(basehead, ".."); ok {
			base, head = b, h
		} else {
			writeError(c.Writer(), errUnprocessable("invalid comparison target: must be base...head or base..head"))
			return nil
		}

		result, err := d.Repos.Compare(c.Request().Context(), repo, base, head)
		if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}

		status := "ahead"
		if len(result.Commits) == 0 {
			status = "identical"
		}

		commits := make([]any, 0, len(result.Commits))
		for _, rc := range result.Commits {
			commits = append(commits, d.URLs.RepoCommit(repo.Owner.Login, repo.Name, repo.ID, rc))
		}

		files := make([]any, 0, len(result.Files))
		for _, f := range result.Files {
			files = append(files, map[string]any{
				"filename":     f.Path,
				"status":       string(f.Status),
				"additions":    f.Additions,
				"deletions":    f.Deletions,
				"changes":      f.Additions + f.Deletions,
				"blob_url":     d.URLs.API("repos", repo.Owner.Login, repo.Name, "blob", result.Head.Commit, f.Path),
				"raw_url":      d.URLs.API("repos", repo.Owner.Login, repo.Name, "raw", result.Head.Commit, f.Path),
				"contents_url": d.URLs.API("repos", repo.Owner.Login, repo.Name, "contents", f.Path),
			})
		}

		writeJSON(c.Writer(), http.StatusOK, map[string]any{
			"url":               d.URLs.API("repos", repo.Owner.Login, repo.Name, "compare", basehead),
			"html_url":          d.URLs.HTML(repo.Owner.Login, repo.Name, "compare", basehead),
			"status":            status,
			"ahead_by":          len(result.Commits),
			"behind_by":         0,
			"total_commits":     len(result.Commits),
			"commits":           commits,
			"base_commit":       nil,
			"merge_base_commit": nil,
			"files":             files,
		})
		return nil
	}
}

// archiveRef returns a short human-readable string for a ref suitable for an
// archive filename prefix.
func archiveRef(ref string) string {
	if strings.HasPrefix(ref, "refs/heads/") {
		return strings.TrimPrefix(ref, "refs/heads/")
	}
	if strings.HasPrefix(ref, "refs/tags/") {
		return strings.TrimPrefix(ref, "refs/tags/")
	}
	if len(ref) > 7 {
		return ref[:7]
	}
	return ref
}

// mountRepoExtra registers README, archive, compare, and contents-write endpoints.
func mountRepoExtra(r *mizu.Router, d Deps) {
	if d.Gists != nil {
		r.Get("/users/{username}/gists", handleUserGists(d))
	}

	if d.Repos == nil {
		return
	}
	r.Get("/repos/{owner}/{repo}/readme", handleREADME(d))
	r.Get("/repos/{owner}/{repo}/zipball/{ref}", handleZipball(d))
	r.Get("/repos/{owner}/{repo}/tarball/{ref}", handleTarball(d))
	r.Get("/repos/{owner}/{repo}/compare/{basehead}", handleCompare(d))
	r.Put("/repos/{owner}/{repo}/contents/{path...}", requireScope(handleContentsCreate(d), "repo", "public_repo"))
	r.Delete("/repos/{owner}/{repo}/contents/{path...}", requireScope(handleContentsDelete(d), "repo", "public_repo"))
}

// keep json import used through writeJSON helper chain
var _ = json.Marshal

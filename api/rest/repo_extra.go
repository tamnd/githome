package rest

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
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

// handleZipball serves GET /repos/{owner}/{repo}/zipball/{ref}. The archive
// streams straight out of one git archive subprocess; nothing is buffered in
// memory, and the ref resolves before the first byte so a bad one is still a
// clean 404.
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
		prefix := repo.Owner.Login + "-" + repo.Name + "-" + archiveRef(ref)
		w := c.Writer()
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, prefix))
		err = d.Repos.Archive(c.Context(), repo, ref, "zip", prefix, w)
		if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
			w.Header().Del("Content-Type")
			w.Header().Del("Content-Disposition")
			writeError(w, errNotFound())
			return nil
		}
		return err
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
		prefix := repo.Owner.Login + "-" + repo.Name + "-" + archiveRef(ref)
		w := c.Writer()
		w.Header().Set("Content-Type", "application/x-gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.gz"`, prefix))
		// git archive emits the tar; the gzip layer stays here so the exec
		// seam never depends on a gzip binary.
		gzw := gzip.NewWriter(w)
		err = d.Repos.Archive(c.Context(), repo, ref, "tar", prefix, gzw)
		if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
			w.Header().Del("Content-Type")
			w.Header().Del("Content-Disposition")
			writeError(w, errNotFound())
			return nil
		}
		if err != nil {
			return err
		}
		return gzw.Close()
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
			"ahead_by":          result.TotalCommits,
			"behind_by":         0,
			"total_commits":     result.TotalCommits,
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

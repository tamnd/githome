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
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/markup"
)

// readmeNames are the README candidates, tried in order, matching github.com's
// preference: a Markdown README wins over plain text, which wins over reST or
// AsciiDoc. The case variants cover the common spellings.
var readmeNames = []string{
	"README.md", "readme.md", "Readme.md",
	"README.markdown", "readme.markdown",
	"README", "readme",
	"README.txt", "readme.txt",
	"README.rst", "readme.rst",
	"README.adoc", "readme.adoc",
}

// handleREADME serves GET /repos/{owner}/{repo}/readme and
// .../readme/{dir}. The dir path parameter scopes the search to a
// subdirectory, the form octokit's repos.getReadmeInDirectory drives. The
// Accept header negotiates raw bytes or rendered HTML the same way the contents
// endpoint does.
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
		dir := strings.Trim(c.Param("dir"), "/")
		for _, name := range readmeNames {
			path := name
			if dir != "" {
				path = dir + "/" + name
			}
			res, err := d.Repos.Contents(repo, path, ref)
			if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
				continue
			}
			if err != nil {
				return err
			}
			if !res.IsDir && res.File != nil {
				writeContentFile(d, c, repo, ref, res.Entry, res.File.Content)
				return nil
			}
		}
		writeError(c.Writer(), errNotFound())
		return nil
	}
}

// contentMedia values name the file representations the contents and readme
// endpoints negotiate from the Accept header.
const (
	contentMediaJSON = iota // the default metadata + base64 object
	contentMediaRaw         // the file's raw bytes
	contentMediaHTML        // the file rendered to HTML
)

// contentMedia reads an Accept header and reports which file representation a
// client asked for: GitHub's vnd.github.raw and vnd.github.html vendor types
// (versioned, unversioned, and +json suffixed forms), defaulting to the JSON
// object. A directory listing ignores this and always returns JSON.
func contentMedia(accept string) int {
	for _, part := range strings.Split(accept, ",") {
		mt := part
		if i := strings.IndexByte(mt, ';'); i >= 0 {
			mt = mt[:i]
		}
		switch strings.ToLower(strings.TrimSpace(mt)) {
		case "application/vnd.github.raw", "application/vnd.github.v3.raw", "application/vnd.github.raw+json":
			return contentMediaRaw
		case "application/vnd.github.html", "application/vnd.github.v3.html", "application/vnd.github.html+json":
			return contentMediaHTML
		}
	}
	return contentMediaJSON
}

// writeContentFile writes a single file response, negotiating the Accept header:
// the raw bytes for vnd.github.raw, rendered HTML for vnd.github.html (when a
// markup renderer is wired), and otherwise the conditional JSON object. It is
// shared by the contents and readme endpoints so both negotiate identically.
func writeContentFile(d Deps, c *mizu.Ctx, repo *domain.Repo, ref string, entry git.PathEntry, content []byte) {
	switch contentMedia(c.Request().Header.Get("Accept")) {
	case contentMediaRaw:
		w := c.Writer()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
		return
	case contentMediaHTML:
		if d.Markup != nil {
			ref := &markup.RepoRef{Owner: repo.Owner.Login, Name: repo.Name, ID: repo.ID}
			html := d.Markup.RenderFile(c.Context(), ref, "", entry.Path, string(content))
			writeHTML(c.Writer(), http.StatusOK, string(html))
			return
		}
		// No renderer wired: fall through to the JSON object.
	}
	out := d.URLs.ContentFile(repo.Owner.Login, repo.Name, ref, entry, content)
	conditionalJSON(c.Writer(), c.Request(), http.StatusOK, out)
}

// handleArchiveRedirect serves GET /repos/{owner}/{repo}/zipball/{ref} and
// .../tarball/{ref}. GitHub answers these with a 302 to codeload rather than
// streaming the archive itself; go-github's GetArchiveLink reads that
// Location without following it. Githome has no codeload host, so the
// redirect points at its own legacy.zip / legacy.tar.gz paths (the codeload
// path shape) on the configured API base. A missing ref means the default
// branch, and the ref is resolved before redirecting so a bad one is the
// same clean 404 GitHub gives.
func handleArchiveRedirect(d Deps, format string) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		ref := c.Param("ref")
		if ref == "" {
			ref = repo.DefaultBranch
		}
		if _, err := d.Repos.GetCommit(repo, ref); err != nil {
			if gitNotFound(err) {
				writeError(c.Writer(), errNotFound())
				return nil
			}
			return err
		}
		leg := "legacy.zip"
		if format == "tar" {
			leg = "legacy.tar.gz"
		}
		segments := append([]string{"repos", repo.Owner.Login, repo.Name, leg}, strings.Split(ref, "/")...)
		c.Writer().Header().Set("Location", d.URLs.API(segments...))
		c.Writer().WriteHeader(http.StatusFound)
		return nil
	}
}

// handleZipball serves GET /repos/{owner}/{repo}/legacy.zip/{ref}, the
// redirect target of the zipball endpoint. The archive streams straight out
// of one git archive subprocess; nothing is buffered in memory, and the ref
// resolves before the first byte so a bad one is still a clean 404.
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

// handleTarball serves GET /repos/{owner}/{repo}/legacy.tar.gz/{ref}, the
// redirect target of the tarball endpoint.
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

// handleCompare serves GET /repos/{owner}/{repo}/compare/{basehead}. The
// three-dot form diffs head against the merge base, the two-dot form diffs
// the trees directly; both walk the same base..head commit range. The diff
// and patch media types in the Accept header switch the body to the raw
// unified diff or the mbox patch series. Commits page with the usual
// per_page/page knobs; the files list rides only on the first page, the way
// GitHub trims the later pages of a long comparison.
func handleCompare(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		repo, err := loadRepo(d, c)
		if repo == nil {
			return err
		}
		basehead := c.Param("basehead")
		var base, head string
		direct := false
		if b, h, ok := strings.Cut(basehead, "..."); ok {
			base, head = b, h
		} else if b, h, ok := strings.Cut(basehead, ".."); ok {
			base, head = b, h
			direct = true
		} else {
			writeError(c.Writer(), errUnprocessable("invalid comparison target: must be base...head or base..head"))
			return nil
		}
		ctx := c.Request().Context()
		owner, name := repo.Owner.Login, repo.Name

		switch pullMedia(c.Request().Header.Get("Accept")) {
		case mediaDiff:
			raw, err := d.Repos.CompareDiff(ctx, repo, base, head, false)
			if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
				writeError(c.Writer(), errNotFound())
				return nil
			}
			if err != nil {
				return err
			}
			negotiatedMediaType(c.Writer(), "diff")
			writePullText(c.Writer(), "application/vnd.github.diff; charset=utf-8", raw)
			return nil
		case mediaPatch:
			raw, err := d.Repos.ComparePatch(ctx, repo, base, head)
			if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
				writeError(c.Writer(), errNotFound())
				return nil
			}
			if err != nil {
				return err
			}
			negotiatedMediaType(c.Writer(), "patch")
			writePullText(c.Writer(), "application/vnd.github.patch; charset=utf-8", raw)
			return nil
		}

		page, perr := parsePageFor(c, "Commit")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}

		var result *domain.CompareResult
		if direct {
			result, err = d.Repos.CompareDirect(ctx, repo, base, head)
		} else {
			result, err = d.Repos.Compare(ctx, repo, base, head)
		}
		if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if err != nil {
			return err
		}

		status := "identical"
		switch {
		case result.TotalCommits > 0 && result.Behind > 0:
			status = "diverged"
		case result.TotalCommits > 0:
			status = "ahead"
		case result.Behind > 0:
			status = "behind"
		}

		paged := paginateSlice(&page, result.Commits)
		commits := make([]any, 0, len(paged))
		for _, rc := range paged {
			commits = append(commits, d.URLs.RepoCommit(owner, name, repo.ID, rc))
		}

		files := make([]any, 0, len(result.Files))
		if page.Page <= 1 {
			for _, f := range result.Files {
				files = append(files, d.URLs.PullRequestFile(owner, name, result.Head.Commit, f))
			}
		}

		var baseCommit, mergeBaseCommit any
		if result.BaseCommit.SHA != "" {
			baseCommit = d.URLs.RepoCommit(owner, name, repo.ID, result.BaseCommit)
		}
		if result.MergeBaseCommit.SHA != "" {
			mergeBaseCommit = d.URLs.RepoCommit(owner, name, repo.ID, result.MergeBaseCommit)
		}

		htmlURL := d.URLs.HTML(owner, name, "compare", basehead)
		permalink := d.URLs.HTML(owner, name, "compare",
			owner+":"+shortSHA(result.Base.Commit)+"..."+owner+":"+shortSHA(result.Head.Commit))

		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		writeJSON(c.Writer(), http.StatusOK, map[string]any{
			"url":               d.URLs.API("repos", owner, name, "compare", basehead),
			"html_url":          htmlURL,
			"permalink_url":     permalink,
			"diff_url":          htmlURL + ".diff",
			"patch_url":         htmlURL + ".patch",
			"status":            status,
			"ahead_by":          result.TotalCommits,
			"behind_by":         result.Behind,
			"total_commits":     result.TotalCommits,
			"commits":           commits,
			"base_commit":       baseCommit,
			"merge_base_commit": mergeBaseCommit,
			"files":             files,
		})
		return nil
	}
}

// shortSHA abbreviates a commit id to the seven characters GitHub uses in
// compare permalinks.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// archiveRef returns a short human-readable string for a ref suitable for an
// archive filename prefix: a full commit id shortens to seven characters the
// way GitHub abbreviates it, a qualified ref drops its refs/ prefix, and any
// slash left in a branch or tag name becomes a dash so the prefix stays a
// single path segment.
func archiveRef(ref string) string {
	ref = strings.TrimPrefix(ref, "refs/heads/")
	ref = strings.TrimPrefix(ref, "refs/tags/")
	ref = strings.TrimPrefix(ref, "heads/")
	ref = strings.TrimPrefix(ref, "tags/")
	if len(ref) == 40 && isHex(ref) {
		return ref[:7]
	}
	return strings.ReplaceAll(ref, "/", "-")
}

// isHex reports whether s is entirely lowercase hex digits, the shape of a
// full git object id.
func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
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
	r.Get("/repos/{owner}/{repo}/readme/{dir...}", handleREADME(d))
	r.Get("/repos/{owner}/{repo}/zipball", handleArchiveRedirect(d, "zip"))
	r.Get("/repos/{owner}/{repo}/zipball/{ref...}", handleArchiveRedirect(d, "zip"))
	r.Get("/repos/{owner}/{repo}/tarball", handleArchiveRedirect(d, "tar"))
	r.Get("/repos/{owner}/{repo}/tarball/{ref...}", handleArchiveRedirect(d, "tar"))
	r.Get("/repos/{owner}/{repo}/legacy.zip/{ref...}", handleZipball(d))
	r.Get("/repos/{owner}/{repo}/legacy.tar.gz/{ref...}", handleTarball(d))
	r.Get("/repos/{owner}/{repo}/compare/{basehead}", handleCompare(d))
	r.Put("/repos/{owner}/{repo}/contents/{path...}", requireScope(handleContentsCreate(d), "repo", "public_repo"))
	r.Delete("/repos/{owner}/{repo}/contents/{path...}", requireScope(handleContentsDelete(d), "repo", "public_repo"))
	r.Post("/repos/{owner}/{repo}/merges", requireScope(handleRepoMerge(d), "repo", "public_repo"))
	r.Post("/repos/{owner}/{repo}/dispatches", requireScope(handleRepoDispatch(d), "repo", "public_repo"))
	r.Get("/repositories/{id}", handleRepoByID(d))
}

// keep json import used through writeJSON helper chain
var _ = json.Marshal

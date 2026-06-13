package rest

import (
	"encoding/base64"
	"errors"
	"net/http"

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

		// GitHub tolerates the whitespace clients wrap base64 content with.
		decoded, err := base64.StdEncoding.DecodeString(stripBase64Whitespace(body.Content))
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

		// Resolve the branch here so the sha stat and the write below look at
		// the same ref; the git layer's own fallback is not the repository's
		// configured default branch.
		branch := body.Branch
		if branch == "" {
			branch = repo.DefaultBranch
		}

		// GitHub's compare-and-swap contract: updating an existing file demands
		// the current blob sha, a stale sha is a 409, and the status says which
		// of create (201) or update (200) happened.
		currentSHA, exists := currentBlobSHA(d, repo, path, branch)
		if exists && body.SHA == "" {
			writeError(c.Writer(), errShaRequired())
			return nil
		}
		if body.SHA != "" && body.SHA != currentSHA {
			writeError(c.Writer(), errShaMismatch(path, body.SHA))
			return nil
		}

		res, err := d.Repos.WriteFile(repo, domain.WriteFileInput{
			Path:           path,
			Content:        decoded,
			Message:        body.Message,
			AuthorName:     authorName,
			AuthorEmail:    authorEmail,
			Branch:         branch,
			CurrentBlobSHA: body.SHA,
		})
		if errors.Is(err, domain.ErrEmptyRepo) || errors.Is(err, domain.ErrGitNotFound) {
			// OK - new repo
			res, err = d.Repos.WriteFile(repo, domain.WriteFileInput{
				Path:        path,
				Content:     decoded,
				Message:     body.Message,
				AuthorName:  authorName,
				AuthorEmail: authorEmail,
				Branch:      branch,
			})
			if err != nil {
				return err
			}
		} else if errors.Is(err, domain.ErrConflict) {
			// The blob moved between the stat above and the write.
			writeError(c.Writer(), errShaMismatch(path, body.SHA))
			return nil
		} else if err != nil {
			return err
		}

		status := http.StatusCreated
		if exists {
			status = http.StatusOK
		}
		// Re-read what the write produced so the response carries the full
		// content object and git commit, the shape octokit and GitOps writers
		// consume instead of re-fetching.
		stat, err := d.Repos.Contents(repo, path, branch)
		if err != nil {
			return err
		}
		commit, err := d.Repos.GetCommit(repo, res.CommitSHA)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), status, map[string]any{
			"content": d.URLs.ContentEntry(repo.Owner.Login, repo.Name, branch, stat.Entry),
			"commit":  d.URLs.GitCommit(repo.Owner.Login, repo.Name, repo.ID, commit),
		})
		return nil
	}
}

// currentBlobSHA stats path on the target branch (or the default branch when
// the request names none) and reports the blob's sha and whether a file is
// there at all. A missing path, an empty repository, and a directory all read
// as "no file": the sha rules only guard file updates.
func currentBlobSHA(d Deps, repo *domain.Repo, path, branch string) (string, bool) {
	ref := branch
	if ref == "" {
		ref = repo.DefaultBranch
	}
	res, err := d.Repos.Contents(repo, path, ref)
	if err != nil || res.IsDir {
		return "", false
	}
	return string(res.Entry.SHA), true
}

// errShaRequired is GitHub's 422 for a contents PUT or DELETE that targets an
// existing file without supplying its current blob sha.
func errShaRequired() *apiError {
	return errUnprocessable("Invalid request.\n\n\"sha\" wasn't supplied.")
}

// errShaMismatch is GitHub's 409 for a supplied sha that no longer matches the
// blob at the path, the signal an octokit retry loop keys on.
func errShaMismatch(path, sha string) *apiError {
	return errConflict(path + " does not match " + sha)
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
		if body.SHA == "" {
			writeError(c.Writer(), errShaRequired())
			return nil
		}

		authorName, authorEmail := "", ""
		if body.Committer != nil {
			authorName = body.Committer.Name
			authorEmail = body.Committer.Email
		}

		branch := body.Branch
		if branch == "" {
			branch = repo.DefaultBranch
		}
		currentSHA, exists := currentBlobSHA(d, repo, path, branch)
		if !exists {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if body.SHA != currentSHA {
			writeError(c.Writer(), errShaMismatch(path, body.SHA))
			return nil
		}

		res, err := d.Repos.DeleteFile(repo, domain.WriteFileInput{
			Path:           path,
			Message:        body.Message,
			AuthorName:     authorName,
			AuthorEmail:    authorEmail,
			Branch:         branch,
			CurrentBlobSHA: body.SHA,
		})
		if errors.Is(err, domain.ErrGitNotFound) {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		if errors.Is(err, domain.ErrConflict) {
			writeError(c.Writer(), errShaMismatch(path, body.SHA))
			return nil
		}
		if err != nil {
			return err
		}

		commit, err := d.Repos.GetCommit(repo, res.CommitSHA)
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, map[string]any{
			"content": nil,
			"commit":  d.URLs.GitCommit(repo.Owner.Login, repo.Name, repo.ID, commit),
		})
		return nil
	}
}

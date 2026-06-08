package pulls

import (
	"log/slog"
	"strconv"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/view"
)

// maxDiffFiles caps how many file diffs the Files tab renders in one page. A
// sprawling pull request can touch thousands of files; rendering every hunk would
// blow the page size and the parse budget. F4 renders the first maxDiffFiles and
// flags the cap in the view so the template tells the viewer the list was
// truncated, never silently dropping the rest. The per-file pagination that loads
// the tail arrives with the review milestone (F5).
const maxDiffFiles = 300

// Files renders the PR Files-changed tab read-only: the shell plus the unified
// diff of every changed file, parsed from the per-file patch text the PR service
// yields. The diff is the three-dot range from the base tip to the head, the same
// range the .diff media type serves, so the page and the API show the identical
// change set. The inline review threads and the review state machine arrive in F5
// over this same model; F4 ships the diff for reading. A missing PR is the soft
// 404. See implementation/09 section 4.
func (h *Handlers) Files(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	pr, ok := h.loadPR(c, repo)
	if !ok {
		return nil
	}
	owner := ownerLogin(repo)

	changes, err := h.pulls.Files(ctx, h.viewer(c).pk, owner, repo.Name, pr.Number)
	if err != nil {
		if isNotFound(err) {
			return h.notFound(c)
		}
		return h.render.ServerError(c, err)
	}

	truncated := false
	if len(changes) > maxDiffFiles {
		// Do not drop the tail in silence: record what was cut so a too-large diff
		// reads as capped, not as fully rendered.
		h.logTruncatedDiff(owner, repo.Name, pr.Number, len(changes))
		changes = changes[:maxDiffFiles]
		truncated = true
	}

	files := diffFiles(changes)
	sortFilesByPath(files)

	title := pr.Title + " #" + strconv.FormatInt(pr.Number, 10)
	shell := h.shell(c, repo, pr, h.viewer(c).pk, "files", title)
	vm := view.PRFilesVM{
		Chrome:       shell.Chrome,
		Shell:        shell,
		ChangedFiles: pr.ChangedFiles,
		Additions:    pr.Additions,
		Deletions:    pr.Deletions,
		Files:        files,
		Truncated:    truncated,
	}
	return h.render.Page(c, "pulls/files", vm)
}

// logTruncatedDiff records a Files-tab cap so a truncated diff is auditable rather
// than silent. A nil logger (in a test wiring) skips the line without panicking.
func (h *Handlers) logTruncatedDiff(owner, name string, number int64, total int) {
	if h.log == nil {
		return
	}
	h.log.Warn("pulls: diff file list truncated",
		slog.String("owner", owner),
		slog.String("repo", name),
		slog.Int64("number", number),
		slog.Int("total", total),
		slog.Int("shown", maxDiffFiles),
	)
}

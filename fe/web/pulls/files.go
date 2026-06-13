package pulls

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
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

	mode := diffModeFromQuery(c)
	files := diffFiles(changes, mode)
	sortFilesByPath(files)

	vc := h.viewer(c)
	// Hang the review threads off the rows they anchor to, by the persisted
	// (path, line, side), so a thread opened in the browser and one opened with the
	// API land on the same line. A thread that no longer maps onto the diff renders
	// in its file's outdated group. A read error on the threads degrades to the diff
	// without them rather than failing the whole page.
	threads := h.loadThreadVMs(ctx, repo, pr, vc)
	files = h.attachThreads(files, threads, owner, repo.Name, pr.Number)

	title := pr.Title + " #" + strconv.FormatInt(pr.Number, 10)
	shell := h.shell(c, repo, pr, vc.pk, "files", title)
	vm := view.PRFilesVM{
		Chrome:       shell.Chrome,
		Shell:        shell,
		ChangedFiles: pr.ChangedFiles,
		Additions:    pr.Additions,
		Deletions:    pr.Deletions,
		Files:        files,
		Truncated:    truncated,
		Review:       h.reviewSurface(ctx, repo, pr, vc),
		Diff:         diffToggle(route.PullFiles(owner, repo.Name, pr.Number), mode),
	}
	return h.render.Page(c, "pulls/files", vm)
}

// loadThreadVMs reads the pull request's review threads and maps them into view
// models. With no review service wired, or on a read error, it returns no threads so
// the diff still renders read-only; the error is logged rather than failing the page,
// since the diff is the load-bearing content and the threads are an overlay on it.
func (h *Handlers) loadThreadVMs(ctx context.Context, repo *domain.Repo, pr *domain.PullRequest, vc viewerCtx) []view.ReviewThreadVM {
	if h.reviews == nil {
		return nil
	}
	owner := ownerLogin(repo)
	threads, err := h.reviews.ReviewThreads(ctx, vc.pk, owner, repo.Name, pr.Number)
	if err != nil {
		if h.log != nil {
			h.log.Warn("pulls: loading review threads failed",
				slog.String("owner", owner),
				slog.String("repo", repo.Name),
				slog.Int64("number", pr.Number),
				slog.Any("err", err),
			)
		}
		return nil
	}
	out := make([]view.ReviewThreadVM, 0, len(threads))
	for _, t := range threads {
		out = append(out, h.reviewThread(ctx, repo, pr, t, vc))
	}
	return out
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

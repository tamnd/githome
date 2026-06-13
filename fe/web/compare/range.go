package compare

import (
	"errors"
	"strings"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/git"
)

// maxCompareFiles caps how many file diffs are inlined on the compare page, the
// same ceiling the PR Files tab observes.
const maxCompareFiles = 300

// Range renders the comparison between two branches, GET
// /{owner}/{repo}/compare/{basehead...}. The basehead tail is parsed as
// "base...head" (the merge-base diff) or "base..head" (the direct diff), with
// each side optionally qualified as owner:ref or owner:repo:ref. With
// ?expand=1 the PR creation form is shown below the diff. A missing branch, a
// basehead that does not parse, or a qualified side naming another repository
// renders the soft 404. See implementation/09 section 8.
func (h *Handlers) Range(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}
	owner := ownerLogin(repo)

	basehead := c.Param("basehead")
	// The .diff and .patch suffixes select the plain-text twin of this page.
	if rest, format, ok := route.SplitPatchSuffix(basehead); ok {
		return h.rangeText(c, repo, rest, format)
	}

	spec, ok := route.ParseBaseHead(basehead)
	if !ok {
		return h.notFound(c)
	}
	// Githome has no cross-owner forks yet, so a qualified side only resolves
	// when it names this same repository; anything else is a clear 404 rather
	// than a silently wrong diff.
	if !sideInRepo(repo, spec.Base) || !sideInRepo(repo, spec.Head) {
		return h.notFound(c)
	}
	base, head := spec.Base.Ref, spec.Head.Ref

	// When no base was specified (single-branch form), fall back to the default.
	if base == "" {
		db, err := h.repos.DefaultBranchRef(repo)
		if errors.Is(err, domain.ErrEmptyRepo) || errors.Is(err, domain.ErrGitNotFound) {
			return h.notFound(c)
		}
		if err != nil {
			return h.render.ServerError(c, err)
		}
		base = db.Name
	}

	mode := diffModeFromQuery(c)
	ignoreWS := ignoreWhitespaceFromQuery(c)

	cmp, err := h.repos.CompareOpts(ctx, repo, base, head, spec.TwoDot, ignoreWS)
	if errors.Is(err, domain.ErrGitNotFound) {
		return h.notFound(c)
	}
	if err != nil {
		return h.render.ServerError(c, err)
	}

	files := buildFiles(cmp.Files, mode)
	if len(files) > maxCompareFiles {
		files = files[:maxCompareFiles]
	}

	commits := buildCommits(owner, repo.Name, cmp.Commits)

	expanded := c.Query("expand") == "1"
	sep := "..."
	if spec.TwoDot {
		sep = ".."
	}
	title := "Comparing " + base + sep + head + " · " + owner + "/" + repo.Name
	vm := view.CompareRangeVM{
		Chrome:       h.chrome(c, title),
		Header:       h.header(repo, "pulls"),
		Nav:          h.nav(repo),
		Base:         branchVM(repo, cmp.Base),
		Head:         branchVM(repo, cmp.Head),
		MergeBase:    shortSHA(cmp.MergeBase),
		Commits:      commits,
		TotalCommits: cmp.TotalCommits,
		Files:        files,
		Additions:    cmp.Additions,
		Deletions:    cmp.Deletions,
		ChangedFiles: cmp.ChangedFiles,
		HasDiff:      len(cmp.Files) > 0,
		Expanded:     expanded,
		CreateURL:    route.Pulls(owner, repo.Name, ""),
		CSRFToken:    view.CSRFFrom(ctx),
		ExpandURL:    route.CompareExpanded(owner, repo.Name, base, head),
		// The toggle re-requests this same range — its own path preserves the
		// two-dot or three-dot form and any qualified sides — flipping one diff
		// axis at a time.
		Diff: diffToggle(c.Request().URL.Path, mode, ignoreWS),
	}
	return h.render.Page(c, "compare/range", vm)
}

// rangeText serves /{owner}/{repo}/compare/{basehead}.diff and .patch: the raw
// diff of the range (merge-base form, or direct for two-dot), or the range's
// own commits as an mbox patch series. The basehead grammar and the qualified-
// side rules are the same as the HTML page's.
func (h *Handlers) rangeText(c *mizu.Ctx, repo *domain.Repo, basehead, format string) error {
	ctx := c.Context()
	spec, ok := route.ParseBaseHead(basehead)
	if !ok {
		return h.notFound(c)
	}
	if !sideInRepo(repo, spec.Base) || !sideInRepo(repo, spec.Head) {
		return h.notFound(c)
	}
	base, head := spec.Base.Ref, spec.Head.Ref
	if base == "" {
		db, err := h.repos.DefaultBranchRef(repo)
		if errors.Is(err, domain.ErrEmptyRepo) || errors.Is(err, domain.ErrGitNotFound) {
			return h.notFound(c)
		}
		if err != nil {
			return err
		}
		base = db.Name
	}

	var body []byte
	var err error
	if format == "diff" {
		body, err = h.repos.CompareDiff(ctx, repo, base, head, spec.TwoDot)
	} else {
		body, err = h.repos.ComparePatch(ctx, repo, base, head)
	}
	if errors.Is(err, domain.ErrGitNotFound) || errors.Is(err, domain.ErrEmptyRepo) {
		return h.notFound(c)
	}
	if err != nil {
		return err
	}
	w := c.Writer()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, err = w.Write(body)
	return err
}

// sideInRepo reports whether a compare side resolves inside this repository.
// The unqualified side always does; an owner- or repo-qualified side must name
// this repository's own owner and name (case-insensitively, like the URL).
// Once forks exist this is where a qualified side resolves to its fork.
func sideInRepo(repo *domain.Repo, s route.CompareSide) bool {
	if s.Owner != "" && !strings.EqualFold(s.Owner, ownerLogin(repo)) {
		return false
	}
	if s.Repo != "" && !strings.EqualFold(s.Repo, repo.Name) {
		return false
	}
	return true
}

func buildFiles(changes []git.FileChange, mode view.DiffMode) []view.DiffFileVM {
	out := make([]view.DiffFileVM, 0, len(changes))
	for _, ch := range changes {
		out = append(out, view.BuildDiffFile(
			ch.Path, ch.PrevPath,
			view.FileStatus(ch.Status),
			ch.Additions, ch.Deletions,
			ch.Patch,
			mode,
		))
	}
	return out
}

// diffModeFromQuery reads GitHub's ?diff= parameter: "split" selects the
// side-by-side view, anything else (including absent) the unified view.
func diffModeFromQuery(c *mizu.Ctx) view.DiffMode {
	if c.Request().URL.Query().Get("diff") == "split" {
		return view.DiffSplit
	}
	return view.DiffUnified
}

// ignoreWhitespaceFromQuery reads GitHub's ?w= parameter: "1" hides
// whitespace-only changes, anything else keeps them.
func ignoreWhitespaceFromQuery(c *mizu.Ctx) bool {
	return c.Request().URL.Query().Get("w") == "1"
}

// diffToggle builds the unified/split and hide-whitespace controls for the
// compare page, each URL flipping one axis while preserving the other. The base
// is the page's own range path so a control re-requests this same comparison.
func diffToggle(base string, mode view.DiffMode, ignoreWS bool) view.DiffToggleVM {
	split := mode == view.DiffSplit
	return view.DiffToggleVM{
		Split:      split,
		IgnoreWS:   ignoreWS,
		UnifiedURL: route.DiffView(base, false, ignoreWS),
		SplitURL:   route.DiffView(base, true, ignoreWS),
		ShowWSURL:  route.DiffView(base, split, false),
		HideWSURL:  route.DiffView(base, split, true),
	}
}

func buildCommits(owner, repo string, commits []git.Commit) []view.CompareCommitVM {
	out := make([]view.CompareCommitVM, 0, len(commits))
	for _, c := range commits {
		when := c.Author.When.UTC()
		out = append(out, view.CompareCommitVM{
			ShortSHA:   shortSHA(c.SHA),
			Title:      commitTitle(c.Message),
			AuthorName: c.Author.Name,
			When:       when.Format("Jan 2, 2006"),
			WhenISO:    when.Format(time.RFC3339),
			URL:        route.Commit(owner, repo, c.SHA),
		})
	}
	return out
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

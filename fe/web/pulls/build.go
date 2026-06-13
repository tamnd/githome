package pulls

import (
	"context"
	"html/template"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/markup"
)

// build.go maps domain and git data into the fe/view pull-request models. It keeps
// fe/view a pure data package by concentrating the mapping here, next to the
// handlers, and it precomputes every URL through fe/route so a template never
// builds a link. The shell is built the same way by all four tab handlers so the
// header and the tab bar are byte-identical across tabs. See implementation/09.

// ownerLogin returns the repo owner's login, tolerating a repo assembled without
// its owner.
func ownerLogin(r *domain.Repo) string {
	if r.Owner != nil {
		return r.Owner.Login
	}
	return ""
}

// numberParam parses the {number} path parameter into a positive PR number.
func numberParam(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// repoRef is the small identity every pull-request view carries.
func repoRef(r *domain.Repo) view.RepoRef {
	owner := ownerLogin(r)
	return view.RepoRef{Owner: owner, Name: r.Name, URL: route.Repo(owner, r.Name)}
}

// header builds the repo context bar with the pulls tab current, the same partial
// every repo page renders.
func (h *Handlers) header(r *domain.Repo) view.RepoHeaderVM {
	owner := ownerLogin(r)
	hdr := view.RepoHeaderVM{
		Owner:      owner,
		Name:       r.Name,
		OwnerURL:   "/" + owner,
		URL:        route.Repo(owner, r.Name),
		Private:    r.Private,
		Fork:       r.Fork,
		OpenIssues: r.OpenIssuesCount,
		ActiveTab:  "pulls",
	}
	if r.Description != nil {
		hdr.Description = *r.Description
	}
	return hdr
}

// nav builds the repo underline-nav link set, the same one every repo page shows,
// with the pulls tab among them.
func (h *Handlers) nav(r *domain.Repo) view.TreeNav {
	owner := ownerLogin(r)
	return view.TreeNav{
		CodeURL:     route.Repo(owner, r.Name),
		IssuesURL:   route.Issues(owner, r.Name, ""),
		PullsURL:    route.Pulls(owner, r.Name, ""),
		CommitsURL:  route.Commits(owner, r.Name, r.DefaultBranch, ""),
		BranchesURL: route.Branches(owner, r.Name),
		TagsURL:     route.Tags(owner, r.Name),
	}
}

// userChip maps a domain user into the small chip the timeline and rows show. A
// nil user (a ghost author whose account is gone) yields a neutral chip with no
// profile link rather than a broken one.
func (h *Handlers) userChip(u *domain.User) view.UserChipVM {
	if u == nil {
		return view.UserChipVM{Login: "ghost"}
	}
	return view.UserChipVM{
		Login:     u.Login,
		AvatarURL: h.urls.HTML("avatars", "u", strconv.FormatInt(u.ID, 10)),
		URL:       "/" + u.Login,
	}
}

// shell builds the PR shell every tab renders inside: the four-state pill, the
// merge byline refs and counts, and the precomputed tab URLs. activeTab is the
// view-only marker the tab bar keys off. It is the one place PR identity becomes a
// view model, so the header and tabs cannot drift between tabs.
func (h *Handlers) shell(c *mizu.Ctx, repo *domain.Repo, pr *domain.PullRequest, viewerPK int64, activeTab, title string) view.PRShellVM {
	owner := ownerLogin(repo)
	state := view.DerivePRState(pr.State, pr.Merged, pr.Draft)

	sh := view.PRShellVM{
		Chrome:    h.chrome(c, title),
		Header:    h.header(repo),
		Nav:       h.nav(repo),
		Repo:      repoRef(repo),
		Number:    pr.Number,
		Title:     pr.Title,
		State:     state.StateVM(),
		Author:    h.userChip(pr.User),
		OpenedAt:  pr.CreatedAt.UTC().Format("Jan 2, 2006"),
		OpenedISO: pr.CreatedAt.UTC().Format(time.RFC3339),

		BaseRef:     pr.Base.Ref,
		HeadRef:     pr.Head.Ref,
		HeadLabel:   headLabel(pr),
		IsCrossRepo: isCrossRepo(pr),

		CommitCount:  pr.CommitsCount,
		ChangedFiles: pr.ChangedFiles,
		CommentCount: pr.CommentsCount,
		Additions:    pr.Additions,
		Deletions:    pr.Deletions,

		IsMerged: pr.Merged,
		IsClosed: pr.State == "closed" && !pr.Merged,

		ActiveTab: activeTab,

		ConversationURL: route.Pull(owner, repo.Name, pr.Number),
		CommitsURL:      route.PullCommits(owner, repo.Name, pr.Number),
		ChecksURL:       route.PullChecks(owner, repo.Name, pr.Number),
		FilesURL:        route.PullFiles(owner, repo.Name, pr.Number),
	}
	// The Checks tab shows only when the checks service is wired, the same gate
	// the standalone checks page mounts behind.
	if h.checks != nil {
		sh.ShowChecksTab = true
		sh.CheckCount = h.checkCount(c, repo, pr)
	}
	if pr.MergedAt != nil {
		sh.MergedAt = pr.MergedAt.UTC().Format("Jan 2, 2006")
	}
	if canWrite(repo, viewerPK) {
		sh.CanEdit = true
		// The title edit reuses the issue title endpoint, since a PR is an issue.
		sh.EditURL = route.IssueTitle(owner, repo.Name, pr.Number)
	}
	return sh
}

// headLabel is the head ref as the byline shows it: "owner:branch" for a head in a
// different repository, the bare ref for a same-repo head.
func headLabel(pr *domain.PullRequest) string {
	if isCrossRepo(pr) && pr.Head.Label != "" {
		return pr.Head.Label
	}
	return pr.Head.Ref
}

// isCrossRepo reports whether the head lives in a different repository than the
// base, which makes the byline qualify the head with its owner.
func isCrossRepo(pr *domain.PullRequest) bool {
	if pr.Head.Repo == nil || pr.Base.Repo == nil {
		return false
	}
	return pr.Head.Repo.PK != pr.Base.Repo.PK
}

// prRow maps a domain pull request into one index row, deriving the four-state pill
// once so the list mini-icon matches the detail header.
func (h *Handlers) prRow(repo *domain.Repo, pr *domain.PullRequest) view.PRRow {
	owner := ownerLogin(repo)
	state := view.DerivePRState(pr.State, pr.Merged, pr.Draft)
	return view.PRRow{
		Number:       pr.Number,
		Title:        pr.Title,
		URL:          route.Pull(owner, repo.Name, pr.Number),
		State:        state.StateVM(),
		Author:       h.userChip(pr.User),
		OpenedAt:     pr.CreatedAt.UTC().Format("Jan 2, 2006"),
		OpenedISO:    pr.CreatedAt.UTC().Format(time.RFC3339),
		CommentCount: pr.CommentsCount,
	}
}

// diffFiles maps the PR's per-file changes into the shared diff component's file
// models. The producer yields each file's unified-diff Patch text (the same bytes
// the REST .diff media type serves); BuildDiffFile parses that text and assigns row
// kinds and positions without recomputing the diff. F4 renders unified, read-only.
func diffFiles(changes []git.FileChange, mode view.DiffMode) []view.DiffFileVM {
	out := make([]view.DiffFileVM, 0, len(changes))
	for _, ch := range changes {
		out = append(out, view.BuildDiffFile(
			ch.Path,
			ch.PrevPath,
			view.FileStatus(ch.Status),
			ch.Additions,
			ch.Deletions,
			ch.Patch,
			mode,
		))
	}
	return out
}

// fillExpandURLs points every expander row at the unfold endpoint, carrying the gap
// it covers in the query: the file path, the head SHA the lines come from, the
// per-side start lines, the count, the direction, and the diff mode so the returned
// fragment matches the table it splices into. The pure view builder fills the line
// math; this is where the route and the head SHA — neither known to fe/view — join
// it. A file with no head SHA (a pure deletion) gets no URLs, so its expanders render
// inert rather than linking nowhere.
func fillExpandURLs(files []view.DiffFileVM, owner, repo string, number int64, headSHA string, mode view.DiffMode) {
	if headSHA == "" {
		return
	}
	base := route.PullFilesExpand(owner, repo, number)
	for fi := range files {
		f := &files[fi]
		for ri := range f.Rows {
			r := &f.Rows[ri]
			if r.Expand == nil {
				continue
			}
			q := url.Values{}
			q.Set("path", f.Path)
			q.Set("sha", headSHA)
			q.Set("os", strconv.Itoa(r.Expand.OldStart))
			q.Set("ns", strconv.Itoa(r.Expand.NewStart))
			q.Set("n", strconv.Itoa(r.Expand.Count))
			q.Set("dir", r.Expand.Dir)
			if mode == view.DiffSplit {
				q.Set("diff", "split")
			}
			r.Expand.URL = base + "?" + q.Encode()
		}
	}
}

// diffModeFromQuery reads GitHub's ?diff= parameter: "split" selects the
// side-by-side view, anything else (including absent) the unified view. The
// value is display-only; it never changes the row Position the API anchors on.
func diffModeFromQuery(c *mizu.Ctx) view.DiffMode {
	if c.Request().URL.Query().Get("diff") == "split" {
		return view.DiffSplit
	}
	return view.DiffUnified
}

// diffToggle builds the unified/split control for a diff page. base is the page's
// own path; the split URL carries ?diff=split and the unified URL drops it, so the
// control flips the mode without disturbing the rest of the page.
func diffToggle(base string, mode view.DiffMode) view.DiffToggleVM {
	return view.DiffToggleVM{
		Split:      mode == view.DiffSplit,
		UnifiedURL: base,
		SplitURL:   base + "?diff=split",
	}
}

// commitGroups groups the PR's commits by authored calendar date, newest day
// first, each row carrying the short sha, the title, and the author. It mirrors the
// code-browsing history grouping so the two commit lists read alike.
func commitGroups(commits []git.Commit) []view.PRCommitDateGroup {
	type bucket struct {
		date string
		rows []view.PRCommitRow
	}
	var order []string
	byDate := map[string]*bucket{}
	for _, c := range commits {
		day := c.Author.When.UTC().Format("Jan 2, 2006")
		b, ok := byDate[day]
		if !ok {
			b = &bucket{date: day}
			byDate[day] = b
			order = append(order, day)
		}
		b.rows = append(b.rows, view.PRCommitRow{
			SHA:        c.SHA,
			ShortSHA:   shortSHA(c.SHA),
			Title:      commitTitle(c.Message),
			AuthorName: c.Author.Name,
			When:       day,
			WhenISO:    c.Author.When.UTC().Format(time.RFC3339),
		})
	}
	groups := make([]view.PRCommitDateGroup, 0, len(order))
	for _, day := range order {
		b := byDate[day]
		groups = append(groups, view.PRCommitDateGroup{Date: b.date, Commits: b.rows})
	}
	return groups
}

// shortSHA is the 7-character abbreviation the commit rows show.
func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

// commitTitle is the first line of a commit message, the row title.
func commitTitle(msg string) string {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return strings.TrimSpace(msg[:i])
	}
	return strings.TrimSpace(msg)
}

// reactionContent is one of the eight reaction kinds in canonical order, paired
// with the emoji glyph the rollup bar shows.
type reactionContent struct {
	key   string
	emoji string
}

// reactionOrder is the canonical reaction set and order github.com shows, the same
// order the issues surface uses so a PR and an issue render reactions identically.
var reactionOrder = []reactionContent{
	{"+1", "\U0001F44D"},
	{"-1", "\U0001F44E"},
	{"laugh", "\U0001F604"},
	{"hooray", "\U0001F389"},
	{"confused", "\U0001F615"},
	{"heart", "❤️"},
	{"rocket", "\U0001F680"},
	{"eyes", "\U0001F440"},
}

// reactionsRollup builds the reaction bar for a PR body or a comment. Whether the
// viewer already reacted is not in the rollup the domain returns, so Reacted stays
// false and the toggle handler resolves create-or-delete on submit, matching the
// issues surface.
func reactionsRollup(subject, toggleURL string, roll domain.ReactionRollup, canReact bool) view.ReactionsVM {
	vm := view.ReactionsVM{Subject: subject, Total: roll.TotalCount, CanReact: canReact}
	for _, rc := range reactionOrder {
		count := 0
		if roll.Counts != nil {
			count = roll.Counts[rc.key]
		}
		vm.Items = append(vm.Items, view.ReactionVM{
			Content: rc.key,
			Emoji:   rc.emoji,
			Count:   count,
			URL:     toggleURL,
		})
	}
	return vm
}

// viewerCtx carries the two viewer facts the build functions gate on: the PK for
// write access and the login for the author-owns-comment check.
type viewerCtx struct {
	pk    int64
	login string
}

// comment builds a timeline comment from a domain comment. A PR shares the issue
// number space, so its conversation comments carry the same issuecomment anchor and
// the same edit and delete targets the issues timeline uses.
func (h *Handlers) comment(ctx context.Context, repo *domain.Repo, number int64, cm *domain.Comment, vc viewerCtx) view.CommentVM {
	owner := ownerLogin(repo)
	vm := view.CommentVM{
		ID:         cm.ID,
		Author:     h.userChip(cm.User),
		Body:       h.renderBody(ctx, repo, cm.Body),
		BodySource: cm.Body,
		CreatedAt:  cm.CreatedAt.UTC().Format("Jan 2, 2006"),
		CreatedISO: cm.CreatedAt.UTC().Format(time.RFC3339),
		Edited:     cm.UpdatedAt.After(cm.CreatedAt),
		Anchor:     "issuecomment-" + strconv.FormatInt(cm.ID, 10),
		URL:        route.PullComment(owner, repo.Name, number, cm.ID),
		Reactions:  reactionsRollup("comment", route.CommentReactions(owner, repo.Name, number, cm.ID), cm.Reactions, vc.pk != 0),
	}
	if canEditComment(repo, cm, vc) {
		vm.CanEdit = true
		vm.EditURL = route.CommentEdit(owner, repo.Name, number, cm.ID)
		vm.DeleteURL = route.CommentDelete(owner, repo.Name, number, cm.ID)
	}
	return vm
}

// canEditComment reports whether the viewer may edit or delete the comment: its
// author (matched by login) or a viewer with write access, the same author-or-writer
// rule the comment service enforces. It gates the display affordance only.
func canEditComment(repo *domain.Repo, cm *domain.Comment, vc viewerCtx) bool {
	if vc.pk == 0 {
		return false
	}
	if cm.User != nil && vc.login != "" && cm.User.Login == vc.login {
		return true
	}
	return canWrite(repo, vc.pk)
}

// renderBody renders a comment or PR body to trusted HTML through the markup
// package, or returns empty HTML when markup is unconfigured so the template falls
// back to the escaped source.
func (h *Handlers) renderBody(ctx context.Context, repo *domain.Repo, src string) template.HTML {
	if h.markup == nil || strings.TrimSpace(src) == "" {
		return ""
	}
	return h.markup.RenderComment(ctx, &markup.RepoRef{Owner: ownerLogin(repo), Name: repo.Name, ID: repo.ID}, src)
}

// sortFilesByPath orders the diff files by path so the file list and the in-page
// jump links share one stable order regardless of the producer's emission order.
func sortFilesByPath(files []view.DiffFileVM) {
	sort.SliceStable(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
}

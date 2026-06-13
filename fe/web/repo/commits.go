package repo

import (
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/git"
)

// commitsPerPage bounds one history page; ?page= walks further back through
// Skip offsets (implementation/07 section 7).
const commitsPerPage = 30

// Commits renders the history view: GET /{owner}/{repo}/commits and
// /{owner}/{repo}/commits/{rest}. The tail is a ref and an optional path filter;
// unlike tree and blob, commits never auto-corrects, so the history of a deleted
// file still renders. The query narrows the walk: ?page= pages back through the
// history, ?author= matches the author's name or email, ?since= and ?until=
// bound it by commit time, and ?path= filters by path when the URL tail carries
// none. The list is grouped by calendar date. See implementation/07 section 7
// and spec sections 1.5 and 4.8.
func (h *Handlers) Commits(c *mizu.Ctx) error {
	ctx := c.Context()
	repo, ok := repoFromContext(ctx)
	if !ok {
		return h.notFound(c)
	}

	ref, rev, path := h.commitsRef(repo, c.Param("rest"))
	if ref == "" {
		// Empty repo or unknown ref: redirect to the repo home which shows the
		// quick-setup guide when there are no commits.
		return c.Redirect(303, route.Repo(ownerLogin(repo), repo.Name))
	}

	f := parseCommitsFilter(c)
	// The URL tail owns the path filter when it carries one; the ?path= query
	// covers the bare /commits URL.
	if path == "" {
		path = f.path
	} else {
		f.path = ""
	}

	// One row past the page tells whether an older page exists without a
	// second walk; the extra row never renders.
	commits, err := h.repos.ListCommits(repo, git.LogOpts{
		From:   rev,
		Path:   path,
		Skip:   (f.page - 1) * commitsPerPage,
		Max:    commitsPerPage + 1,
		Author: f.author,
		Since:  f.since,
		Until:  f.until,
	})
	if errors.Is(err, domain.ErrEmptyRepo) || errors.Is(err, domain.ErrGitNotFound) {
		return h.notFound(c)
	}
	if err != nil {
		return err
	}
	hasOlder := len(commits) > commitsPerPage
	if hasOlder {
		commits = commits[:commitsPerPage]
	}

	owner := ownerLogin(repo)
	base := route.Commits(owner, repo.Name, ref, path)
	vm := view.CommitsVM{
		Chrome:       h.chrome(c, "Commits · "+repo.FullName()),
		Header:       h.header(c.Context(), repo, "commits"),
		Nav:          h.nav(repo, ref),
		Repo:         repoRef(repo),
		Ref:          view.Ref{Name: ref, IsDefault: ref == repo.DefaultBranch},
		Path:         path,
		Groups:       groupCommitsByDate(repo, commits),
		Pager:        commitsPager(base, f, hasOlder),
		FilterAction: base,
		FilterAuthor: f.author,
		FilterSince:  f.sinceRaw,
		FilterUntil:  f.untilRaw,
	}
	return h.render.Page(c, "repo/commits", vm)
}

// commitsFilter is the parsed query narrowing one history page. sinceRaw and
// untilRaw keep the viewer's own spelling for the form echo and the pager
// links; since and until are the parsed bounds the walk consumes.
type commitsFilter struct {
	page     int
	author   string
	path     string
	sinceRaw string
	untilRaw string
	since    *time.Time
	until    *time.Time
}

// parseCommitsFilter reads the history filters off the query. A page below one
// clamps to one and an unparseable date is dropped rather than failing the
// page, the tolerant reading a hand-edited URL deserves.
func parseCommitsFilter(c *mizu.Ctx) commitsFilter {
	f := commitsFilter{page: 1, author: c.Query("author"), path: c.Query("path")}
	if n, err := strconv.Atoi(c.Query("page")); err == nil && n > 1 {
		f.page = n
	}
	if t, raw, ok := parseCommitsTime(c.Query("since"), false); ok {
		f.since, f.sinceRaw = t, raw
	}
	if t, raw, ok := parseCommitsTime(c.Query("until"), true); ok {
		f.until, f.untilRaw = t, raw
	}
	return f
}

// parseCommitsTime reads a since/until bound as a full RFC 3339 timestamp or a
// bare date. A bare until date means through the end of that day, the
// inclusive reading the date picker implies, so the bound moves to the next
// midnight.
func parseCommitsTime(v string, endOfDay bool) (*time.Time, string, bool) {
	if v == "" {
		return nil, "", false
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return &t, v, true
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return nil, "", false
	}
	if endOfDay {
		t = t.AddDate(0, 0, 1)
	}
	return &t, v, true
}

// commitsPager builds the newer/older links over the filtered walk: every
// filter survives the hop, and page one drops the page parameter so the first
// page keeps its canonical URL.
func commitsPager(base string, f commitsFilter, hasOlder bool) view.Pager {
	p := view.Pager{Page: f.page}
	if f.page > 1 {
		p.PrevURL = commitsPageURL(base, f, f.page-1)
	}
	if hasOlder {
		p.NextURL = commitsPageURL(base, f, f.page+1)
	}
	return p
}

// commitsPageURL is base with the filter query and the page number attached.
func commitsPageURL(base string, f commitsFilter, page int) string {
	q := url.Values{}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	if f.author != "" {
		q.Set("author", f.author)
	}
	if f.sinceRaw != "" {
		q.Set("since", f.sinceRaw)
	}
	if f.untilRaw != "" {
		q.Set("until", f.untilRaw)
	}
	if f.path != "" {
		q.Set("path", f.path)
	}
	if len(q) == 0 {
		return base
	}
	return base + "?" + q.Encode()
}

// commitsRef resolves the commits tail into a ref, the revision the log walk
// starts from, and an optional path. An empty tail defaults to the repository's
// head branch. A non-empty tail must name a ref; the remainder is the path
// history filter and need not exist as a current path. A tail that names no ref
// yields an empty ref, a soft 404.
func (h *Handlers) commitsRef(repo *domain.Repo, rest string) (ref, rev, path string) {
	if rest == "" {
		head, err := h.repos.DefaultBranchRef(repo)
		if err != nil {
			return "", "", ""
		}
		return head.Name, "refs/heads/" + head.Name, ""
	}
	ref, rev, path, ok := h.resolveRef(repo, h.loadRefs(repo), rest)
	if !ok {
		return "", "", ""
	}
	return ref, rev, path
}

// groupCommitsByDate projects a flat commit list into date-headed groups in the
// order the history returned them, preserving the newest-first walk.
func groupCommitsByDate(repo *domain.Repo, commits []git.Commit) []view.CommitDateGroup {
	owner := ownerLogin(repo)
	var groups []view.CommitDateGroup
	var cur *view.CommitDateGroup
	for _, c := range commits {
		date := c.Author.When.UTC().Format("Jan 2, 2006")
		if cur == nil || cur.Date != date {
			groups = append(groups, view.CommitDateGroup{Date: date})
			cur = &groups[len(groups)-1]
		}
		cur.Commits = append(cur.Commits, view.CommitRowVM{
			SHA:         c.SHA,
			ShortSHA:    shortSHA(c.SHA),
			Title:       commitTitle(c.Message),
			Body:        commitBody(c.Message),
			AuthorName:  c.Author.Name,
			AuthorEmail: c.Author.Email,
			When:        c.Author.When.UTC().Format("Jan 2, 2006"),
			BrowseURL:   route.Tree(owner, repo.Name, c.SHA, ""),
			CommitURL:   route.Commit(owner, repo.Name, c.SHA),
		})
	}
	return groups
}

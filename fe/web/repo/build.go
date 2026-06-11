package repo

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/markup"
)

// build.go maps domain and git data into the fe/view models. It keeps fe/view a
// pure data package (no domain import) by concentrating the mapping here, next to
// the handlers that use it, and it precomputes every URL through fe/route so a
// template never builds a link. See implementation/07 section 11.

// repoRef is the small identity every repo view carries.
func repoRef(r *domain.Repo) view.RepoRef {
	owner := ownerLogin(r)
	return view.RepoRef{Owner: owner, Name: r.Name, URL: route.Repo(owner, r.Name)}
}

// ownerLogin returns the repo owner's login, tolerating a repo assembled without
// its owner (which the resolver never does, but a defensive read keeps templates
// from printing an empty owner in a slash).
func ownerLogin(r *domain.Repo) string {
	if r.Owner != nil {
		return r.Owner.Login
	}
	return ""
}

// header builds the repo context bar for the given active tab.
func (h *Handlers) header(r *domain.Repo, activeTab string) view.RepoHeaderVM {
	owner := ownerLogin(r)
	hdr := view.RepoHeaderVM{
		Owner:     owner,
		Name:      r.Name,
		OwnerURL:  "/" + owner,
		URL:       route.Repo(owner, r.Name),
		Private:   r.Private,
		Fork:      r.Fork,
		ActiveTab: activeTab,
	}
	if r.Description != nil {
		hdr.Description = *r.Description
	}
	return hdr
}

// nav builds the repo underline-nav link set. The Code, Issues, Pull requests,
// Commits, Branches, and Tags tabs are the surface so far; the rest arrive with
// their milestones. The Issues and Pull requests links are the bare index URLs,
// which the default-filter views canonicalize to.
func (h *Handlers) nav(r *domain.Repo, ref string) view.TreeNav {
	owner := ownerLogin(r)
	return view.TreeNav{
		CodeURL:     route.Repo(owner, r.Name),
		IssuesURL:   route.Issues(owner, r.Name, ""),
		PullsURL:    route.Pulls(owner, r.Name, ""),
		CommitsURL:  route.Commits(owner, r.Name, ref, ""),
		BranchesURL: route.Branches(owner, r.Name),
		TagsURL:     route.Tags(owner, r.Name),
	}
}

// clone builds the clone URLs from the presenter, the same builder the REST
// surface uses, so the web and the API show the identical clone strings.
func (h *Handlers) clone(r *domain.Repo) view.CloneVM {
	owner := ownerLogin(r)
	return view.CloneVM{
		HTTP: h.urls.RepoGitHTTP(owner, r.Name),
		SSH:  h.urls.RepoGitSSH(owner, r.Name),
	}
}

// breadcrumbs builds the path breadcrumb for a tree or blob: the repo name links
// to the ref root, each directory to its tree, and the final segment is the
// current location with no link. kind selects whether the last crumb is a tree or
// a blob; the intermediate crumbs are always trees.
func breadcrumbs(r *domain.Repo, ref, p string, lastIsBlob bool) []view.Crumb {
	owner := ownerLogin(r)
	crumbs := []view.Crumb{{Name: r.Name, URL: route.Tree(owner, r.Name, ref, "")}}
	if p == "" {
		crumbs[0].Last = true
		return crumbs
	}
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		sub := strings.Join(segs[:i+1], "/")
		last := i == len(segs)-1
		c := view.Crumb{Name: seg, Last: last}
		switch {
		case last && lastIsBlob:
			c.URL = route.Blob(owner, r.Name, ref, sub)
		default:
			c.URL = route.Tree(owner, r.Name, ref, sub)
		}
		crumbs = append(crumbs, c)
	}
	return crumbs
}

// refPicker builds the branch/tag switcher, each entry carrying the URL that
// keeps the viewer on the same view kind and path. F1 renders the full bounded
// set as plain links so it switches refs with no JS.
func (h *Handlers) refPicker(r *domain.Repo, current string, kind route.RefKind, p string) view.RefPickerVM {
	owner := ownerLogin(r)
	branches, tags := h.branchTagLists(r)
	vm := view.RefPickerVM{Current: current}
	for _, b := range branches {
		vm.Branches = append(vm.Branches, view.RefChoice{
			Name:      b.Name,
			URL:       switchURL(owner, r.Name, kind, b.Name, p),
			IsCurrent: b.Name == current,
		})
	}
	for _, t := range tags {
		choice := view.RefChoice{
			Name:      t.Name,
			URL:       switchURL(owner, r.Name, kind, t.Name, p),
			IsCurrent: t.Name == current,
		}
		if t.Name == current {
			vm.IsTag = true
		}
		vm.Tags = append(vm.Tags, choice)
	}
	return vm
}

// switchURL builds the URL that switches to ref while staying on the current view
// kind and path.
func switchURL(owner, name string, kind route.RefKind, ref, p string) string {
	switch kind {
	case route.KindBlob:
		return route.Blob(owner, name, ref, p)
	case route.KindCommits:
		return route.Commits(owner, name, ref, p)
	default:
		return route.Tree(owner, name, ref, p)
	}
}

// treeEntries maps a directory listing into rows, sorted directories first then
// case-insensitively by name, which matches the github.com tree order rather than
// the Contents API's pure name sort (implementation/07 section 4).
func treeEntries(r *domain.Repo, ref string, entries []git.PathEntry) []view.TreeEntryVM {
	owner := ownerLogin(r)
	sorted := make([]git.PathEntry, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(i, j int) bool {
		di, dj := isDirEntry(sorted[i]), isDirEntry(sorted[j])
		if di != dj {
			return di
		}
		return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
	})
	out := make([]view.TreeEntryVM, 0, len(sorted))
	for _, e := range sorted {
		kind, icon := entryKindIcon(e)
		row := view.TreeEntryVM{Name: e.Name, Path: e.Path, Type: kind, Icon: icon}
		switch kind {
		case "dir":
			row.Href = route.Tree(owner, r.Name, ref, e.Path)
		case "submodule":
			// A submodule has no in-repo tree; F1 renders it without a link.
		default:
			row.Href = route.Blob(owner, r.Name, ref, e.Path)
		}
		out = append(out, row)
	}
	return out
}

// isDirEntry reports whether a path entry sorts as a directory (a tree).
func isDirEntry(e git.PathEntry) bool {
	return e.Type == git.ObjectTree
}

// entryKindIcon classifies a path entry and returns its view kind and octicon.
// A symlink is a blob with a link mode; a submodule is a commit object embedded
// in the tree.
func entryKindIcon(e git.PathEntry) (kind, icon string) {
	switch {
	case e.Type == git.ObjectTree:
		return "dir", "file-directory-fill"
	case e.Type == git.ObjectCommit:
		return "submodule", "file-submodule"
	case e.Mode == "120000":
		return "symlink", "file-symlink-file"
	default:
		return "file", "file"
	}
}

// latestCommit builds the latest-commit bar over a tree path. It reads one commit
// from the path-scoped history; a path with no history (or an empty repo) yields
// an absent summary the template hides. F1 reads this synchronously; the lazy
// per-row cells are a later optimization (implementation/07 section 4.1).
func (h *Handlers) latestCommit(r *domain.Repo, ref, p string) view.CommitSummary {
	commits, err := h.repos.ListCommits(r, git.LogOpts{From: ref, Path: p, Max: 1})
	if err != nil || len(commits) == 0 {
		return view.CommitSummary{}
	}
	c := commits[0]
	return view.CommitSummary{
		SHA:        c.SHA,
		ShortSHA:   shortSHA(c.SHA),
		Title:      commitTitle(c.Message),
		AuthorName: c.Author.Name,
		When:       c.Author.When.UTC().Format("Jan 2, 2006"),
		Present:    true,
	}
}

// readme finds and reads the preferred README in a directory listing and builds
// its view model. When a markdown README renders through the markup package, Body
// carries the trusted GFM HTML and the template shows it; the decoded Source rides
// along as the escaped fallback for the template (a non-markdown README, or markup
// unconfigured). A directory with no README, or a README that fails to read, yields
// nil so the template shows nothing.
func (h *Handlers) readme(ctx context.Context, r *domain.Repo, ref string, listing []git.PathEntry) *view.ReadmeVM {
	name := preferredReadme(listing)
	if name == "" {
		return nil
	}
	path := joinPath(currentDir(listing), name)
	res, err := h.repos.Contents(r, path, ref)
	if err != nil || res.IsDir || res.File == nil {
		return nil
	}
	if len(res.File.Content) > maxBlobDisplayBytes {
		// The same display cutoff the blob view applies: a README past it is
		// omitted rather than rendered or escaped wholesale into the tree page.
		return nil
	}
	source := string(res.File.Content)
	vm := &view.ReadmeVM{Name: name, Source: source}
	if h.markup != nil && isMarkdownName(name) {
		vm.Body = h.markup.RenderFile(ctx, h.markupRepo(r), ref, path, source)
	}
	return vm
}

// isMarkdownName reports whether a file name carries a markdown extension, the
// gate for rendering a README through the GFM pipeline. A plain README.txt or
// README without an extension stays escaped source, matching how github.com only
// auto-renders the markup variants.
func isMarkdownName(name string) bool {
	switch ext(name) {
	case "md", "markdown", "mdown", "mkdn", "mkd":
		return true
	default:
		return false
	}
}

// markupRepo builds the small repo identity the markup package resolves
// references and rewrites relative links against, keeping markup free of the
// domain package.
func (h *Handlers) markupRepo(r *domain.Repo) *markup.RepoRef {
	return &markup.RepoRef{Owner: ownerLogin(r), Name: r.Name, ID: r.ID}
}

// preferredReadme picks the README to auto-render: a case-insensitive README with
// a markup extension wins over a plain one, mirroring the resolver order the REST
// readme endpoint uses.
func preferredReadme(entries []git.PathEntry) string {
	var plain string
	for _, e := range entries {
		if e.Type != git.ObjectBlob {
			continue
		}
		switch strings.ToLower(e.Name) {
		case "readme.md", "readme.markdown":
			return e.Name
		case "readme", "readme.txt", "readme.rst":
			if plain == "" {
				plain = e.Name
			}
		}
	}
	return plain
}

// currentDir returns the directory the listing entries live in, derived from the
// first entry's path (every entry in a listing shares the same parent). The repo
// root yields the empty path.
func currentDir(entries []git.PathEntry) string {
	if len(entries) == 0 {
		return ""
	}
	if dir := path.Dir(entries[0].Path); dir != "." {
		return dir
	}
	return ""
}

// joinPath joins a directory and a name, collapsing the empty-directory case to
// the bare name so a root README reads as "README.md" not "/README.md".
func joinPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// shortSHA abbreviates an object id to the conventional seven characters.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// commitTitle is the first line of a commit message.
func commitTitle(msg string) string {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return strings.TrimSpace(msg[:i])
	}
	return strings.TrimSpace(msg)
}

// commitBody is the message after the first line, trimmed, for the expandable
// detail in a history row. An empty body yields the empty string.
func commitBody(msg string) string {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return strings.TrimSpace(msg[i+1:])
	}
	return ""
}

// humanizeBytes formats a byte count the way github.com labels file sizes.
func humanizeBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d Bytes", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

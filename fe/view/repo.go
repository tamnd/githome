package view

import "html/template"

// This file holds the repo-area view models the code-browsing handlers render:
// the repo home, the tree listing, the blob view, and the commits, branches,
// tags, and file-finder lists. They are flat presentation structs with no
// behavior and no domain import: the handler package maps domain and git data
// into them and precomputes every URL through fe/route, so a template never
// reaches past its view model. Each full-page model embeds a Chrome for the
// shell. See implementation/07 sections 3 to 11.

// RepoRef is the small identity every repo view carries: the owner login, the
// repo name, and the precomputed home URL. Templates use it for the breadcrumb
// root and the clone block heading.
type RepoRef struct {
	Owner string
	Name  string
	URL   string
}

// Ref is the resolved-ref context shared by the tree, blob, and commits views:
// the ref as it appears in the URL (a branch, tag, or sha) and the commit it
// resolves to, which the permalink and the "browse at this commit" links use.
type Ref struct {
	Name      string
	CommitSHA string
	IsDefault bool
}

// Crumb is one path breadcrumb: a label and the tree URL it links to. The last
// crumb in a list is the current location and is rendered without a link.
type Crumb struct {
	Name string
	URL  string
	Last bool
}

// RepoHeaderVM is the context bar above every repo page: the owner/name with the
// visibility pill and the fork-of line. OpenIssues feeds the counter chip on the
// Issues tab; the star/watch/fork counts stay absent until the domain tracks
// them (implementation/07 section 3.1 notes the gap).
type RepoHeaderVM struct {
	Owner       string
	Name        string
	OwnerURL    string
	URL         string
	Description string
	Private     bool
	Fork        bool
	ParentName  string // owner/name of the fork parent, empty when not a fork
	ParentURL   string
	ActiveTab   string // code | issues | pulls | commits | branches | tags, drives the underline nav
	OpenIssues  int    // open-issue count for the Issues tab counter, 0 hides it
}

// TreeNav is the per-tab link set the repo underline nav renders. It is computed
// once per repo so every repo page shows the same tabs with the same URLs. The
// Issues tab joins the set in F3 and the Pull requests tab in F4, so the code,
// commits, branches, and tags views all link into the issues and pulls surfaces
// with one shared header.
type TreeNav struct {
	CodeURL     string
	IssuesURL   string
	PullsURL    string
	CommitsURL  string
	BranchesURL string
	TagsURL     string
}

// RefPickerVM is the branch/tag switcher. It lists the refs inline (bounded) and
// each entry carries the URL that keeps the viewer on the same view kind and
// path. The entries render as plain links so the picker works with no JS. Each
// group is capped; when the cap bites, the More URL points at the full branches
// or tags page so every ref stays reachable.
type RefPickerVM struct {
	Current         string
	IsTag           bool
	Branches        []RefChoice
	Tags            []RefChoice
	MoreBranchesURL string // set when the branch list was capped
	MoreTagsURL     string // set when the tag list was capped
}

// RefChoice is one entry in the ref picker: the ref name and the URL that
// switches to it while keeping the current view kind and path.
type RefChoice struct {
	Name      string
	URL       string
	IsCurrent bool
}

// CloneVM carries the clone URLs the home and tree views show. F1 fills the HTTP
// and SSH forms from the presenter; the web download link is added with archives.
type CloneVM struct {
	HTTP string
	SSH  string
}

// CommitSummary is the latest-commit bar over a tree and each row in the commits
// list: the author, the abbreviated sha, the title, and the precomputed URLs.
type CommitSummary struct {
	SHA        string
	ShortSHA   string
	Title      string
	AuthorName string
	When       string // already formatted for the row; relativeTime handles the page
	URL        string // the single-commit URL, linked once that view ships
	Present    bool
}

// AboutVM is the repo home sidebar: the description, homepage, and topic chips.
// The license chip and the languages bar are left for the milestones that add
// their domain fields (implementation/07 section 3.1). Topics render as plain
// chips since no topic browse surface exists to link them to yet.
type AboutVM struct {
	Description string
	Homepage    string
	Topics      []string
}

// ReadmeVM is the rendered README shown under a tree. Body carries the
// GFM-rendered, sanitized HTML the markup package produced (the only path from
// file content to trusted HTML); Source is the decoded bytes the template falls
// back to as escaped preformatted text when Body is empty (a non-markdown README,
// or markup unconfigured). See implementation/07 section 3.2.
type ReadmeVM struct {
	Name   string
	Source string
	Body   template.HTML // empty for a non-markdown README; the template falls back to Source
}

// RepoHomeVM is the repo home: the header, the default-root tree, the About
// sidebar, and the README.
type RepoHomeVM struct {
	Chrome Chrome
	Header RepoHeaderVM
	Nav    TreeNav
	Tree   TreeVM
	About  AboutVM
	Readme *ReadmeVM // nil when the root has no README
}

// QuickSetupVM is the empty-repo home: the header plus the clone-and-push setup
// blocks instead of a tree. See implementation/07 section 1.11.
type QuickSetupVM struct {
	Chrome Chrome
	Header RepoHeaderVM
	Nav    TreeNav
	Clone  CloneVM
}

// TreeVM is a directory listing at a ref: the breadcrumb, the ref picker, the
// latest-commit bar, the entries (directories first), and an optional README.
type TreeVM struct {
	Chrome    Chrome
	Header    RepoHeaderVM
	Nav       TreeNav
	Repo      RepoRef
	Ref       Ref
	Path      string
	Crumbs    []Crumb
	RefPicker RefPickerVM
	Latest    CommitSummary
	Entries   []TreeEntryVM
	Readme    *ReadmeVM
	Clone     CloneVM
	Embedded  bool // true when rendered inside the home page (skips the shell parts)
}

// TreeEntryVM is one row in a tree listing: the name, the type-driven octicon,
// and the precomputed URL (a directory links to /tree, a file to /blob).
type TreeEntryVM struct {
	Name          string
	Path          string
	Type          string // dir | file | symlink | submodule
	Icon          string // the octicon name for the type
	Href          string
	SymlinkTarget string
	SubmoduleURL  string
}

// BlobVM is the single-file view. The handler classifies the blob into a kind
// that selects the body: text and svg carry the per-line highlighted HTML with
// stable ids so the line anchors resolve, a markdown blob (not viewed with
// ?plain=1) carries Body as rendered GFM, and an image or pdf embeds from the raw
// URL. See implementation/07 sections 5.1 and 5.2.
type BlobVM struct {
	Chrome    Chrome
	Header    RepoHeaderVM
	Nav       TreeNav
	Repo      RepoRef
	Ref       Ref
	Path      string
	Crumbs    []Crumb
	RefPicker RefPickerVM
	Name      string
	Kind      string        // text | markdown | image | pdf | binary | svg | toolarge
	Lang      string        // the highlighter grammar label, shown in the blob header
	Lines     []BlobLine    // the highlighted source lines for a text or svg blob
	Body      template.HTML // the rendered GFM for a markdown blob (Kind == "markdown")
	RawText   string
	LineCount int
	Size      int64
	SizeLabel string
	RawURL    string
	Plain     bool
	Truncated bool
}

// BlobLine is one source line: its 1-based number (the id="L{n}" anchor) and the
// highlighted HTML the markup highlighter produced. Text is trusted markup: the
// source text is HTML-escaped and only the pl-* token spans are raw, so the
// template emits it without re-escaping. A blob in an unknown language degrades
// to the escaped line with no spans.
type BlobLine struct {
	Number int
	Text   template.HTML
}

// CommitsVM is the history list, grouped by calendar date in the viewer's view.
// Pager carries the newer/older hops with the filters preserved; the Filter
// fields echo the query back into the filter form.
type CommitsVM struct {
	Chrome Chrome
	Header RepoHeaderVM
	Nav    TreeNav
	Repo   RepoRef
	Ref    Ref
	Path   string
	Groups []CommitDateGroup
	Pager  Pager

	FilterAction string // the GET form target, the page's own canonical URL
	FilterAuthor string
	FilterSince  string
	FilterUntil  string
}

// CommitDateGroup is one day's heading and the commits authored that day.
type CommitDateGroup struct {
	Date    string
	Commits []CommitRowVM
}

// CommitRowVM is one row in the history: the message title and body, the author,
// the abbreviated sha, and the precomputed browse and copy URLs.
type CommitRowVM struct {
	SHA         string
	ShortSHA    string
	Title       string
	Body        string
	AuthorName  string
	AuthorEmail string
	When        string
	BrowseURL   string // tree at this commit
	CommitURL   string // single-commit detail
}

// BranchesVM is the branch overview: the default branch first, then the rest.
type BranchesVM struct {
	Chrome  Chrome
	Header  RepoHeaderVM
	Nav     TreeNav
	Repo    RepoRef
	Default string
	Items   []BranchRowVM
}

// BranchRowVM is one branch row: the name and the precomputed tree and history
// URLs. The ahead/behind counts and PR status arrive with the compare domain.
type BranchRowVM struct {
	Name       string
	IsDefault  bool
	TreeURL    string
	CommitsURL string
}

// TagsVM is the tag overview, version-aware descending.
type TagsVM struct {
	Chrome Chrome
	Header RepoHeaderVM
	Nav    TreeNav
	Repo   RepoRef
	Items  []TagRowVM
}

// TagRowVM is one tag row: the name, the commit it points at, the tree URL,
// and the source-archive download links.
type TagRowVM struct {
	Name     string
	ShortSHA string
	Message  string
	TreeURL  string
	ZipURL   string
	TarGzURL string
}

// FileFinderVM is the fuzzy file index at a ref: the flattened file list as plain
// links, plus a truncation flag the handler logs when the recursive tree is
// capped. See implementation/07 section 10.4.
type FileFinderVM struct {
	Chrome    Chrome
	Header    RepoHeaderVM
	Nav       TreeNav
	Repo      RepoRef
	Ref       string
	Files     []FinderEntry
	Truncated bool
}

// FinderEntry is one file in the finder: the path and its blob URL.
type FinderEntry struct {
	Path string
	URL  string
}

// BlameLineVM is one annotated source line in the blame view. NewGroup is true
// when this line opens a new commit hunk, which the template uses to show the
// commit metadata once per group rather than repeating it on every line.
type BlameLineVM struct {
	LineNum    int
	Text       string // raw source text (HTML-escaped in template)
	SHA        string
	ShortSHA   string // first 7 chars
	AuthorName string
	When       string // human-readable date e.g. "Jan 2, 2006"
	CommitURL  string
	NewGroup   bool // true when this line starts a new commit group
}

// BlameVM is the line-by-line blame view: every source line annotated with the
// commit that last changed it. BlobURL links back to the normal blob view.
type BlameVM struct {
	Chrome  Chrome
	Header  RepoHeaderVM
	Nav     TreeNav
	Repo    RepoRef
	Ref     Ref
	Path    string
	Lines   []BlameLineVM
	BlobURL string // link back to the blob view
}

// CommitVM is the single-commit view: the commit metadata, parent links, and
// the rendered unified diff.
type CommitVM struct {
	Chrome      Chrome
	Header      RepoHeaderVM
	Nav         TreeNav
	Repo        RepoRef
	SHA         string
	ShortSHA    string
	Title       string
	Body        string
	AuthorName  string
	AuthorEmail string
	When        string
	ParentSHAs  []string // short SHAs; empty for the initial commit
	ParentURLs  []string // tree browse URL for each parent
	// Files is the commit's diff rendered through the shared per-file diff
	// component, the same one the PR Files tab and the compare page use.
	Files []DiffFileVM
	// FilesTruncated marks Files as the bounded head of a larger change; the
	// template shows a note pointing at the browse view for the rest.
	FilesTruncated bool
	FilesCount     int          // number of files changed, counted before the cap
	Additions      int          // total added lines across all files
	Deletions      int          // total deleted lines across all files
	CommitsURL     string       // back link to the history page
	TreeURL        string       // tree at this commit
	Diff           DiffToggleVM // the unified/split and hide-whitespace controls
}

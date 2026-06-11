package repo

import (
	"sort"
	"strconv"
	"strings"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/fe/view"
	"github.com/tamnd/githome/git"
)

// Tags renders the tag overview: GET /{owner}/{repo}/tags. Tags sort version-aware
// descending, so v2.0.0 precedes v1.9.0 and a non-version tag falls back to
// reverse lexical order. Each row links the tree at the tag, shows the annotated
// message when present, and carries the zip and tar.gz download links the
// archive endpoint serves. See implementation/07 section 10.2.
func (h *Handlers) Tags(c *mizu.Ctx) error {
	repo, ok := repoFromContext(c.Context())
	if !ok {
		return h.notFound(c)
	}
	tags, err := h.repos.ListTags(repo)
	if err != nil {
		return err
	}

	sortTagsVersionDesc(tags)
	owner := ownerLogin(repo)
	rows := make([]view.TagRowVM, 0, len(tags))
	for _, t := range tags {
		row := view.TagRowVM{
			Name:     t.Name,
			ShortSHA: shortSHA(t.Commit),
			TreeURL:  route.Tree(owner, repo.Name, t.Name, ""),
			// The qualified refs/tags/ form keeps the download on the tag even
			// when a branch shares its name.
			ZipURL:   route.ArchiveZip(owner, repo.Name, "refs/tags/"+t.Name),
			TarGzURL: route.ArchiveTarGz(owner, repo.Name, "refs/tags/"+t.Name),
		}
		if t.Annotated != nil {
			row.Message = commitTitle(t.Annotated.Message)
		}
		rows = append(rows, row)
	}

	vm := view.TagsVM{
		Chrome: h.chrome(c, "Tags · "+repo.FullName()),
		Header: h.header(repo, "tags"),
		Nav:    h.nav(repo, repo.DefaultBranch),
		Repo:   repoRef(repo),
		Items:  rows,
	}
	return h.render.Page(c, "repo/tags", vm)
}

// sortTagsVersionDesc orders tags newest-version first. A tag that parses as a
// dotted version compares numerically segment by segment; a pair where either
// side does not parse falls back to reverse lexical order, which keeps the sort
// total and stable.
func sortTagsVersionDesc(tags []git.Tag) {
	sort.SliceStable(tags, func(i, j int) bool {
		vi, oki := parseVersion(tags[i].Name)
		vj, okj := parseVersion(tags[j].Name)
		if oki && okj {
			return compareVersions(vi, vj) > 0
		}
		return tags[i].Name > tags[j].Name
	})
}

// parseVersion extracts the dotted numeric components of a tag name, tolerating a
// leading v and ignoring a pre-release suffix after a dash. It reports ok false
// when there is no leading numeric component to compare.
func parseVersion(name string) ([]int, bool) {
	s := strings.TrimPrefix(name, "v")
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, len(out) > 0
}

// compareVersions compares two parsed versions component by component, treating a
// missing trailing component as zero so 1.2 and 1.2.0 compare equal.
func compareVersions(a, b []int) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

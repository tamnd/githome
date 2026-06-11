package repo

import (
	"strings"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe/route"
	"github.com/tamnd/githome/git"
)

// refSet is a repository's branch and tag lists, read once per request and
// shared by every consumer in that request: the ref-path splitter and the ref
// picker used to enumerate the refs independently, costing two full ref reads
// each per tree/blob page. The name maps answer membership for the splitter,
// branches apart from tags so the split can keep the branch-beats-tag
// precedence; the ordered lists feed the picker.
type refSet struct {
	branches    []git.Branch
	tags        []git.Tag
	branchNames map[string]bool
	tagNames    map[string]bool
}

// loadRefs reads the repository's branches and tags once. An empty repository
// (or a read error on either list) yields the empty set for that list, which
// makes every ref-path tail a soft 404, the correct answer for a repo with no
// commits.
func (h *Handlers) loadRefs(repo *domain.Repo) *refSet {
	rs := &refSet{branchNames: map[string]bool{}, tagNames: map[string]bool{}}
	if branches, err := h.repos.ListBranches(repo); err == nil {
		rs.branches = branches
		for _, b := range branches {
			rs.branchNames[b.Name] = true
		}
	}
	if tags, err := h.repos.ListTags(repo); err == nil {
		rs.tags = tags
		for _, t := range tags {
			rs.tagNames[t.Name] = true
		}
	}
	return rs
}

// refLookup adapts the request's shared ref set to the route.RefLookup the
// ref-path splitter consumes. Branch and tag membership answer from the
// prebuilt maps so the split stays free of per-candidate git reads; only a
// candidate that can be a commit-ish (the symbolic HEAD or a hex object id,
// never anything with a slash) pays the commit lookup. See implementation/07
// section 2.
type refLookup struct {
	h    *Handlers
	repo *domain.Repo
	refs *refSet
}

func (l refLookup) Branch(name string) bool { return l.refs.branchNames[name] }
func (l refLookup) Tag(name string) bool    { return l.refs.tagNames[name] }

func (l refLookup) Commitish(rev string) bool {
	if strings.Contains(rev, "/") {
		return false
	}
	if rev != "HEAD" && !looksLikeSHA(rev) {
		return false
	}
	_, err := l.h.repos.GetCommit(l.repo, rev)
	return err == nil
}

// gitRev returns the revision the git layer reads for a matched ref. The web
// route promises the branch when a name is both a branch and a tag, but git's
// own rev-parse order prefers the tag, so a bare name handed down would flip
// the choice. Qualifying the matched name pins it; a commit-ish passes
// through as written.
func gitRev(ref string, kind route.RefMatch) string {
	switch kind {
	case route.RefBranch:
		return "refs/heads/" + ref
	case route.RefTag:
		return "refs/tags/" + ref
	default:
		return ref
	}
}

// resolveCommitish resolves a revision (a branch, tag, full or abbreviated sha,
// or HEAD) to its full commit sha. It backs the file finder and the short-sha
// canonicalization. ok is false when the revision does not resolve.
func (h *Handlers) resolveCommitish(repo *domain.Repo, rev string) (full string, ok bool) {
	c, err := h.repos.GetCommit(repo, rev)
	if err != nil {
		return "", false
	}
	return c.SHA, true
}

// looksLikeSHA reports whether s is a plausible abbreviated or full object id: at
// least seven hex digits and all hex. Git's own minimum abbreviation is four, but
// seven is the conventional floor and avoids treating a short word as a sha.
func looksLikeSHA(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

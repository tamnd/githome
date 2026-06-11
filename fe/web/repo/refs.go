package repo

import (
	"strings"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
)

// refSet is a repository's branch and tag lists, read once per request and
// shared by every consumer in that request: the ref-path splitter and the ref
// picker used to enumerate the refs independently, costing two full ref reads
// each per tree/blob page. names answers membership for the splitter; the
// ordered lists feed the picker.
type refSet struct {
	branches []git.Branch
	tags     []git.Tag
	names    map[string]bool
}

// loadRefs reads the repository's branches and tags once. An empty repository
// (or a read error on either list) yields the empty set for that list, which
// makes every ref-path tail a soft 404, the correct answer for a repo with no
// commits.
func (h *Handlers) loadRefs(repo *domain.Repo) *refSet {
	rs := &refSet{names: map[string]bool{}}
	if branches, err := h.repos.ListBranches(repo); err == nil {
		rs.branches = branches
		for _, b := range branches {
			rs.names[b.Name] = true
		}
	}
	if tags, err := h.repos.ListTags(repo); err == nil {
		rs.tags = tags
		for _, t := range tags {
			rs.names[t.Name] = true
		}
	}
	return rs
}

// refExists returns the predicate SplitRefPath consumes to peel a "<ref>/<path>"
// tail. It answers each candidate from the request's shared ref set, falling
// back to a commit lookup only for a candidate that looks like an object id.
// SplitRefPath calls the predicate once per leading-segment length, longest
// first, so answering from the prebuilt set keeps the split free of per-candidate
// git reads. See implementation/07 section 2.
func (h *Handlers) refExists(repo *domain.Repo, refs *refSet) func(string) bool {
	return func(candidate string) bool {
		if candidate == "" {
			return false
		}
		if refs.names[candidate] {
			return true
		}
		// A single-segment, hex-looking candidate may be a full or abbreviated
		// commit sha. Only then is the extra commit lookup worth it: a ref with a
		// slash is never a bare sha, and a non-hex token never resolves.
		if !strings.Contains(candidate, "/") && looksLikeSHA(candidate) {
			if _, err := h.repos.GetCommit(repo, candidate); err == nil {
				return true
			}
		}
		return false
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

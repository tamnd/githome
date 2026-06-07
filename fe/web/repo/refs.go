package repo

import (
	"strings"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
)

// refExists returns the predicate SplitRefPath consumes to peel a "<ref>/<path>"
// tail. It resolves the repository's branch and tag names once (two git reads),
// then answers each candidate from that set, falling back to a commit lookup only
// for a candidate that looks like an object id. SplitRefPath calls the predicate
// once per leading-segment length, longest first, so answering from a prebuilt
// set keeps the split to those two reads rather than one open per candidate. See
// implementation/07 section 2.
func (h *Handlers) refExists(repo *domain.Repo) func(string) bool {
	names := h.refNames(repo)
	return func(candidate string) bool {
		if candidate == "" {
			return false
		}
		if names[candidate] {
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

// refNames returns the set of the repository's branch and tag short names. An
// empty repository yields an empty set, which makes every tail a soft 404, the
// correct answer for a repo with no commits.
func (h *Handlers) refNames(repo *domain.Repo) map[string]bool {
	set := map[string]bool{}
	branches, err := h.repos.ListBranches(repo)
	if err == nil {
		for _, b := range branches {
			set[b.Name] = true
		}
	}
	tags, err := h.repos.ListTags(repo)
	if err == nil {
		for _, t := range tags {
			set[t.Name] = true
		}
	}
	return set
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

// branchTagLists returns the bounded branch and tag name lists for the ref
// picker, branches first by the git layer's order, tags after. F1 renders the
// full set as plain links; the typed filter fragment is a later enhancement.
func (h *Handlers) branchTagLists(repo *domain.Repo) (branches []git.Branch, tags []git.Tag) {
	branches, err := h.repos.ListBranches(repo)
	if err != nil {
		branches = nil
	}
	tags, err = h.repos.ListTags(repo)
	if err != nil {
		tags = nil
	}
	return branches, tags
}

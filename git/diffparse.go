package git

import (
	"strings"
	"time"
)

// Helpers shared by the merge surface: parsing a unified diff into per-file
// changes, and the small time and string conversions commit-tree and the diff
// readers need.

// parseGitTime reads a strict ISO 8601 timestamp (%aI / %cI), the form the
// commit listing requests. An unparseable value yields the zero time rather
// than an error, since a malformed date should not fail a whole listing.
func parseGitTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t
}

// gitDate formats a signature time for the GIT_AUTHOR_DATE / GIT_COMMITTER_DATE
// environment commit-tree reads. RFC3339 is one of the formats git parses.
func gitDate(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// firstLine returns the first line of s with its trailing newline removed, used
// to pluck the tree id off merge-tree's output.
func firstLine(s string) string {
	if before, _, ok := strings.Cut(s, "\n"); ok {
		return before
	}
	return strings.TrimRight(s, "\n")
}

// isZeroOID reports whether sha is the all-zero object id git writes for the
// absent side of an add or a delete.
func isZeroOID(sha string) bool {
	if sha == "" {
		return true
	}
	return strings.Trim(sha, "0") == ""
}

// stripDiffPrefix removes the a/ or b/ path prefix git puts on the --- and +++
// lines of a diff.
func stripDiffPrefix(p string) string {
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		return p[2:]
	}
	return p
}

// parseDiff turns a full-index unified diff into one FileChange per file. It
// reads each file's status from the extended header (new file, deleted file,
// rename, copy), its blob id from the index line, and its line counts and patch
// text from the hunks. Binary files carry no patch and zero counts.
func parseDiff(text string) []FileChange {
	var (
		files          []FileChange
		cur            *FileChange
		oldSHA, newSHA string
		patch          []string
		inHunk         bool
	)
	flush := func() {
		if cur == nil {
			return
		}
		if !isZeroOID(newSHA) {
			cur.SHA = newSHA
		} else {
			cur.SHA = oldSHA
		}
		cur.Patch = strings.TrimRight(strings.Join(patch, "\n"), "\n")
		files = append(files, *cur)
	}
	for ln := range strings.SplitSeq(text, "\n") {
		switch {
		case strings.HasPrefix(ln, "diff --git "):
			flush()
			cur = &FileChange{Status: "modified"}
			oldSHA, newSHA, patch, inHunk = "", "", nil, false
		case cur == nil:
			// Skip anything before the first file header.
		case strings.HasPrefix(ln, "@@"):
			inHunk = true
			patch = append(patch, ln)
		case inHunk:
			patch = append(patch, ln)
			switch {
			case strings.HasPrefix(ln, "+") && !strings.HasPrefix(ln, "+++"):
				cur.Additions++
			case strings.HasPrefix(ln, "-") && !strings.HasPrefix(ln, "---"):
				cur.Deletions++
			}
		case strings.HasPrefix(ln, "new file mode"):
			cur.Status = "added"
		case strings.HasPrefix(ln, "deleted file mode"):
			cur.Status = "removed"
		case strings.HasPrefix(ln, "rename from "):
			cur.Status = "renamed"
			cur.PrevPath = strings.TrimPrefix(ln, "rename from ")
		case strings.HasPrefix(ln, "rename to "):
			cur.Path = strings.TrimPrefix(ln, "rename to ")
		case strings.HasPrefix(ln, "copy from "):
			cur.Status = "copied"
			cur.PrevPath = strings.TrimPrefix(ln, "copy from ")
		case strings.HasPrefix(ln, "copy to "):
			cur.Path = strings.TrimPrefix(ln, "copy to ")
		case strings.HasPrefix(ln, "index "):
			rest := strings.SplitN(strings.TrimPrefix(ln, "index "), " ", 2)[0]
			if a, b, ok := strings.Cut(rest, ".."); ok {
				oldSHA, newSHA = a, b
			}
		case strings.HasPrefix(ln, "--- "):
			if p := strings.TrimPrefix(ln, "--- "); cur.Path == "" && p != "/dev/null" {
				cur.Path = stripDiffPrefix(p)
			}
		case strings.HasPrefix(ln, "+++ "):
			if p := strings.TrimPrefix(ln, "+++ "); p != "/dev/null" {
				cur.Path = stripDiffPrefix(p)
			}
		}
	}
	flush()
	return files
}

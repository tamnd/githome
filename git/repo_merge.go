package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// The merge surface a pull request rests on, all of it shelling out to the git
// binary like the rest of the write path. Mergeability is a test merge that
// touches no refs (merge-tree --write-tree); the three merge strategies build
// new commit objects with commit-tree and return the resulting tip, leaving the
// caller to advance the base ref. The diff and commit listing back the pull
// request's files, commits, and .diff/.patch media.

// MergeMethod is one of GitHub's three ways to land a pull request.
type MergeMethod string

// The merge strategies. merge writes a two-parent merge commit; squash collapses
// the head into one commit on the base; rebase replays the head's commits onto
// the base tip.
const (
	MergeCommit MergeMethod = "merge"
	MergeSquash MergeMethod = "squash"
	MergeRebase MergeMethod = "rebase"
)

// FileChange is one file's diff between two commits, the shape the pull request
// files endpoint renders. Status is GitHub's vocabulary (added, removed,
// modified, renamed, copied, changed). SHA is the blob id of the new side, or
// the old side for a deletion. Patch is the unified hunk text for that file,
// empty for a binary file.
type FileChange struct {
	Path      string
	PrevPath  string // set for a rename or copy
	Status    string
	Additions int
	Deletions int
	SHA       SHA
	Patch     string
}

// MergeBase returns the best common ancestor of a and b. ok is false when the
// two commits share no history, the case a cross-repository head with no merge
// base produces.
func (s *Store) MergeBase(ctx context.Context, pk int64, a, b SHA) (sha SHA, ok bool, err error) {
	args := []string{"merge-base", a, b}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return "", false, err
	}
	switch r.code {
	case 0:
		return strings.TrimSpace(string(r.stdout)), true, nil
	case 1:
		return "", false, nil
	default:
		return "", false, fail(args, r)
	}
}

// AheadBehind reports how far head is ahead of and behind base: ahead counts the
// commits in head not reachable from base, behind the commits in base not
// reachable from head. It is the comparison the pull request and the BEHIND
// merge state read.
func (s *Store) AheadBehind(ctx context.Context, pk int64, base, head SHA) (ahead, behind int, err error) {
	args := []string{"rev-list", "--left-right", "--count", base + "..." + head}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return 0, 0, err
	}
	if r.code != 0 {
		return 0, 0, fail(args, r)
	}
	// Output is "<left>\t<right>": left is base-only (behind), right head-only (ahead).
	fields := strings.Fields(string(r.stdout))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("git rev-list --left-right --count: unexpected output %q", string(r.stdout))
	}
	behind, err = strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, err
	}
	ahead, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, err
	}
	return ahead, behind, nil
}

// CommitsBetween returns the commits in head that are not in base, oldest first,
// the list the pull request commits endpoint pages over. It is empty when head
// is an ancestor of base.
func (s *Store) CommitsBetween(ctx context.Context, pk int64, base, head SHA) ([]Commit, error) {
	return s.CommitsBetweenN(ctx, pk, base, head, 0)
}

// CommitsBetweenN is CommitsBetween bounded to at most max commits (the newest
// max of the range, still returned oldest first), so a compare across thousands
// of commits never loads the whole range into memory. max <= 0 is unbounded.
func (s *Store) CommitsBetweenN(ctx context.Context, pk int64, base, head SHA, max int) ([]Commit, error) {
	args := []string{"log", "--reverse", "--pretty=format:" + logRecordFormat}
	if max > 0 {
		args = append(args, "-n", strconv.Itoa(max))
	}
	args = append(args, base+".."+head)
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return nil, err
	}
	if r.code != 0 {
		return nil, fail(args, r)
	}
	return parseLogRecords(string(r.stdout)), nil
}

// PushCommits lists the commits a pushed range introduced, for the push webhook
// body: the newest commits up to limit, returned oldest first the way GitHub
// orders them (--reverse flips the output after -n limiting, so a capped walk
// keeps the newest commits). An empty before walks from after alone, the
// new-ref case where no old tip bounds the range.
func (s *Store) PushCommits(ctx context.Context, pk int64, before, after SHA, limit int) ([]Commit, error) {
	rng := after
	if before != "" {
		rng = before + ".." + after
	}
	args := []string{"log", "--reverse", "--pretty=format:" + logRecordFormat, "-n", strconv.Itoa(limit), rng}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return nil, err
	}
	if r.code != 0 {
		return nil, fail(args, r)
	}
	return parseLogRecords(string(r.stdout)), nil
}

// logRecordFormat is the machine-readable git log pretty format the subprocess
// log readers share: NUL-separated fields, \x1e-terminated records, via git's
// %x00 / %x1e placeholders so the separators land in the output without putting
// NUL bytes in argv, which exec rejects.
var logRecordFormat = strings.Join([]string{
	"%H", "%T", "%P", "%an", "%ae", "%aI", "%cn", "%ce", "%cI", "%B",
}, "%x00") + "%x1e"

// parseLogRecords parses logRecordFormat output into commits, in output order.
func parseLogRecords(out string) []Commit {
	const recSep = "\x1e"
	const fieldSep = "\x00"
	var commits []Commit
	for rec := range strings.SplitSeq(out, recSep) {
		rec = strings.Trim(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, fieldSep)
		if len(f) < 10 {
			continue
		}
		c := Commit{
			SHA:       f[0],
			Tree:      f[1],
			Author:    Signature{Name: f[3], Email: f[4], When: parseGitTime(f[5])},
			Committer: Signature{Name: f[6], Email: f[7], When: parseGitTime(f[8])},
			Message:   strings.TrimRight(f[9], "\n"),
		}
		if p := strings.Fields(f[2]); len(p) > 0 {
			c.Parents = p
		}
		commits = append(commits, c)
	}
	return commits
}

// LastCommitForPath returns the newest commit at rev touching path (the whole
// tree when path is empty). ok is false when nothing matches: an unborn ref, a
// bad revision, or a path with no history. It is one bounded git log -1
// subprocess; unlike a go-git PathFilter walk it rides the commit-graph and
// pathspec machinery, so a path last touched far in the past does not walk the
// whole history in process.
func (s *Store) LastCommitForPath(ctx context.Context, pk int64, rev, path string) (c Commit, ok bool, err error) {
	args := []string{"log", "-1", "--pretty=format:" + logRecordFormat, "--end-of-options", rev}
	if path != "" {
		args = append(args, "--", path)
	}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return Commit{}, false, err
	}
	if r.code != 0 {
		// A missing rev (or an empty repository) is an absent answer for the
		// latest-commit bar, not an infrastructure failure.
		return Commit{}, false, nil
	}
	commits := parseLogRecords(string(r.stdout))
	if len(commits) == 0 {
		return Commit{}, false, nil
	}
	return commits[0], true, nil
}

// CommitFiles returns the paths a commit added, removed, and modified relative
// to its first parent (or the empty tree for a root commit), the per-commit
// file lists a push webhook body carries.
func (s *Store) CommitFiles(ctx context.Context, pk int64, sha SHA) (added, removed, modified []string, err error) {
	args := []string{"diff-tree", "--no-commit-id", "--name-status", "-r", "--root", "-m", "--first-parent", sha}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return nil, nil, nil, err
	}
	if r.code != 0 {
		return nil, nil, nil, fail(args, r)
	}
	added, removed, modified = []string{}, []string{}, []string{}
	for line := range strings.SplitSeq(strings.TrimRight(string(r.stdout), "\n"), "\n") {
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "\t", 2)
		if len(f) < 2 || f[0] == "" {
			continue
		}
		path := f[1]
		// Rename detection is off here, so renames arrive as an add plus a
		// delete, which is also how GitHub lists them. Stay safe anyway and
		// take the destination path if a two-path line ever shows up.
		if i := strings.LastIndexByte(path, '\t'); i >= 0 {
			path = path[i+1:]
		}
		switch f[0][0] {
		case 'A':
			added = append(added, path)
		case 'D':
			removed = append(removed, path)
		default:
			modified = append(modified, path)
		}
	}
	return added, removed, modified, nil
}

// DiffStat totals the additions, deletions, and changed file count between base
// and head over the three-dot range (merge base to head), matching the counts a
// pull request reports.
func (s *Store) DiffStat(ctx context.Context, pk int64, base, head SHA) (additions, deletions, changed int, err error) {
	args := []string{"diff", "--numstat", "--find-renames", base + "..." + head}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return 0, 0, 0, err
	}
	if r.code != 0 {
		return 0, 0, 0, fail(args, r)
	}
	for line := range strings.SplitSeq(strings.TrimRight(string(r.stdout), "\n"), "\n") {
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "\t", 3)
		if len(f) < 3 {
			continue
		}
		changed++
		// A binary file reports "-" for both counts; treat it as zero.
		if a, e := strconv.Atoi(f[0]); e == nil {
			additions += a
		}
		if d, e := strconv.Atoi(f[1]); e == nil {
			deletions += d
		}
	}
	return additions, deletions, changed, nil
}

// ChangedFiles returns the per-file diff between base and head over the
// three-dot range, parsed from a single full-index patch so each file carries
// its status, blob id, line counts, and hunk text in one pass. When both ends
// are full object ids the parsed diff is served from a content-addressed LRU,
// so the second ask for a range (the review-thread indexer right after the
// Files page, or a compare reload) skips the git subprocess entirely.
func (s *Store) ChangedFiles(ctx context.Context, pk int64, base, head SHA) ([]FileChange, error) {
	return s.changedFiles(ctx, pk, base, head, false, false)
}

// ChangedFilesDirect is ChangedFiles over the two-dot form: the diff between
// the two trees themselves rather than between head and the merge base, so
// changes on the base side show up reversed. It backs the compare page's
// "base..head" URLs.
func (s *Store) ChangedFilesDirect(ctx context.Context, pk int64, base, head SHA) ([]FileChange, error) {
	return s.changedFiles(ctx, pk, base, head, true, false)
}

// ChangedFilesOpts is ChangedFiles with the range form and the whitespace mode
// chosen by the caller. ignoreWhitespace passes git's -w so a hunk whose only
// change is whitespace drops out, the body GitHub's "Hide whitespace" (?w=1)
// serves. The whitespace-ignored diff is a separate view with its own line
// counts and offsets, so it is cached under a distinct key and never reused as
// the canonical diff the review anchors resolve against.
func (s *Store) ChangedFilesOpts(ctx context.Context, pk int64, base, head SHA, direct, ignoreWhitespace bool) ([]FileChange, error) {
	return s.changedFiles(ctx, pk, base, head, direct, ignoreWhitespace)
}

// changedFiles runs the diff for both range forms. A direct diff, and a
// whitespace-ignored one, each cache under a prefixed key: the variants answer
// differently for the same end pair, and a key collision would serve one as
// another.
func (s *Store) changedFiles(ctx context.Context, pk int64, base, head SHA, direct, ignoreWhitespace bool) ([]FileChange, error) {
	cacheable := s.diffs != nil && isFullSHA(base) && isFullSHA(head)
	key := ""
	if cacheable {
		key = diffKey(pk, base, head)
		if direct {
			key = "direct:" + key
		}
		if ignoreWhitespace {
			key = "w:" + key
		}
		if files := s.diffs.get(key); files != nil {
			return files, nil
		}
	}
	args := []string{"diff", "--no-color", "--full-index", "--find-renames"}
	if ignoreWhitespace {
		args = append(args, "-w")
	}
	if direct {
		args = append(args, base, head)
	} else {
		args = append(args, base+"..."+head)
	}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return nil, err
	}
	if r.code != 0 {
		return nil, fail(args, r)
	}
	files := parseDiff(string(r.stdout))
	if cacheable && files != nil {
		s.diffs.put(key, files)
	}
	return files, nil
}

// DiffRaw returns the unified diff between base and head over the three-dot
// range, the body the .diff media type serves and gh pr diff prints.
func (s *Store) DiffRaw(ctx context.Context, pk int64, base, head SHA) ([]byte, error) {
	args := []string{"diff", "--no-color", base + "..." + head}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return nil, err
	}
	if r.code != 0 {
		return nil, fail(args, r)
	}
	return r.stdout, nil
}

// FormatPatch returns the head's commits as an mbox patch series, the body the
// .patch media type serves. The range is base..head so only the pull request's
// own commits appear.
func (s *Store) FormatPatch(ctx context.Context, pk int64, base, head SHA) ([]byte, error) {
	args := []string{"format-patch", "--stdout", "--no-color", base + ".." + head}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return nil, err
	}
	if r.code != 0 {
		return nil, fail(args, r)
	}
	return r.stdout, nil
}

// DiffDirect returns the plain two-point diff between base and head, no merge
// base involved: the body the two-dot compare's .diff form and the commit
// page's .diff form serve. Either end may be a tree id, which is how a root
// commit diffs against the empty tree.
func (s *Store) DiffDirect(ctx context.Context, pk int64, base, head SHA) ([]byte, error) {
	args := []string{"diff", "--no-color", "--end-of-options", base, head}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return nil, err
	}
	if r.code != 0 {
		return nil, fail(args, r)
	}
	return r.stdout, nil
}

// FormatPatchCommit returns one commit as a format-patch mail, the body the
// commit page's .patch form serves. format-patch -1 handles a root commit
// without a parent.
func (s *Store) FormatPatchCommit(ctx context.Context, pk int64, sha SHA) ([]byte, error) {
	args := []string{"format-patch", "-1", "--stdout", "--no-color", "--end-of-options", string(sha)}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return nil, err
	}
	if r.code != 0 {
		return nil, fail(args, r)
	}
	return r.stdout, nil
}

// TestMerge performs a three-way merge of base and head in memory and reports
// whether it is conflict-free, writing the merged tree to the object store but
// touching no ref. clean is true when git produced a tree with no conflicts;
// tree is that merged tree's id. It is the heart of the mergeability worker.
func (s *Store) TestMerge(ctx context.Context, pk int64, base, head SHA) (tree SHA, clean bool, err error) {
	args := []string{"merge-tree", "--write-tree", base, head}
	r, err := s.run(ctx, pk, nil, args...)
	if err != nil {
		return "", false, err
	}
	// merge-tree exits 0 for a clean merge, 1 when it had to record conflicts,
	// and 128 for a real failure. The first line of stdout is the tree id.
	line := firstLine(string(r.stdout))
	switch r.code {
	case 0:
		return line, true, nil
	case 1:
		return line, false, nil
	default:
		return "", false, fail(args, r)
	}
}

// Merge lands head into base by the given method and returns the new tip commit.
// It writes only objects, never a ref, so the caller advances refs/heads/<base>
// to the returned commit under its own locking. ok is false when the strategy
// cannot apply cleanly (a conflicting test merge, or a rebase over a merge
// commit), in which case no objects matter and no ref should move.
func (s *Store) Merge(ctx context.Context, pk int64, method MergeMethod, base, head SHA, message string, author, committer Signature) (sha SHA, ok bool, err error) {
	switch method {
	case MergeCommit:
		return s.mergeCommit(ctx, pk, base, head, message, author, committer)
	case MergeSquash:
		return s.squashCommit(ctx, pk, base, head, message, author, committer)
	case MergeRebase:
		return s.rebaseOnto(ctx, pk, base, head, committer)
	default:
		return "", false, fmt.Errorf("git: unknown merge method %q", method)
	}
}

// mergeCommit writes a two-parent merge commit of base and head.
func (s *Store) mergeCommit(ctx context.Context, pk int64, base, head SHA, message string, author, committer Signature) (SHA, bool, error) {
	tree, clean, err := s.TestMerge(ctx, pk, base, head)
	if err != nil || !clean {
		return "", false, err
	}
	sha, err := s.commitTree(ctx, pk, tree, []SHA{base, head}, message, author, committer)
	return sha, err == nil, err
}

// squashCommit writes a single commit on top of base carrying head's merged
// content, the squash strategy: same merged tree as a merge commit, one parent.
func (s *Store) squashCommit(ctx context.Context, pk int64, base, head SHA, message string, author, committer Signature) (SHA, bool, error) {
	tree, clean, err := s.TestMerge(ctx, pk, base, head)
	if err != nil || !clean {
		return "", false, err
	}
	sha, err := s.commitTree(ctx, pk, tree, []SHA{base}, message, author, committer)
	return sha, err == nil, err
}

// rebaseOnto replays head's commits onto base one at a time, preserving each
// commit's author and stamping the merger as committer. It refuses (ok false) a
// commit with more than one parent, since a linear replay has no honest meaning
// across a merge.
func (s *Store) rebaseOnto(ctx context.Context, pk int64, base, head SHA, committer Signature) (SHA, bool, error) {
	commits, err := s.CommitsBetween(ctx, pk, base, head)
	if err != nil {
		return "", false, err
	}
	tip := base
	for _, c := range commits {
		if len(c.Parents) != 1 {
			return "", false, nil
		}
		args := []string{"merge-tree", "--write-tree", "--merge-base=" + c.Parents[0], tip, c.SHA}
		r, err := s.run(ctx, pk, nil, args...)
		if err != nil {
			return "", false, err
		}
		if r.code == 1 {
			return "", false, nil // a conflict somewhere in the replay
		}
		if r.code != 0 {
			return "", false, fail(args, r)
		}
		tree := firstLine(string(r.stdout))
		next, err := s.commitTree(ctx, pk, tree, []SHA{tip}, c.Message, c.Author, committer)
		if err != nil {
			return "", false, err
		}
		tip = next
	}
	return tip, true, nil
}

// commitTree writes a commit object for tree with the given parents, message,
// and identities, and returns its id. Author and committer identity ride in on
// the environment, the channel commit-tree reads them from.
func (s *Store) commitTree(ctx context.Context, pk int64, tree SHA, parents []SHA, message string, author, committer Signature) (SHA, error) {
	args := []string{"commit-tree", tree}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	env := []string{
		"GIT_AUTHOR_NAME=" + author.Name,
		"GIT_AUTHOR_EMAIL=" + author.Email,
		"GIT_AUTHOR_DATE=" + gitDate(author.When),
		"GIT_COMMITTER_NAME=" + committer.Name,
		"GIT_COMMITTER_EMAIL=" + committer.Email,
		"GIT_COMMITTER_DATE=" + gitDate(committer.When),
	}
	r, err := s.runEnv(ctx, pk, env, strings.NewReader(message), args...)
	if err != nil {
		return "", err
	}
	if r.code != 0 {
		return "", fail(args, r)
	}
	return strings.TrimSpace(string(r.stdout)), nil
}

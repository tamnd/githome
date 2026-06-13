package git

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Blame used to run go-git's in-process algorithm with no deadline and no size
// cap, which could pin a goroutine for tens of seconds on a deep file; a
// commit's patch was likewise assembled whole in memory whatever its size.
// Both are pure functions of full object ids, so each gets a subprocess fast
// path under a deadline and a content-addressed cache. The go-git paths remain
// as the fallback for a Repo opened without a store and for failed runs, which
// also keeps the error mapping for bad refs and missing paths where the
// callers expect it.

// blameTimeout bounds one git blame subprocess. A file whose history cannot be
// annotated in this window is one the server refuses to pin a request on.
const blameTimeout = 15 * time.Second

// commitPatchTimeout bounds one git diff-tree subprocess.
const commitPatchTimeout = 30 * time.Second

// commitPatchMaxBytes caps the patch text CommitPatch materializes. The FE
// commit page inlines far less; past this cap the patch is cut and the page's
// own truncation notice covers the rest.
const commitPatchMaxBytes = 8 << 20

// blameCacheMaxBytes / patchCacheMaxBytes bound the two content-addressed
// caches; the per-entry caps keep one huge file or diff from evicting the rest.
const (
	blameCacheMaxBytes      = 16 << 20
	blameCacheMaxEntryBytes = 2 << 20
	patchCacheMaxBytes      = 32 << 20
	patchCacheMaxEntryBytes = 2 << 20
)

func blameCost(lines []BlameLine) int64 {
	var n int64
	for i := range lines {
		n += int64(len(lines[i].Text) + len(lines[i].AuthorName) + len(lines[i].AuthorEmail) + 96)
	}
	return n
}

// blameBatch is Blame as one git blame --porcelain subprocess under a deadline.
// handled is false when the Repo has no store or the run failed, in which case
// the caller falls back to go-git; when handled is true the result, including
// a size-cap refusal, is final.
func (r *Repo) blameBatch(ref, path string) (lines []BlameLine, handled bool, err error) {
	if r.store == nil {
		return nil, false, nil
	}
	c, cerr := r.commitFromRev(ref)
	if cerr != nil {
		return nil, false, nil
	}
	sha := c.Hash.String()
	key := strconv.FormatInt(r.pk, 10) + ":" + sha + ":" + path
	if v, ok := r.store.blames.get(key); ok {
		return v, true, nil
	}
	if r.maxBlobBytes > 0 {
		res, rerr := r.store.run(context.Background(), r.pk, nil, "cat-file", "-s", sha+":"+path)
		if rerr != nil || res.code != 0 {
			return nil, false, nil
		}
		if n, perr := strconv.ParseInt(strings.TrimSpace(string(res.stdout)), 10, 64); perr == nil && n > r.maxBlobBytes {
			return nil, true, ErrBlobTooLarge
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), blameTimeout)
	defer cancel()
	res, rerr := r.store.run(ctx, r.pk, nil, "blame", "--porcelain", "--end-of-options", sha, "--", path)
	if rerr != nil || res.code != 0 {
		return nil, false, nil
	}
	lines, ok := parseBlamePorcelain(string(res.stdout))
	if !ok {
		return nil, false, nil
	}
	r.store.blames.put(key, lines)
	return lines, true, nil
}

// parseBlamePorcelain parses git blame --porcelain output. Commit metadata
// appears once per commit, on its first group; subsequent groups repeat only
// the header line, so attribution accumulates in a map keyed by sha.
func parseBlamePorcelain(out string) ([]BlameLine, bool) {
	type meta struct {
		name, email string
		when        time.Time
	}
	metas := map[string]*meta{}
	var cur *meta
	var curSHA string
	var lines []BlameLine
	for raw := range strings.SplitSeq(out, "\n") {
		if raw == "" {
			continue
		}
		if raw[0] == '\t' {
			if cur == nil {
				return nil, false
			}
			lines = append(lines, BlameLine{
				SHA:         curSHA,
				AuthorName:  cur.name,
				AuthorEmail: cur.email,
				When:        cur.when,
				Text:        raw[1:],
				LineNum:     len(lines) + 1,
			})
			continue
		}
		// A group header starts with the full object id and switches the
		// current commit; metadata lines fill it in on first sight.
		if sha, _, ok := strings.Cut(raw, " "); ok && isFullSHA(sha) {
			curSHA = sha
			if m, seen := metas[sha]; seen {
				cur = m
			} else {
				cur = &meta{}
				metas[sha] = cur
			}
			continue
		}
		if cur == nil {
			return nil, false
		}
		switch {
		case strings.HasPrefix(raw, "author "):
			cur.name = raw[len("author "):]
		case strings.HasPrefix(raw, "author-mail "):
			cur.email = strings.Trim(raw[len("author-mail "):], "<>")
		case strings.HasPrefix(raw, "author-time "):
			if n, err := strconv.ParseInt(raw[len("author-time "):], 10, 64); err == nil {
				cur.when = time.Unix(n, 0).UTC()
			}
			// committer, summary, filename, previous, boundary, ... are skipped.
		}
	}
	return lines, true
}

// commitPatchBatch is CommitPatch as one git diff-tree subprocess under a
// deadline, capped at commitPatchMaxBytes. handled is false when the Repo has
// no store or the run failed, in which case the caller falls back to go-git.
func (r *Repo) commitPatchBatch(sha string) (patch string, handled bool) {
	if r.store == nil {
		return "", false
	}
	c, err := r.commitFromRev(sha)
	if err != nil {
		return "", false
	}
	if c.NumParents() == 0 {
		return "", true
	}
	full := c.Hash.String()
	key := strconv.FormatInt(r.pk, 10) + ":" + full
	if v, ok := r.store.patches.get(key); ok {
		return v, true
	}
	parent := c.ParentHashes[0].String()
	ctx, cancel := context.WithTimeout(context.Background(), commitPatchTimeout)
	defer cancel()
	out, _, code, rerr := r.store.runLimited(ctx, r.pk, commitPatchMaxBytes,
		"diff-tree", "-p", "--no-commit-id", "--end-of-options", parent, full)
	if rerr != nil || code != 0 {
		return "", false
	}
	patch = string(out)
	r.store.patches.put(key, patch)
	return patch, true
}
